/*
One connection to a person's stream, held open for as long as it is wanted.

**Being told about something is an improvement on asking, never a replacement
for being able to ask.** Everything this delivers can also be read over REST,
and a consumer that cannot work without the stream has built something more
fragile than the stream it depends on. What this reports, through `watch`, is
enough to know when asking is necessary again.

The reconnection rules are Convia's, from `docs/events.md`, and the close codes
are the only moment a refusal can still say why: a browser hides the status of a
failed WebSocket upgrade from scripts, so a session that ended looks exactly
like an unreachable server unless the stream was already open when it happened.
*/

import type { components } from './contract.js'

export type ConviaEvent = components['schemas']['Event']

/* Close codes Convia sends. `docs/events.md` publishes them. */
const fellBehind = 4000
const sessionEnded = 4001
const cursorTooOld = 4002

/*
The wait before reconnecting starts at a second and doubles to half a minute.

Starting low is for the ordinary case, an instance restarting during a
deployment. The ceiling is for the other one: every client reconnecting at once
into a Convia that is still down is a load it does not need while recovering.
*/
const firstRetry = 1_000
const lastRetry = 30_000

/*
Condition is what the stream is doing, and why it stopped when it did.

It is a union rather than a boolean because the reasons are not interchangeable:
two of them mean the consumer has a gap and should re-read, one means it should
stop and sign in again, and the rest mean waiting is enough.
*/
export type Condition =
  /* Opening, or waiting to open again. Events are not arriving. */
  | { kind: 'connecting'; attempt: number }

  /* Open. What arrives is everything, in order. */
  | { kind: 'live' }

  /*
  Convia dropped events because this stream could not keep up with them.

  The reconnection resumes from the last cursor, so what Convia still holds is
  replayed -- but it dropped what it dropped, and a consumer that must not have
  a gap re-reads over REST rather than trusting the replay to be complete.
  */
  | { kind: 'behind' }

  /*
  The cursor was older than the events Convia keeps, so nothing can be replayed.

  **This one the consumer has to act on.** The stream reconnects from scratch
  and will carry what happens next, and everything between the last event seen
  and now exists only over REST.
  */
  | { kind: 'restarted' }

  /*
  The session that opened the stream no longer authenticates anybody.

  Nothing reconnects after this. A reconnect would be refused before the
  upgrade, and a client that kept trying would be hammering a door that is going
  to stay shut until somebody signs in again -- which this cannot do for them.
  */
  | { kind: 'ended' }

  /* `close` was called. Nothing reconnects. */
  | { kind: 'closed' }

export type Listener = (event: ConviaEvent) => void
export type Watcher = (condition: Condition) => void

export interface StreamOptions {
  /*
  Where the installation is, when it is not the origin serving this page.

  Given as an http(s) origin; the scheme is translated. It carries no session,
  for the reason the transport's does not, so it is here for tests and for
  anything supplying its own WebSocket.
  */
  origin?: string

  /* The WebSocket implementation. Supplied by tests; otherwise the global one. */
  WebSocket?: typeof globalThis.WebSocket

  /* The first wait before reconnecting, in milliseconds. */
  backoff?: number

  /* The longest wait between reconnections, in milliseconds. */
  longestBackoff?: number
}

/*
addressOf is the person's stream, resuming after a cursor when there is one.

`https` becomes `wss` and everything else becomes `ws`, which is the rule rather
than a guess: a stream opened unencrypted from an encrypted page would be
refused by the browser anyway, and refused later than here.
*/
export function addressOf(origin: string, after?: string): string {
  const scheme = origin.startsWith('https:') ? 'wss:' : 'ws:'
  const host = origin.replace(/^https?:/, '')
  const resume = after === undefined ? '' : `?after=${encodeURIComponent(after)}`
  return `${scheme}${host}/v1/me/events${resume}`
}

/*
EventStream is one connection, and the listeners it feeds.

One per consumer rather than one per thing that cares: each connection is a
place against a ceiling Convia keeps per person, and everything that wants
events wants the same ones.
*/
export class EventStream {
  readonly #origin: string
  readonly #WebSocket: typeof globalThis.WebSocket
  readonly #firstRetry: number
  readonly #lastRetry: number

  readonly #listeners = new Set<Listener>()
  readonly #watchers = new Set<Watcher>()

  #socket: WebSocket | null = null
  #waiting: ReturnType<typeof setTimeout> | undefined
  #delay: number
  #cursor: string | undefined
  #attempt = 0
  #stopped = false
  #condition: Condition = { kind: 'connecting', attempt: 0 }

  constructor(options: StreamOptions = {}) {
    this.#origin = options.origin ?? (globalThis.location?.origin ?? '')
    this.#WebSocket = options.WebSocket ?? globalThis.WebSocket
    this.#firstRetry = options.backoff ?? firstRetry
    this.#lastRetry = options.longestBackoff ?? lastRetry
    this.#delay = this.#firstRetry
    this.#open()
  }

