import { describe, expect, it, vi } from 'vitest'

import { ConviaError, Unreachable, isConviaError, isUnreachable } from './errors.js'
import { Convia, Timeout, isTimeout } from './transport.js'

/*
answering builds a client whose fetch is whatever the test needs, and records
what it was asked, so a test can say what the client sent as well as what it
did with the answer.
*/
interface Asked {
  request: Request
  /*
  The options as the transport passed them, kept beside the Request.

  **The Request alone cannot be asserted against**, which is the trap here: its
  constructor fills in defaults, and `credentials` defaults to `'same-origin'`
  -- so a test reading `request.credentials` passes whether the transport set it
  or not. Reading `init` is reading what the transport actually did.
  */
  init: RequestInit | undefined
}

function answering(reply: (request: Request) => Response | Promise<Response>) {
  const asked: Asked[] = []
  const client = new Convia({
    fetch: (input, init) => {
      const request = new Request(new URL(String(input), 'http://convia.test'), init)
      asked.push({ request, init })
      return Promise.resolve(reply(request))
    },
  })
  return { client, asked }
}

const json = (status: number, body: unknown, headers: Record<string, string> = {}) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json', ...headers },
  })

describe('what the client sends', () => {
  it('puts the version prefix on every path exactly once', async () => {
    const { client, asked } = answering(() => json(200, {}))
    await client.get('/me')
    expect(new URL(asked[0]!.request.url).pathname).toBe('/v1/me')
  })

  it('leaves out a query parameter that was not given rather than sending it empty', async () => {
    const { client, asked } = answering(() => json(200, {}))
    await client.get('/me/rooms', { query: { before: undefined, limit: 50, cursor: '' } })

    const url = new URL(asked[0]!.request.url)
    expect(url.searchParams.get('limit')).toBe('50')
    expect(url.searchParams.has('before')).toBe(false)
    expect(url.searchParams.has('cursor')).toBe(false)
  })

  /*
  The session is a cookie the page cannot read, so this is the whole
  authentication model. A request that forgot it is a request that arrives
  signed in as nobody, and the symptom is a 401 nothing explains.
  */
  it('sends the session cookie, which is the only credential there is', async () => {
    const { client, asked } = answering(() => json(200, {}))
    await client.get('/me')
    expect(asked[0]!.init?.credentials).toBe('same-origin')
  })

  it('declares JSON only when it is carrying some', async () => {
    const { client, asked } = answering(() => json(200, {}))

    await client.get('/me')
    expect(asked[0]!.request.headers.get('content-type')).toBeNull()

    await client.post('/v1/me/rooms', { body: { name: 'Standup' } })
    expect(asked[1]!.request.headers.get('content-type')).toBe('application/json')
    expect(await asked[1]!.request.text()).toBe('{"name":"Standup"}')
  })

  /*
  A caller setting Accept or Content-Type by hand would be setting the two
  things the transport is responsible for, and a wrong Content-Type is refused
  by Convia with a code that says nothing about what the caller did.
  */
  it('does not let a caller override the headers it is responsible for', async () => {
    const { client, asked } = answering(() => json(200, {}))
    await client.post('/me', {
      body: {},
      headers: { Accept: 'text/html', 'Content-Type': 'text/plain', 'X-Mine': 'kept' },
    })

    expect(asked[0]!.request.headers.get('accept')).toBe('application/json')
    expect(asked[0]!.request.headers.get('content-type')).toBe('application/json')
    expect(asked[0]!.request.headers.get('x-mine')).toBe('kept')
  })
})

/*
A browser's `fetch` refuses to be called detached from `window`, and Node's does
not care -- so every other test here passes against a client that would fail on
its first request in the one environment this package is for.

This simulates the browser by making the global throw exactly as it does. It was
written after a real browser found it, which is the honest order: no unit test
could have, because they all supply a fetch of their own.
*/
describe('calling the fetch it did not make', () => {
  it('calls the global fetch bound to the global, not detached', async () => {
    const real = globalThis.fetch
    try {
      const picky = function (this: unknown) {
        if (this !== globalThis) {
          throw new TypeError("Failed to execute 'fetch' on 'Window': Illegal invocation")
        }
        return Promise.resolve(new Response('{}', { status: 200 }))
      }
      globalThis.fetch = picky as unknown as typeof globalThis.fetch

      await expect(new Convia().get('/me')).resolves.toEqual({})
    } finally {
      globalThis.fetch = real
    }
  })
})

