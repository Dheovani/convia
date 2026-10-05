import { describe, expect, it, vi } from 'vitest'

import { EventStream, addressOf, type Condition, type ConviaEvent } from './events.js'

/*
A socket the test drives: it records where it was opened and lets the test
decide when it opens, what it carries, and how it closes.
*/
class FakeSocket {
  static opened: FakeSocket[] = []

  onopen: (() => void) | null = null
  onmessage: ((message: MessageEvent) => void) | null = null
  onclose: ((closed: CloseEvent) => void) | null = null

  closedByCaller = false

  constructor(readonly url: string) {
    FakeSocket.opened.push(this)
  }

  close(): void {
    this.closedByCaller = true
  }

  // What the server does, as far as a client can tell.
  opens(): void {
    this.onopen?.()
  }

  carries(event: Partial<ConviaEvent> & Record<string, unknown>): void {
    this.onmessage?.({ data: JSON.stringify(event) } as MessageEvent)
  }

  carriesRaw(data: string): void {
    this.onmessage?.({ data } as MessageEvent)
  }

  closes(code = 1006): void {
    this.onclose?.({ code } as CloseEvent)
  }
}

function streaming(options: { backoff?: number; longestBackoff?: number } = {}) {
  FakeSocket.opened = []
  const stream = new EventStream({
    origin: 'https://convia.test',
    WebSocket: FakeSocket as unknown as typeof globalThis.WebSocket,
    backoff: options.backoff ?? 1000,
    ...(options.longestBackoff === undefined ? {} : { longestBackoff: options.longestBackoff }),
  })
  return { stream, sockets: FakeSocket.opened }
}

const anEvent = (cursor?: string): Partial<ConviaEvent> & Record<string, unknown> => ({
  id: 'evt_4XZQP7KN2VJH6TBWMDR3YAFC5E',
  type: 'message.posted',
  ...(cursor === undefined ? {} : { cursor }),
})

describe('where it connects', () => {
  it('speaks wss from an encrypted origin and ws from a plain one', () => {
    expect(addressOf('https://convia.test')).toBe('wss://convia.test/v1/me/events')
    expect(addressOf('http://localhost:8080')).toBe('ws://localhost:8080/v1/me/events')
  })

  it('carries the cursor it is resuming from, encoded', () => {
    expect(addressOf('https://convia.test', 'abc 1')).toBe('wss://convia.test/v1/me/events?after=abc%201')
  })
})

describe('delivering what arrives', () => {
  it('gives every listener every event', () => {
    const { stream, sockets } = streaming()
    const first: string[] = []
    const second: string[] = []
    stream.listen((event) => first.push(event.id))
    stream.listen((event) => second.push(event.id))

    sockets[0]!.opens()
    sockets[0]!.carries(anEvent())

    expect(first).toHaveLength(1)
    expect(second).toHaveLength(1)
  })

  it('stops delivering to a listener that unsubscribed', () => {
    const { stream, sockets } = streaming()
    const heard: string[] = []
    const stop = stream.listen((event) => heard.push(event.id))

    sockets[0]!.opens()
    sockets[0]!.carries(anEvent())
    stop()
    sockets[0]!.carries(anEvent())

    expect(heard).toHaveLength(1)
  })

  /*
  A screen closing on the very event that closed it is the ordinary case, and
  walking a set somebody is removing from is how one of the others silently
  stops being called.
  */
  it('still tells the others when one unsubscribes mid-delivery', () => {
    const { stream, sockets } = streaming()
    const heard: string[] = []

    const stop = stream.listen(() => {
      heard.push('first')
      stop()
    })
    stream.listen(() => heard.push('second'))

    sockets[0]!.opens()
    sockets[0]!.carries(anEvent())

    expect(heard).toEqual(['first', 'second'])
  })

  /*
  One consumer's bug is theirs. It must not stop the rest being told, and it
  must not take the connection down.
  */
  it('tells the others when one throws, and stays connected', () => {
    const { stream, sockets } = streaming()
    const heard: string[] = []

    stream.listen(() => {
      throw new Error('a bug in the consumer')
    })
    stream.listen(() => heard.push('second'))

    sockets[0]!.opens()
    expect(() => sockets[0]!.carries(anEvent())).not.toThrow()
    expect(heard).toEqual(['second'])
    expect(stream.condition.kind).toBe('live')
  })

  it('ignores something that is not an event rather than dropping the connection', () => {
    const { stream, sockets } = streaming()
    const heard: string[] = []
    stream.listen((event) => heard.push(event.id))

    sockets[0]!.opens()
    sockets[0]!.carriesRaw('not json at all')
    sockets[0]!.carries(anEvent())

    expect(heard).toHaveLength(1)
    expect(stream.condition.kind).toBe('live')
  })

  /*
  This is what the copy of the set is for, and the case the obvious test misses.

  Removing during delivery is handled by a Set on its own: an entry already
  visited is gone and the rest are still walked. **Adding** is not. Without the
  copy, a listener registered by another listener hears the very event that
  registered it, which makes what a listener sees depend on the order the others
  happen to be in.
  */
  it('gives a listener added during delivery the next event, not this one', () => {
    const { stream, sockets } = streaming()
    const heard: string[] = []

    stream.listen(() => {
      heard.push('first')
      if (heard.length === 1) {
        stream.listen(() => heard.push('late'))
      }
    })

    sockets[0]!.opens()
    sockets[0]!.carries(anEvent())
    expect(heard).toEqual(['first'])

    sockets[0]!.carries(anEvent())
    expect(heard).toEqual(['first', 'first', 'late'])
  })

  it('unsubscribing twice does not remove somebody else', () => {
    const { stream, sockets } = streaming()
    const heard: string[] = []
    const stop = stream.listen(() => heard.push('first'))
    stream.listen(() => heard.push('second'))

    stop()
    stop()

    sockets[0]!.opens()
    sockets[0]!.carries(anEvent())
    expect(heard).toEqual(['second'])
  })
})

