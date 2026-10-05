/*
Making a retry safe, and making it worth making.

**The two halves are useless apart.** A key with no retry protects against
nothing: `fetch` does not retry, so the only repeat is one the caller makes, and
a key generated per call is a different key each time. A retry with no key is
the duplicate it was meant to avoid -- a client that receives no answer cannot
tell whether its request was lost on the way out or on the way back, and the
second attempt may be the second room, the second message, the second charge.

So a retry here carries one key across every attempt, and that is the whole
design.
*/

import { ConviaError, Unreachable } from './errors.js'
import { Timeout } from './transport.js'

/* What Convia accepts. The key is opaque to it, and bounded. */
const longest = 255

/*
newIdempotencyKey mints one.

A UUID, because the only property that matters is that two callers never collide
and the same caller never reuses one by accident. Convia does not read it.
*/
export function newIdempotencyKey(): string {
  return globalThis.crypto.randomUUID()
}

/*
checkIdempotencyKey refuses a key Convia would, before the request is made.

A key too long is answered with a refusal that says nothing useful about which
of several calls was wrong, and an empty one is almost always a variable that
was not set -- which would silently drop the protection the caller asked for.
*/
export function checkIdempotencyKey(key: string): void {
  if (key.length === 0) {
    throw new TypeError('An idempotency key cannot be empty.')
  }
  if (key.length > longest) {
    throw new TypeError(`An idempotency key is at most ${longest} characters; this one is ${key.length}.`)
  }
}

export interface RetryPolicy {
  /*
  How many times to send the request in total, including the first.

  Small on purpose. A client that keeps trying is a client that turns one
  installation's bad minute into every client's bad minute, and the thing worth
  surviving is a moment, not an outage.
  */
  attempts?: number

  /* The shortest wait between attempts, in milliseconds. */
  backoff?: number

  /* The longest wait between attempts, in milliseconds. */
  longestBackoff?: number
}

const howManyTimes = 3
const howLongAtFirst = 250
const howLongAtMost = 10_000

/*
worthRetrying reports whether sending the same request again could end
differently.

**A refusal is retried only when Convia said so with a status.** A request that
got no answer is retried too, and that is safe only because a key is always
carrying it: the request may have arrived and done its work, and without a key
trying again is how one room becomes two. Nothing here takes a key as a
condition, because `withRetries` mints one when it is not given -- there is no
unkeyed path for this to guard.

`409 conflict` is deliberately **not** retried, and that is a compromise rather
than a decision. Convia answers two different things with it: a key reused for a
different request, which will never succeed, and a request with this key still
running, which succeeds as soon as the first finishes. They carry the same code
and differ only in the message -- and a client that branches on message text is
a client that breaks when somebody rewords it. So the permanent reading is
taken, because retrying the permanent one forever is worse than not collecting
the other one's result.
*/
function worthRetrying(thrown: unknown): boolean {
  if (thrown instanceof ConviaError) {
    return thrown.retryable
  }

  if (thrown instanceof Unreachable || thrown instanceof Timeout) {
    return true
  }

  return false
}

/*
howLongToPause is the wait before the next attempt, in milliseconds.

Convia's own answer wins when it gave one: `Retry-After` is the installation
saying what it can take, and a client that knows better is a client that does
not. Otherwise the wait doubles each time, with jitter -- without it, every
client refused in the same second comes back in the same second.
*/
function howLongToPause(attempt: number, thrown: unknown, policy: Required<RetryPolicy>): number {
  if (thrown instanceof ConviaError && thrown.retryAfter !== undefined) {
    return Math.min(thrown.retryAfter * 1000, policy.longestBackoff)
  }

  const doubled = Math.min(policy.backoff * 2 ** (attempt - 1), policy.longestBackoff)
  return doubled / 2 + Math.random() * (doubled / 2)
}

const pause = (ms: number) => new Promise((wake) => setTimeout(wake, ms))

export interface Attempt<T> {
  (key: string, attempt: number): Promise<T>
}

/*
withRetries performs one operation until it succeeds, is refused for good, or
runs out of attempts.

The key is minted once and handed to every attempt, which is what makes the
repeat safe: Convia performs the operation at most once and replays the original
answer -- the original status included -- to anything carrying the same key.

It is **opt-in**, and that is deliberate. A transport that retried everything by
itself would hide the load it was making, and the first anybody would know of it
is an installation that cannot recover because its clients will not let it.
*/
export async function withRetries<T>(
  work: Attempt<T>,
  key: string = newIdempotencyKey(),
  policy: RetryPolicy = {},
): Promise<T> {
  checkIdempotencyKey(key)

  const settled: Required<RetryPolicy> = {
    attempts: policy.attempts ?? howManyTimes,
    backoff: policy.backoff ?? howLongAtFirst,
    longestBackoff: policy.longestBackoff ?? howLongAtMost,
  }

  if (settled.attempts < 1) {
    throw new TypeError('An operation has to be attempted at least once.')
  }

  let last: unknown
  for (let attempt = 1; attempt <= settled.attempts; attempt += 1) {
    try {
      return await work(key, attempt)
    } catch (thrown) {
      last = thrown

      /*
      The last attempt does not wait before giving up, and a refusal that will
      not change is not waited on at all: sleeping before re-throwing is time
      somebody spends learning nothing.
      */
      if (attempt === settled.attempts || !worthRetrying(thrown)) {
        throw thrown
      }

      await pause(howLongToPause(attempt, thrown, settled))
    }
  }

  // Unreachable: the loop either returns or throws. Kept so the type is honest.
  throw last
}
