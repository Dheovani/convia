/*
The two words Convia and its clients have to agree on.

An error code says why a request was refused; an event type says what happened.
Both are branched on, and both are meaningless if the two sides disagree about
what the strings are.

**They are not written here.** They come out of `api/openapi.yaml`, which Go's
contract tests already compare with the implementation in both directions, so
the list in this file is the list the server actually answers with rather than
a copy somebody keeps in step by remembering to. `npm run generate` rebuilds
`contract.ts`, and CI fails when the rebuild would change it.

What this file adds is names. `components["schemas"]["EventType"]` is the same
type, and nobody should have to write it.
*/

import type { components } from './contract.js'

/*
ErrorCode is the part of a refusal to branch on.

The message beside it is prose and may be reworded; this is the documented,
stable identifier. Nothing should decide anything from the message text.
*/
export type ErrorCode = components['schemas']['ErrorCode']

/*
EventType is what happened, as Convia names it.

The same names are used by the application-facing stream and by a person's own,
which is why there is one list rather than two: an event a person receives is
the same event, with a narrower envelope.
*/
export type EventType = components['schemas']['EventType']

/*
Failure is what a refusal says, and FailureBody is how it arrives.

Convia wraps it: the body is `{"error": {...}}`, not the object itself. Both are
named because both are read at different moments -- the transport parses the
envelope, and everything above it is handed the inside.
*/
export type FailureBody = components['schemas']['Error']
export type Failure = FailureBody['error']

/*
errorCodes and eventTypes are the same vocabularies at runtime.

A union type cannot be checked at run time, and both of these arrive as strings
over the network -- from a server that may be newer than this package. Code
that has to ask "is this one I know?" needs a value, not a type.

They are kept in step with the types above by the assertions below, which stop
the build when the contract gains a name a list does not have. That check is the
only reason writing them out by hand is safe: `satisfies` alone says every entry
is a real one, and says nothing about a real one being missing -- which is the
direction that matters, because the missing name is the one arriving over the
network from a server this package will then fail to understand.
*/
export const errorCodes = [
  'invalid_request',
  'malformed_json',
  'unsupported_media_type',
  'unsupported_version',
  'payload_too_large',
  'unauthenticated',
  'wrong_password',
  'forbidden',
  'not_found',
  'method_not_allowed',
  'precondition_failed',
  'conflict',
  'in_progress',
  'rate_limited',
  'internal_error',
  'unavailable',
] as const satisfies readonly ErrorCode[]

export const eventTypes = [
  'call.started',
  'call.ended',
  'participant.joined',
  'participant.left',
  'participant.removed',
  'participant.role_changed',
  'invitation.declined',
  'message.posted',
  'message.edited',
  'message.deleted',
  'room.member_added',
  'room.member_removed',
  'room.member_role_changed',
  'room.updated',
  'room.closed',
  'room.reopened',
  'room.deleted',
  'presence.changed',
] as const satisfies readonly EventType[]

/*
The lists are complete, checked when this compiles rather than when it runs.

`Exclude` leaves whatever the contract has and the list does not, and a type
constrained to `never` is what stops the build. It is written this way round so
the error names the missing entry -- "Type '\"rate_limited\"' does not satisfy
the constraint 'never'" is a message somebody can act on, where the usual
`true`/`never` spelling only says that something, somewhere, is missing.

**There is nothing to run here.** These are types, not values: a `declare const`
with a statement reading it would be erased by the compiler and then fail at run
time as an identifier that was never emitted.
*/
type Absent<Whole, Listed> = Exclude<Whole, Listed>
type Complete<Missing extends never> = Missing

type EveryErrorCodeIsListed = Complete<Absent<ErrorCode, (typeof errorCodes)[number]>>
type EveryEventTypeIsListed = Complete<Absent<EventType, (typeof eventTypes)[number]>>

export type { EveryErrorCodeIsListed, EveryEventTypeIsListed }

/*
isErrorCode and isEventType report whether a string is one this package knows.

**They answer about this package, not about Convia.** An installation newer than
the package will send names that are not here, and the honest answer to those is
"I do not know this one" -- which is why they exist rather than a cast. A client
that treats an unknown event as a known one acts on something it did not read.
*/
export function isErrorCode(value: string): value is ErrorCode {
  return (errorCodes as readonly string[]).includes(value)
}

export function isEventType(value: string): value is EventType {
  return (eventTypes as readonly string[]).includes(value)
}