  /* What the stream is doing now. */
  get condition(): Condition {
    return this.#condition
  }

  /*
  listen delivers every event to one listener until the returned function is
  called.

  Unsubscribing is safe at any time, including from inside a listener and more
  than once: a set holds one entry per function and removing what is not there
  does nothing, so no flag is needed to make the second call harmless.
  */
  listen(listener: Listener): () => void {
    this.#listeners.add(listener)
    return () => void this.#listeners.delete(listener)
  }

  /*
  watch reports every change of condition, and the current one at once.

  **The first call is the caller's own**, so a watcher that throws on it throws
  out of `watch` rather than being swallowed: that is a bug in the consumer at
  the one moment it is cheapest to see, in their own stack. It is also not
  registered, because registering it would mean swallowing the same bug on every
  change after this one. Later calls are a different matter -- by then nobody is
  there to catch, and one consumer must not stop the others being told.
  */
  watch(watcher: Watcher): () => void {
    watcher(this.#condition)
    this.#watchers.add(watcher)

    return () => void this.#watchers.delete(watcher)
  }

  /*
  close stops the stream and everything it was going to do next.

  Calling it twice is calling it once. After it, nothing reconnects: a consumer
  that wants the stream again makes another.
  */
  close(): void {
    if (this.#stopped) {
      return
    }
    this.#stopped = true
    clearTimeout(this.#waiting)
    this.#waiting = undefined

    const socket = this.#socket
    this.#socket = null
    socket?.close()

    this.#report({ kind: 'closed' })
  }

  #report(condition: Condition): void {
    this.#condition = condition

    /*
    Watched from a copy, for the reason listeners are: a watcher that stops
    watching from inside its own call would otherwise change the set being
    walked.
    */
    for (const watcher of [...this.#watchers]) {
      try {
        watcher(condition)
      } catch {
        // A consumer's own failure is theirs. It must not stop the others
        // hearing about a stream that just went down.
      }
    }
  }

  #deliver(event: ConviaEvent): void {
    /*
    Delivered from a copy of the set.

    A listener that unsubscribes while being called is the ordinary case -- a
    screen closing on the event that closed it -- and walking a set somebody is
    removing from is how one of them silently stops being called. The copy also
    means a listener added during delivery hears the next event, not this one,
    which is the answer that does not depend on iteration order.
    */
    for (const listener of [...this.#listeners]) {
      try {
        listener(event)
      } catch {
        // One consumer throwing must not stop the rest from being told, and
        // must not take the connection down with it.
      }
    }
  }

  /*
  There is no `stopped` check here on purpose.
  *
  This runs from the constructor and from the timer `close` clears, so a
  stopped stream never reaches it. A guard would read like one that matters
  and would be the one nobody notices going wrong, because nothing can reach
  it to notice. The check that does the work is in `onclose`.
  */
  #open(): void {
    this.#attempt += 1
    this.#report({ kind: 'connecting', attempt: this.#attempt })

    const opening = new this.#WebSocket(addressOf(this.#origin, this.#cursor))
    this.#socket = opening

    opening.onopen = () => {
      // The wait starts again from the bottom, so one bad hour does not leave
      // every later reconnection half a minute slow.
      this.#delay = this.#firstRetry
      this.#attempt = 0
      this.#report({ kind: 'live' })
    }

    opening.onmessage = (message: MessageEvent) => {
      let event: ConviaEvent
      try {
        event = JSON.parse(String(message.data)) as ConviaEvent
      } catch {
        // Something that is not an event cannot be delivered as one, and
        // dropping the connection over it would lose the ones that are.
        return
      }

      if (event.cursor !== undefined) {
        this.#cursor = event.cursor
      }

      this.#deliver(event)
    }

    opening.onclose = (closed: CloseEvent) => {
      this.#socket = null
      if (this.#stopped) {
        return
      }

      if (closed.code === sessionEnded) {
        /*
        Nothing reconnects. The upgrade would be refused and the refusal would
        arrive without a status, so a client that kept trying would learn
        nothing from each attempt and would keep making them.
        */
        this.#stopped = true
        this.#report({ kind: 'ended' })
        return
      }

      if (closed.code === cursorTooOld) {
        // Resuming is what was refused, so the next connection does not.
        this.#cursor = undefined
        this.#report({ kind: 'restarted' })
      } else if (closed.code === fellBehind) {
        this.#report({ kind: 'behind' })
      }

      this.#waiting = setTimeout(() => this.#open(), this.#delay)
      this.#delay = Math.min(this.#delay * 2, this.#lastRetry)
    }
  }
}
