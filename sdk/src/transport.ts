/*
One request against a Convia installation, as a signed-in person.

**There is no token to attach.** A session travels in a cookie the page cannot
read, so authentication here is `credentials: 'same-origin'` and nothing else.
That is also why there is no `baseUrl` pointing at another installation: the
cookie is `__Host-` prefixed and host-only, so a page on a different origin
cannot send it, and an option that let somebody try would be an option that
cannot work.

What this adds over calling `fetch` directly is the three things every caller
would otherwise write again: a deadline, cancellation that composes with the
caller's own, and a refusal turned into something to branch on.
*/

import { ConviaError, Unreachable, Unreadable } from './errors.js'
import { checkIdempotencyKey } from './idempotency.js'
import type { FailureBody } from './vocabulary.js'

/*
changes reports a method that does something, as opposed to reading.

Only these carry an idempotency key, because only these have anything to
perform at most once.
*/
function changes(method: string): boolean {
  return method === 'POST' || method === 'PATCH' || method === 'PUT' || method === 'DELETE'
}

/* The version prefix every route carries. */
const prefix = '/v1'

/*
howLongToWait is the default deadline, in milliseconds.

A request with no deadline is a request that can hang until the tab closes, and
the usual way an interface becomes unresponsive is one of those behind a
spinner nothing clears. Thirty seconds is longer than any session-surface route
should take and short enough that somebody notices rather than waits.
*/
const howLongToWait = 30_000

export interface ClientOptions {
  /*
  Where the installation is, when it is not the origin serving this page.

  **It cannot carry a session**, for the reason above, so it is here for one
  thing: tests, and anything else that supplies its own `fetch`.
  */
  origin?: string

  /* The fetch to use. Supplied by tests; otherwise the global one. */
  fetch?: typeof globalThis.fetch

  /* How long to wait before giving up, in milliseconds. 0 waits forever. */
  timeout?: number
}

export interface RequestOptions {
  /* A body to send as JSON. Absent means no body, which is not the same as null. */
  body?: unknown

  /* Query parameters. Anything undefined or empty is left out rather than sent empty. */
  query?: Record<string, string | number | boolean | undefined>

  /* The caller's own cancellation, composed with this client's deadline. */
  signal?: AbortSignal

  /* Extra headers. The ones this sets itself cannot be overridden. */
  headers?: Record<string, string>

  /* A deadline for this request alone, in milliseconds. 0 waits forever. */
  timeout?: number

  /*
  A key that makes a repeat of this request safe.
  *
  Sent only on a method that changes something: a GET carrying one would
  ask Convia to remember an answer nothing is going to repeat.
  */
  idempotencyKey?: string
}

/*
Timeout reports a request this client gave up on.

It is **not** the same as the caller aborting, and keeping them apart is the
point: a caller's abort is something they did and needs no report, while a
deadline passing is a fact about the installation or the network that somebody
may want to show, retry, or count.
*/
export class Timeout extends Error {
  readonly after: number

  constructor(after: number) {
    super(`Convia did not answer within ${after}ms.`)
    this.name = 'Timeout'
    this.after = after
  }
}

export function isTimeout(value: unknown): value is Timeout {
  return value instanceof Timeout
}

function search(query: RequestOptions['query']): string {
  if (query === undefined) {
    return ''
  }

  const parameters = new URLSearchParams()
  for (const [name, value] of Object.entries(query)) {
    if (value !== undefined && value !== '') {
      parameters.set(name, String(value))
    }
  }

  const rendered = parameters.toString()
  return rendered === '' ? '' : `?${rendered}`
}

/*
retryAfterOf reads the header, in seconds.

An absent or unreadable header stays undefined rather than becoming zero. A
caller that reads "not stated" as "now" is how a rate limit becomes an outage,
so there is no default here to be wrong about.
*/
function retryAfterOf(response: Response): number | undefined {
  const stated = response.headers.get('retry-after')
  if (stated === null) {
    return undefined
  }

  const seconds = Number(stated)
  if (Number.isFinite(seconds) && seconds >= 0) {
    return seconds
  }

  // The header may also be a date. Past dates mean now, which is zero rather
  // than a negative wait somebody would subtract.
  const at = Date.parse(stated)
  return Number.isNaN(at) ? undefined : Math.max(0, (at - Date.now()) / 1000)
}

/*
Convia is an installation, talked to as whoever is signed in to this page.

One instance is enough for a page; it holds no state beyond its options, so
nothing is lost by making another.
*/
export class Convia {
  readonly #origin: string
  readonly #fetch: typeof globalThis.fetch
  readonly #timeout: number

