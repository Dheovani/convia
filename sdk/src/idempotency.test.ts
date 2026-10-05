import { describe, expect, it, vi } from 'vitest'

import { ConviaError, Unreachable } from './errors.js'
import { checkIdempotencyKey, newIdempotencyKey, withRetries } from './idempotency.js'
import { Convia, Timeout } from './transport.js'

const refusal = (status: number, code: string, retryAfter?: number) => {
  const thrown = new ConviaError(status, { code: code as never, message: 'No.' })
  thrown.retryAfter = retryAfter
  return thrown
}

/*
Retries wait, and a test that waited with them would take as long as the thing
it is testing. The clock is faked and advanced, so what is measured is the
decision rather than the delay.
*/
async function running<T>(work: () => Promise<T>): Promise<T> {
  vi.useFakeTimers()
  try {
    const settled = work()
    const caught = settled.catch(() => undefined)
    // Long enough for every backoff the policies below could ask for.
    await vi.advanceTimersByTimeAsync(120_000)
    await caught
    return await settled
  } finally {
    vi.useRealTimers()
  }
}

describe('the key itself', () => {
  it('refuses what Convia would refuse, before the request is made', () => {
    expect(() => checkIdempotencyKey('')).toThrow(TypeError)
    expect(() => checkIdempotencyKey('x'.repeat(256))).toThrow(TypeError)
    expect(() => checkIdempotencyKey('x'.repeat(255))).not.toThrow()
    expect(() => checkIdempotencyKey(newIdempotencyKey())).not.toThrow()
  })

  it('mints a different one every time, which is the only property that matters', () => {
    const minted = new Set(Array.from({ length: 200 }, newIdempotencyKey))
    expect(minted.size).toBe(200)
  })
})

describe('what the transport sends it on', () => {
  function answering(reply: () => Response) {
    const sent: (Headers | undefined)[] = []
    const client = new Convia({
      fetch: (input, init) => {
        sent.push(new Request(new URL(String(input), 'http://convia.test'), init).headers)
        return Promise.resolve(reply())
      },
    })
    return { client, sent }
  }

  const ok = () => new Response('{}', { status: 200, headers: { 'Content-Type': 'application/json' } })

  it('sends it on a request that changes something', async () => {
    const { client, sent } = answering(ok)
    await client.post('/me/rooms', { body: { name: 'Standup' }, idempotencyKey: 'abc' })
    expect(sent[0]?.get('idempotency-key')).toBe('abc')
  })

  /*
  A GET carrying one would ask Convia to remember an answer nothing is going to
  repeat, and the key would occupy a window it was not needed in.
  */
  it('leaves it off a request that only reads', async () => {
    const { client, sent } = answering(ok)
    await client.get('/me/rooms', { idempotencyKey: 'abc' })
    expect(sent[0]?.get('idempotency-key')).toBeNull()
  })

  it('refuses an unusable key rather than sending it and being refused', async () => {
    const { client, sent } = answering(ok)
    await expect(client.post('/me/rooms', { idempotencyKey: '' })).rejects.toThrow(TypeError)
    await expect(client.post('/me/rooms', { idempotencyKey: 'x'.repeat(256) })).rejects.toThrow(TypeError)
    expect(sent).toHaveLength(0)
  })
})

describe('carrying one key across every attempt', () => {
  /*
  This is the whole design. A key minted per attempt is a different key each
  time, and Convia would treat each as a new request -- which is the duplicate
  the key exists to prevent.
  */
  it('hands the same key to every attempt', async () => {
    const keys: string[] = []
    let attempts = 0

    await running(() =>
      withRetries(async (key) => {
        keys.push(key)
        attempts += 1
        if (attempts < 3) {
          throw refusal(503, 'unavailable')
        }
        return 'done'
      }),
    )

    expect(keys).toHaveLength(3)
    expect(new Set(keys).size).toBe(1)
  })

  it('uses the key it was given rather than minting one', async () => {
    const keys: string[] = []
    await running(() => withRetries(async (key) => void keys.push(key), 'mine'))
    expect(keys).toEqual(['mine'])
  })

  it('counts attempts from one, so a caller can log which it is on', async () => {
    const seen: number[] = []
    let attempts = 0

    await running(() =>
      withRetries(async (_key, attempt) => {
        seen.push(attempt)
        attempts += 1
        if (attempts < 3) {
          throw refusal(429, 'rate_limited')
        }
      }),
    )

    expect(seen).toEqual([1, 2, 3])
  })
})