describe('coming back', () => {
  /*
  Reconnecting within one stream resumes by itself. This is the other case: a
  consumer that kept the cursor somewhere surviving the process -- a page
  reload, an application restarting -- and wants what happened while nothing was
  connected at all.
  */
  it('resumes from a cursor it was given, on its very first connection', () => {
    FakeSocket.opened = []
    const stream = new EventStream({
      origin: 'https://convia.test',
      WebSocket: FakeSocket as unknown as typeof globalThis.WebSocket,
      after: 'from-last-time',
    })

    expect(FakeSocket.opened[0]?.url).toBe('wss://convia.test/v1/me/events?after=from-last-time')
    stream.close()
  })

  it('resumes from the last cursor it saw', async () => {
    vi.useFakeTimers()
    try {
      const { sockets } = streaming({ backoff: 10 })
      sockets[0]!.opens()
      sockets[0]!.carries(anEvent('cursor-1'))
      sockets[0]!.carries(anEvent('cursor-2'))
      sockets[0]!.closes()

      await vi.advanceTimersByTimeAsync(20)
      expect(sockets[1]?.url).toBe('wss://convia.test/v1/me/events?after=cursor-2')
    } finally {
      vi.useRealTimers()
    }
  })

  /*
  Resuming is what 4002 refused. Reconnecting with the same cursor would be
  refused again, and again, for as long as anybody left it running.
  */
  it('stops resuming from a cursor Convia said is too old', async () => {
    vi.useFakeTimers()
    try {
      const { stream, sockets } = streaming({ backoff: 10 })
      sockets[0]!.opens()
      sockets[0]!.carries(anEvent('cursor-1'))
      sockets[0]!.closes(4002)

      expect(stream.condition.kind).toBe('restarted')

      await vi.advanceTimersByTimeAsync(20)
      expect(sockets[1]?.url).toBe('wss://convia.test/v1/me/events')
    } finally {
      vi.useRealTimers()
    }
  })

  /*
  4000 means Convia dropped events this stream could not keep up with. The
  cursor is still good, so the reconnection resumes -- but the consumer is told,
  because what Convia dropped it cannot replay.
  */
  it('resumes after falling behind, and says that it fell behind', async () => {
    vi.useFakeTimers()
    try {
      const seen: Condition[] = []
      const { stream, sockets } = streaming({ backoff: 10 })
      stream.watch((condition) => seen.push(condition))

      sockets[0]!.opens()
      sockets[0]!.carries(anEvent('cursor-1'))
      sockets[0]!.closes(4000)

      expect(seen.some((one) => one.kind === 'behind')).toBe(true)

      await vi.advanceTimersByTimeAsync(20)
      expect(sockets[1]?.url).toBe('wss://convia.test/v1/me/events?after=cursor-1')
    } finally {
      vi.useRealTimers()
    }
  })

  /*
  A reconnect after 4001 would be refused before the upgrade, and a refused
  handshake reaches a script without its status -- so each attempt would teach
  the client nothing and it would keep making them.
  */
  it('never reconnects after the session ended', async () => {
    vi.useFakeTimers()
    try {
      const { stream, sockets } = streaming({ backoff: 10 })
      sockets[0]!.opens()
      sockets[0]!.closes(4001)

      expect(stream.condition.kind).toBe('ended')

      await vi.advanceTimersByTimeAsync(5_000)
      expect(sockets).toHaveLength(1)
    } finally {
      vi.useRealTimers()
    }
  })

  it('waits longer each time, up to the ceiling', async () => {
    vi.useFakeTimers()
    try {
      const { sockets } = streaming({ backoff: 100, longestBackoff: 400 })

      for (const wait of [100, 200, 400, 400]) {
        sockets[sockets.length - 1]!.closes()
        await vi.advanceTimersByTimeAsync(wait - 1)
        const before = sockets.length
        await vi.advanceTimersByTimeAsync(1)
        expect(sockets.length, `after ${wait}ms`).toBe(before + 1)
      }
    } finally {
      vi.useRealTimers()
    }
  })

  /*
  Without this, one bad hour leaves every later reconnection half a minute slow,
  for as long as the page stays open.
  */
  it('waits from the bottom again once it has connected', async () => {
    vi.useFakeTimers()
    try {
      const { sockets } = streaming({ backoff: 100, longestBackoff: 400 })

      sockets[0]!.closes()
      await vi.advanceTimersByTimeAsync(100)
      sockets[1]!.closes()
      await vi.advanceTimersByTimeAsync(200)

      // The third connection succeeds, which should reset the wait.
      sockets[2]!.opens()
      sockets[2]!.closes()

      await vi.advanceTimersByTimeAsync(100)
      expect(sockets).toHaveLength(4)
    } finally {
      vi.useRealTimers()
    }
  })
})

