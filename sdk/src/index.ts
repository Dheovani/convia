/*
Convia's TypeScript SDK.

**What is exported here is what Convia promises.** `contract.ts` is generated
from `api/openapi.yaml` and is not exported as a whole: it describes every
surface, including the operator and tenant operations this package deliberately
does not reach, and exporting it would make each of those part of what somebody
may depend on.

What this milestone publishes first is the vocabulary, because it is the part
every consumer needs and the part nothing currently shares. Convia's own
interface branches on `'message.posted'` and `'wrong_password'` as bare strings
in several files, with nothing checking that either is a name the server uses.
*/

export {
  errorCodes,
  eventTypes,
  isErrorCode,
  isEventType,
  type ErrorCode,
  type EventType,
  type Failure,
} from './vocabulary.js'

export {
  ConviaError,
  Unreachable,
  isConviaError,
  isUnreachable,
  type Refusal,
} from './errors.js'

export {
  Convia,
  Timeout,
  isTimeout,
  type ClientOptions,
  type RequestOptions,
} from './transport.js'

export {
  newIdempotencyKey,
  checkIdempotencyKey,
  withRetries,
  type Attempt,
  type RetryPolicy,
} from './idempotency.js'