describe('what the client makes of an answer', () => {
  it('reads an empty answer as nothing rather than failing to parse it', async () => {
    const { client } = answering(() => new Response(null, { status: 204 }))
    await expect(client.delete('/me/sessions/current')).resolves.toBeUndefined()
  })

  it('unwraps a refusal and keeps what it is for', async () => {
    const { client } = answering(() =>
      json(403, { error: { code: 'wrong_password', message: 'No.', request_id: 'req_1' } }),
    )

    await expect(client.post('/me/password')).rejects.toSatisfy((thrown: unknown) => {
      expect(isConviaError(thrown)).toBe(true)
      const refusal = thrown as ConviaError
      expect(refusal.status).toBe(403)
      expect(refusal.code).toBe('wrong_password')
      expect(refusal.requestId).toBe('req_1')
      expect(refusal.unauthenticated).toBe(false)
      return true
    })
  })

  /*
  A proxy's HTML error page is still a refusal, and the status is the part that
  still means something. Failing to parse it would throw away the one thing the
  caller could have acted on.
  */
  it('keeps the status of a refusal Convia did not shape', async () => {
    const { client } = answering(() => new Response('<h1>502 Bad Gateway</h1>', { status: 502 }))

    const thrown = await client.get('/me').catch((error: unknown) => error)
    expect(isConviaError(thrown)).toBe(true)
    expect((thrown as ConviaError).status).toBe(502)
    expect((thrown as ConviaError).code).toBe('unknown')
  })

  it('carries a code from an installation newer than itself', async () => {
    const { client } = answering(() =>
      json(422, { error: { code: 'quota_exhausted', message: 'Later.' } }),
    )

    const thrown = await client.get('/me').catch((error: unknown) => error)
    expect((thrown as ConviaError).code).toBe('quota_exhausted')
  })

  describe('how long to wait before trying again', () => {
    it('reads a wait stated in seconds', async () => {
      const { client } = answering(() =>
        json(429, { error: { code: 'rate_limited', message: 'Slow down.' } }, { 'Retry-After': '30' }),
      )

      const thrown = await client.get('/me').catch((error: unknown) => error)
      expect((thrown as ConviaError).retryAfter).toBe(30)
      expect((thrown as ConviaError).retryable).toBe(true)
    })

    it('reads a wait stated as a date', async () => {
      const when = new Date(Date.now() + 60_000).toUTCString()
      const { client } = answering(() =>
        json(503, { error: { code: 'unavailable', message: 'Later.' } }, { 'Retry-After': when }),
      )

      const thrown = await client.get('/me').catch((error: unknown) => error)
      expect((thrown as ConviaError).retryAfter).toBeGreaterThan(55)
      expect((thrown as ConviaError).retryAfter).toBeLessThanOrEqual(60)
    })

    /*
    An unstated wait is undefined, not zero. A caller reading "not stated" as
    "now" is how a rate limit becomes an outage.
    */
    it('leaves an unstated or unreadable wait undefined rather than zero', async () => {
      for (const headers of [{}, { 'Retry-After': 'soon' }]) {
        const { client } = answering(() =>
          json(429, { error: { code: 'rate_limited', message: 'Slow down.' } }, headers),
        )
        const thrown = await client.get('/me').catch((error: unknown) => error)
        expect((thrown as ConviaError).retryAfter).toBeUndefined()
      }
    })

    it('reads a date already past as now rather than as a negative wait', async () => {
      const when = new Date(Date.now() - 60_000).toUTCString()
      const { client } = answering(() =>
        json(503, { error: { code: 'unavailable', message: 'Later.' } }, { 'Retry-After': when }),
      )

      const thrown = await client.get('/me').catch((error: unknown) => error)
      expect((thrown as ConviaError).retryAfter).toBe(0)
    })
  })
})