describe('stopping', () => {
  it('closes the socket and reconnects to nothing', async () => {
    vi.useFakeTimers()
    try {
      const { stream, sockets } = streaming({ backoff: 10 })
      sockets[0]!.opens()
      stream.close()

      expect(sockets[0]!.closedByCaller).toBe(true)
      expect(stream.condition.kind).toBe('closed')

      sockets[0]!.closes()
      await vi.advanceTimersByTimeAsync(5_000)
      expect(sockets).toHaveLength(1)
    } finally {
      vi.useRealTimers()
    }
  })

  it('closing twice is closing once', () => {
    const { stream } = streaming()
    const seen: Condition[] = []
    stream.watch((condition) => seen.push(condition))

    stream.close()
    stream.close()

    expect(seen.filter((one) => one.kind === 'closed')).toHaveLength(1)
  })

  it('does not reconnect while it is waiting to, once closed', async () => {
    vi.useFakeTimers()
    try {
      const { stream, sockets } = streaming({ backoff: 50 })
      sockets[0]!.closes()
      stream.close()

      await vi.advanceTimersByTimeAsync(5_000)
      expect(sockets).toHaveLength(1)
    } finally {
      vi.useRealTimers()
    }
  })
})

describe('telling the consumer what is happening', () => {
  it('reports the condition at once, so a watcher does not wait for a change', () => {
    const { stream } = streaming()
    const seen: Condition[] = []
    stream.watch((condition) => seen.push(condition))
    expect(seen).toHaveLength(1)
    expect(seen[0]!.kind).toBe('connecting')
  })

  it('stops reporting to a watcher that stopped watching', () => {
    const { stream, sockets } = streaming()
    const seen: Condition[] = []
    const stop = stream.watch((condition) => seen.push(condition))
    stop()

    sockets[0]!.opens()
    expect(seen).toHaveLength(1)
  })

  /*
  The first call happens inside `watch`, on the consumer's own stack, so a
  watcher that throws there throws at them -- a bug at the one moment it is
  cheapest to see. It is also not registered, so the same bug is not then
  swallowed on every change afterwards.
  */
  it('throws a first-call failure back at the caller, and does not keep the watcher', () => {
    const { stream, sockets } = streaming()
    const seen: string[] = []

    expect(() =>
      stream.watch(() => {
        throw new Error('a bug in the consumer')
      }),
    ).toThrow('a bug in the consumer')

    stream.watch((condition) => seen.push(condition.kind))
    sockets[0]!.opens()

    // Two reports reached the surviving watcher, and none reached the one that
    // threw -- which would have thrown again here had it been kept.
    expect(seen).toEqual(['connecting', 'live'])
  })

  /*
  Once it is registered, nobody is there to catch: a consumer throwing while
  being told the stream went down must not stop the others being told.
  */
  it('tells the others when a registered watcher throws', () => {
    const { stream, sockets } = streaming()
    const seen: string[] = []
    let first = true

    stream.watch(() => {
      if (first) {
        first = false
        return
      }
      throw new Error('a bug in the consumer')
    })
    stream.watch((condition) => seen.push(condition.kind))

    expect(() => sockets[0]!.opens()).not.toThrow()
    expect(seen).toContain('live')
  })
})
