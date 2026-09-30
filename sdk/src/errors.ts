/*
The two kinds of failure, kept apart because they need different words.

A refusal is Convia answering. An unreachable Convia is not an answer, and
telling somebody their password was wrong when the network was down would be a
lie -- so they are different types rather than one type with a flag, and a
`catch` that handles only one of them does not silently handle the other.
*/

import type { ErrorCode, Failure } from './vocabulary.js'

/*
ConviaError carries a refusal Convia explained.

`status` and `code` answer different questions and both are kept: the status
decides whether the session survived, and the code decides what to tell the
person. Branch on the code; the message is prose and may be reworded.
*/
export class ConviaError extends Error {
  readonly status: number

  /*
  The code is typed wider than ErrorCode on purpose.

  An installation may be newer than this package, and a refusal naming a code
  this version has never heard of still has to arrive as a refusal rather than
  as a parse failure. `isErrorCode` is how a caller asks whether it is one of
  the ones it knows how to handle.
  */
  readonly code: ErrorCode | (string & {})

  readonly requestId: string | undefined

  constructor(status: number, failure: Failure) {
    super(failure.message)
    this.name = 'ConviaError'
    this.status = status
    this.code = failure.code
    this.requestId = failure.request_id
  }

  /*
  unauthenticated reports a session that is gone rather than a request that was
  wrong. It is the one refusal that ends the signed-in state, so it is worth a
  name: a caller that checks `status === 401` in five places will eventually
  check it in four.
  */
  get unauthenticated(): boolean {
    return this.status === 401
  }

  /*
  retryable reports a refusal that may succeed later **unchanged**.

  A rate limit and an outage are the same request at a better moment. Everything
  else is the caller's to fix, and retrying it is how a client turns one bad
  request into a thousand.
  */
  get retryable(): boolean {
    return this.status === 429 || this.status === 503
  }

  /*
  retryAfter is how long Convia asked the caller to wait, in seconds.

  It is undefined when Convia did not say, which is not the same as zero: a
  caller that reads an absent header as "now" is the reason rate limits become
  outages.
  */
  retryAfter: number | undefined
}

/*
Unreachable is a request that never got an answer.

It carries no explanation because there is none to carry, and the cause is kept
so that a caller debugging a proxy can see what fetch actually said.
*/
export class Unreachable extends Error {
  constructor(cause: unknown) {
    super('Convia could not be reached.')
    this.name = 'Unreachable'
    this.cause = cause
  }
}

/*
isConviaError and isUnreachable are for `catch`, where the value is `unknown`.

`instanceof` works and is what these do, but a caller writing it has to import
the class to ask the question. These read as what they mean.
*/
export function isConviaError(value: unknown): value is ConviaError {
  return value instanceof ConviaError
}

export function isUnreachable(value: unknown): value is Unreachable {
  return value instanceof Unreachable
}