/*
The three ways a request ends without an answer, which a client that confuses
them reports wrongly: the caller stopped it, the deadline passed, or Convia was
never reached. Each needs different words, and two of them are not failures to
show anybody.
*/
describe('stopping, giving up, and not arriving', () => {
  it('reports an unreachable Convia as unreachable, not as a refusal', async () => {
    const cause = new TypeError('failed to fetch')
    const client = new Convia({ fetch: () => Promise.reject(cause) })

    const thrown = await client.get('/me').catch((error: unknown) => error)
    expect(isUnreachable(thrown)).toBe(true)
    expect(isConviaError(thrown)).toBe(false)
    expect((thrown as Unreachable).cause).toBe(cause)
  })

  /*
  A caller's abort is something they did on purpose. Reporting it as a network
  failure would show an error for a page the person navigated away from.
  */
  it('lets a caller the abort they raised, rather than wrapping it', async () => {
    const stop = new AbortController()
    const client = new Convia({
      fetch: (_input, init) =>
        new Promise((_resolve, reject) => {
          init?.signal?.addEventListener('abort', () => {
            reject(new DOMException('The user aborted a request.', 'AbortError'))
          })
        }),
    })

    const asking = client.get('/me', { signal: stop.signal })
    stop.abort()

    const thrown = await asking.catch((error: unknown) => error)
    expect(isUnreachable(thrown)).toBe(false)
    expect(isTimeout(thrown)).toBe(false)
    expect((thrown as Error).name).toBe('AbortError')
  })

  /*
  A deadline passing is a fact about the installation, not something the caller
  did, so it is its own type: a caller may want to show it, retry it or count
  it, and none of those are things to do about an abort they raised themselves.
  */
  it('reports its own deadline as a timeout, not as the caller stopping', async () => {
    vi.useFakeTimers()
    try {
      const client = new Convia({
        timeout: 50,
        fetch: (_input, init) =>
          new Promise((_resolve, reject) => {
            init?.signal?.addEventListener('abort', () => {
              reject(new DOMException('signal timed out', 'TimeoutError'))
            })
          }),
      })

      const asking = client.get('/me')
      const thrown = asking.catch((error: unknown) => error)
      await vi.advanceTimersByTimeAsync(60)

      const error = await thrown
      expect(isTimeout(error)).toBe(true)
      expect((error as Timeout).after).toBe(50)
      expect(isUnreachable(error)).toBe(false)
    } finally {
      vi.useRealTimers()
    }
  })

  it('lets one request wait longer than the client usually would', async () => {
    vi.useFakeTimers()
    try {
      const client = new Convia({
        timeout: 20,
        fetch: (_input, init) =>
          new Promise((resolve, reject) => {
            init?.signal?.addEventListener('abort', () => {
              reject(new DOMException('signal timed out', 'TimeoutError'))
            })
            setTimeout(() => resolve(json(200, { ok: true })), 50)
          }),
      })

      const asking = client.get('/me/export', { timeout: 200 })
      await vi.advanceTimersByTimeAsync(60)
      await expect(asking).resolves.toEqual({ ok: true })
    } finally {
      vi.useRealTimers()
    }
  })

  /*
  Zero means no deadline, and that is asserted by **no signal being passed at
  all** rather than by waiting to see nothing happen.

  Waiting was the first attempt and it could not fail: `AbortSignal.timeout` is
  native and does not go through the timer a fake-timer clock replaces, so
  advancing the clock never fired it and the assertion held whether or not a
  deadline had been set. What the transport does is observable directly, so it
  is observed directly.
  */
  it('passes no deadline at all when told to wait forever', async () => {
    const seen: (AbortSignal | null | undefined)[] = []
    const patient = new Convia({
      timeout: 0,
      fetch: (_input, init) => {
        seen.push(init?.signal)
        return Promise.resolve(json(200, { ok: true }))
      },
    })

    await expect(patient.get('/me')).resolves.toEqual({ ok: true })
    expect(seen[0]).toBeUndefined()
  })

  /*
  And a caller's own cancellation still reaches fetch when there is no deadline
  to compose it with -- the composition must not be what carries it.
  */
  it("still forwards the caller's cancellation when there is no deadline", async () => {
    const stop = new AbortController()
    const seen: (AbortSignal | null | undefined)[] = []
    const patient = new Convia({
      timeout: 0,
      fetch: (_input, init) => {
        seen.push(init?.signal)
        return Promise.resolve(json(200, { ok: true }))
      },
    })

    await patient.get('/me', { signal: stop.signal })
    expect(seen[0]).toBeDefined()
    expect(seen[0]?.aborted).toBe(false)
  })
})