  constructor(options: ClientOptions = {}) {
    this.#origin = options.origin ?? ''
    /*
    Bound, because a browser's `fetch` refuses to be called detached.
    *
    Holding the function and calling it as `this.#fetch(...)` loses the
    `window` it belongs to, and the browser answers `Illegal invocation` --
    which arrives here as a failed request and is reported as a Convia that
    could not be reached. **Node does not care**, so every unit test passes:
    they all supply a fetch of their own and never touch the global one. It
    was a browser that found this.
    */
    this.#fetch = options.fetch ?? globalThis.fetch.bind(globalThis)
    this.#timeout = options.timeout ?? howLongToWait
  }

  async request<T>(method: string, path: string, options: RequestOptions = {}): Promise<T> {
    const headers: Record<string, string> = {
      ...options.headers,
      Accept: 'application/json',
    }

    let body: string | undefined
    if (options.body !== undefined) {
      body = JSON.stringify(options.body)
      headers['Content-Type'] = 'application/json'
    }

    if (options.idempotencyKey !== undefined && changes(method)) {
      checkIdempotencyKey(options.idempotencyKey)
      headers['Idempotency-Key'] = options.idempotencyKey
    }

    const waiting = options.timeout ?? this.#timeout
    const deadline = waiting > 0 ? AbortSignal.timeout(waiting) : undefined

    /*
    The caller's cancellation and this client's deadline are both reasons to
    stop, and either may fire. Composing them is what keeps a caller from
    having to choose between its own abort and a deadline.
    */
    const reasons = [options.signal, deadline].filter((one) => one !== undefined)
    const signal = reasons.length > 0 ? AbortSignal.any(reasons) : undefined

    let response: Response
    try {
      response = await this.#fetch(this.#origin + prefix + path + search(options.query), {
        method,
        headers,
        credentials: 'same-origin',
        ...(body === undefined ? {} : { body }),
        ...(signal === undefined ? {} : { signal }),
      })
    } catch (cause) {
      /*
      An abort is not an unreachable Convia, and the two must not be confused:
      reporting a cancelled request as a network failure would show somebody an
      error for something they did on purpose.

      Which abort it was is read from the deadline rather than from the thrown
      value, because `AbortSignal.any` forwards the first reason and a caller's
      own abort reason is theirs to choose -- it may be anything at all.
      */
      if (isAbort(cause)) {
        if (deadline?.aborted === true && options.signal?.aborted !== true) {
          throw new Timeout(waiting)
        }
        throw cause
      }
      throw new Unreachable(cause)
    }

    /*
    An empty answer is nothing, and this is a shortcut rather than the rule:
    without it, `.json()` on an empty body throws, the `catch` below turns that
    into undefined, and a 204 is `ok`, so the answer is the same either way.

    It is kept because the equivalence is accidental -- it depends on a throw
    being caught, which is a cost on every empty answer and a surprise to anyone
    reading the parse as the place errors are handled.
    */
    if (response.status === 204) {
      return undefined as T
    }

    const text = await response.text()
    let payload: unknown
    let readable = true

    if (text !== '') {
      try {
        payload = JSON.parse(text)
      } catch {
        readable = false
      }
    }

    /*
    A success whose body cannot be read is not a success the caller can use.
    *
    Returning undefined for it -- which is what parsing into a swallowed
    catch does -- hands back a value typed as the thing that was asked for,
    and the caller finds out when it reads a field: a TypeError with nothing
    in it about a truncated answer or a proxy that replaced the body.
    */
    if (response.ok && !readable) {
      throw new Unreadable(response.status, text)
    }

    if (!response.ok) {
      /*
      A refusal Convia did not shape -- a proxy's HTML error page, say -- is
      still a refusal. Failing to parse it would lose the status, which is the
      part that still means something.
      */
      const failure = (payload as FailureBody | undefined)?.error
      const refusal = new ConviaError(
        response.status,
        failure ?? { code: 'unknown', message: 'Convia refused the request.' },
      )
      refusal.retryAfter = retryAfterOf(response)
      throw refusal
    }

    return payload as T
  }

  get<T>(path: string, options: RequestOptions = {}): Promise<T> {
    return this.request<T>('GET', path, options)
  }

  post<T>(path: string, options: RequestOptions = {}): Promise<T> {
    return this.request<T>('POST', path, options)
  }

  patch<T>(path: string, options: RequestOptions = {}): Promise<T> {
    return this.request<T>('PATCH', path, options)
  }

  delete<T>(path: string, options: RequestOptions = {}): Promise<T> {
    return this.request<T>('DELETE', path, options)
  }
}

/*
isAbort recognises a cancellation however it was raised.

`fetch` throws a DOMException named AbortError in a browser and in Node, but a
caller may abort with a reason of their own, and `AbortSignal.any` forwards it
unchanged -- so the name is checked and the type is not insisted on.
*/
function isAbort(value: unknown): boolean {
  return (
    (value instanceof DOMException && value.name === 'AbortError') ||
    (value instanceof Error && value.name === 'AbortError') ||
    (value instanceof Error && value.name === 'TimeoutError')
  )
}