describe('what is worth attempting again', () => {
  async function counting(thrown: unknown) {
    let attempts = 0
    const work = async () => {
      attempts += 1
      throw thrown
    }
    await running(() => withRetries(work, 'mine')).catch(() => undefined)
    return attempts
  }

  it('attempts again what Convia said could end differently', async () => {
    expect(await counting(refusal(429, 'rate_limited'))).toBe(3)
    expect(await counting(refusal(503, 'unavailable'))).toBe(3)
  })

  /*
  Everything else is the caller's to fix. A client that retries a 400 retries it
  forever, and turns one wrong request into a thousand.
  */
  it('does not attempt again what will be refused the same way', async () => {
    for (const [status, code] of [
      [400, 'invalid_request'],
      [401, 'unauthenticated'],
      [403, 'forbidden'],
      [404, 'not_found'],
      [413, 'payload_too_large'],
      [500, 'internal_error'],
    ] as const) {
      expect(await counting(refusal(status, code)), `${status}`).toBe(1)
    }
  })

  /*
  Convia answers two different things with 409: a key reused for a different
  request, which never succeeds, and a request with this key still running,
  which succeeds once the first finishes. They carry the same code and differ
  only in the message, so the permanent reading is taken -- retrying the
  permanent one forever is worse than not collecting the other one's result.
  */
  it('does not attempt a conflict again, because it cannot tell which conflict it is', async () => {
    expect(await counting(refusal(409, 'conflict'))).toBe(1)
  })

  /*
  A request that got no answer may have arrived and done its work. Trying again
  is safe only because a key is carrying it, and that is the case the key was
  invented for.
  */
  it('attempts again a request that got no answer at all', async () => {
    expect(await counting(new Unreachable(new TypeError('failed to fetch')))).toBe(3)
    expect(await counting(new Timeout(30_000))).toBe(3)
  })

  it('never attempts again something that is not a failure it understands', async () => {
    expect(await counting(new TypeError('a bug in the caller'))).toBe(1)
  })

  it('throws what the last attempt threw, not a summary of them', async () => {
    const last = refusal(503, 'unavailable')
    await expect(running(() => withRetries(async () => { throw last }, 'mine'))).rejects.toBe(last)
  })
})

describe('how long it waits', () => {
  it('waits as long as Convia asked, rather than as long as it guessed', async () => {
    const waits: number[] = []
    let at = 0
    let attempts = 0

    vi.useFakeTimers()
    try {
      const settled = withRetries(
        async () => {
          attempts += 1
          waits.push(Date.now() - at)
          at = Date.now()
          if (attempts < 2) {
            throw refusal(429, 'rate_limited', 5)
          }
        },
        'mine',
      )
      const caught = settled.catch(() => undefined)
      await vi.advanceTimersByTimeAsync(60_000)
      await caught
      await settled
    } finally {
      vi.useRealTimers()
    }

    // The second attempt waited the five seconds the header stated.
    expect(waits[1]).toBe(5000)
  })

  it('never waits longer than it was told to, however long Convia asked', async () => {
    const waits: number[] = []
    let at = 0
    let attempts = 0

    vi.useFakeTimers()
    try {
      const settled = withRetries(
        async () => {
          attempts += 1
          waits.push(Date.now() - at)
          at = Date.now()
          if (attempts < 2) {
            throw refusal(503, 'unavailable', 86_400)
          }
        },
        'mine',
        { longestBackoff: 2_000 },
      )
      const caught = settled.catch(() => undefined)
      await vi.advanceTimersByTimeAsync(60_000)
      await caught
      await settled
    } finally {
      vi.useRealTimers()
    }

    expect(waits[1]).toBe(2000)
  })

  /*
  The last attempt does not wait before giving up: sleeping and then re-throwing
  is time somebody spends learning nothing.
  */
  it('does not wait after the attempt it is not going to repeat', async () => {
    let attempts = 0
    const started = Date.now()

    vi.useFakeTimers()
    try {
      const settled = withRetries(
        async () => {
          attempts += 1
          throw refusal(503, 'unavailable')
        },
        'mine',
        { attempts: 1 },
      )
      const caught = settled.catch(() => undefined)
      await vi.advanceTimersByTimeAsync(0)
      await caught
      expect(Date.now() - started).toBe(0)
    } finally {
      vi.useRealTimers()
    }

    expect(attempts).toBe(1)
  })

  it('refuses a policy that would never attempt anything', async () => {
    await expect(withRetries(async () => 'x', 'mine', { attempts: 0 })).rejects.toThrow(TypeError)
  })
})
