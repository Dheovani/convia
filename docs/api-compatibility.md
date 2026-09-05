# API Specification and Compatibility Policy

Convia's public API is a product interface. External applications, its own web client, and future SDKs all depend on it, so it is governed rather than changed opportunistically.

This document records the decisions of milestone M03 in [`TODO.md`](../TODO.md). The transport rules every endpoint follows are in [`api-conventions.md`](api-conventions.md); this document governs how the contract itself may change.

## The Contract

The contract is [`api/openapi.yaml`](../api/openapi.yaml), written in **OpenAPI 3.0.3**.

OpenAPI was chosen over alternatives because it is the format Convia's expected consumers already use, and because it supports client generation for the languages named in M19 and M20. Version 3.0.3 was chosen over 3.1 for the breadth of generator and tooling support available today. Moving to 3.1 is worthwhile once the generators selected in M19 and M20 support it fully; that change is invisible to clients and is not itself a breaking API change.

The specification is authoritative. When the implementation and the specification disagree, that is a bug in one of them, never an accepted difference.

### Validation

Contract validation runs inside `go test`, in `internal/server/contract_test.go`, so it runs locally and in CI with no extra toolchain. It enforces that:

- the document is a valid OpenAPI 3.0.3 specification, including every example it contains;
- every implemented route is documented, and every documented operation is implemented;
- the documented `ErrorCode` enum is exactly the set of codes the service can return;
- real responses from the running handler validate against the documented schemas.

A breaking-change detector against a released baseline is deferred until the first release exists, because there is nothing to compare against yet. It is tracked as M03-010.

## Naming

| Element | Convention | Example |
| --- | --- | --- |
| Resource path | Plural noun, lowercase, hyphenated when multi-word | `/v1/rooms`, `/v1/call-history` |
| Path parameter | Snake case, suffixed with the resource | `/v1/rooms/{room_id}` |
| JSON field | Snake case | `display_name` |
| Timestamp field | Suffixed `_at` | `created_at`, `ended_at` |
| Duration field | Suffixed with its unit | `timeout_seconds` |
| Identifier field | Suffixed `_id` | `application_id` |
| Boolean field | Reads as a predicate | `is_muted`, `has_recording` |
| Enum value | Lowercase snake case string, never an integer | `waiting`, `in_progress` |
| Operation ID | Camel case, verb then subject | `createRoom`, `listCallParticipants` |
| Error code | Lowercase snake case | `invalid_request` |

Actions that do not map onto a resource lifecycle are sub-resources invoked with `POST`, such as `POST /v1/calls/{call_id}/end`. Verbs never appear in a resource path itself.

Names describe Convia concepts. A name that only makes sense to someone who knows the media provider, the database schema, or the internal package layout is wrong.

## Additive and Breaking Changes

An **additive** change may ship at any time, in any release:

- adding an endpoint, an optional request field, or a response field;
- adding a new error code, or a new value to an enum that clients only read;
- relaxing a validation rule, such as raising a maximum length;
- adding an optional query parameter whose default preserves current behavior;
- documentation, example, and description changes.

A **breaking** change requires a new API version or a completed deprecation cycle:

- removing or renaming an endpoint, field, parameter, or error code;
- changing a field's type, format, or meaning;
- making an optional request field required, or tightening validation;
- changing the HTTP status or the error code returned for an existing condition;
- adding a value to an enum that clients must write or exhaustively handle;
- changing the default value of a parameter;
- removing a response field, including one that is only sometimes present.

Clients are expected to ignore unknown response fields and to treat an unknown error code as a generic failure of its HTTP status class. A client that breaks because Convia added a field is not a Convia compatibility failure, and this expectation is documented for SDK authors.

Note that Convia rejects unknown fields in *request* bodies. The asymmetry is deliberate: a misspelled request field is a client bug worth reporting immediately, while tolerance for unknown response fields is what makes additive evolution possible.

## Lifecycle States

Every operation declares a state through the `x-lifecycle` extension:

| State | Meaning | Compatibility guarantee |
| --- | --- | --- |
| `experimental` | Published for feedback | May change or disappear without notice |
| `preview` | Feature complete, still validating | Breaking change allowed after a 30-day notice |
| `stable` | Supported product surface | Breaking change only through a new API version or a full deprecation cycle |
| `deprecated` | Superseded, still working | Removed no earlier than the announced sunset |
| `removed` | Gone | Returns `404`, documented in the changelog |

An operation may not become `stable` until it has contract tests, error responses, and authorization tests.

## Deprecation

A deprecated operation:

- is marked `deprecated: true` in the specification, with `x-lifecycle: deprecated`;
- returns the `Deprecation` header carrying the date the deprecation took effect;
- returns the `Sunset` header carrying the earliest date it may stop working;
- returns a `Link` header with `rel="deprecation"` pointing at the migration notes;
- continues to behave exactly as before until it is removed.

Minimum notice before removal, counted from the announcement:

| Lifecycle at announcement | Minimum notice |
| --- | --- |
| `experimental` | None |
| `preview` | 30 days |
| `stable` | 180 days |

Deprecations are announced in the changelog and in the specification in the same release. Silent removal is never acceptable for a `stable` operation.

## Idempotency

`GET`, `PUT`, and `DELETE` are idempotent by their HTTP semantics. Convia implements them that way: repeating a `DELETE` on an already-deleted resource succeeds rather than failing.

`POST` operations that create a resource or start a state transition accept an `Idempotency-Key` request header:

- the key is a client-generated opaque string of at most 255 characters;
- a repeated key with the same request returns the original response, including its original status;
- a repeated key with a different request body is rejected as a conflict;
- keys are retained for at least 24 hours, after which a repeat is treated as a new request;
- keys are scoped to one application, so two applications cannot collide.

Endpoints requiring an idempotency key state it explicitly. This behavior is defined now so that call and room mutations in M08 and M09 implement it uniformly. The error codes it needs will be added to the contract in the milestone that implements it.

## Optimistic Concurrency

Resources whose updates can conflict return an `ETag` header. Conditional updates use `If-Match`:

- a request with a matching `If-Match` proceeds;
- a request with a stale `If-Match` is rejected without applying any change;
- a request without `If-Match` on an endpoint that requires it is rejected;
- a rejected update never partially applies.

Endpoints declare whether `If-Match` is required or optional. Required is the default for any resource where a lost update would be visible to another participant. No mutable resource exists yet; the first one implements this and adds the corresponding error codes.

## Pagination Cursors

Cursors are **opaque**. Their encoding is an internal implementation detail and may change at any time without a version bump, because no client is permitted to decode one.

- A cursor is valid only for the query that produced it. Changing filters or sort order invalidates it.
- A cursor remains usable for at least one hour after it is issued.
- An expired or malformed cursor is rejected rather than silently treated as the first page.
- Cursor pagination is stable for items that existed when paging started; items created during a traversal may or may not appear.
- Clients must not persist cursors as bookmarks or derive item counts from them.

The `limit` and `cursor` parameters are defined once in the specification and reused by every list endpoint. Bounds are in [`api-conventions.md`](api-conventions.md).

## Error Code Ownership

Error codes are public API surface, governed like any other part of the contract:

- a code is added by adding it to the `ErrorCode` enum and to `internal/api`, in the same change; the contract test fails otherwise;
- adding a code is additive, and changing the meaning of an existing code is breaking;
- a code is added only when a client would take a different action from it. Where the HTTP status is enough, the existing code is reused;
- `message` is never part of the contract and may be reworded at any time;
- a code must not reveal internal implementation, database state, or media provider behavior.

## SDK Consumption

SDKs consume this contract rather than reimplementing it:

- request and response models, error codes, and enums are generated from `api/openapi.yaml`, and generated code is never edited by hand;
- the generator and its version are pinned per SDK, and regeneration is part of the release process;
- handwritten code in an SDK covers ergonomics — authentication, retries, pagination iteration, real-time connections — not the wire schema;
- an SDK that needs a type the contract does not describe indicates a gap in the contract, which is fixed in the contract first.

## Provider Neutrality

No public contract may contain a media-provider concept. LiveKit room names, participant identities, grants, tokens, track identifiers, and provider metadata are internal.

Where a client genuinely needs connection data to establish media, it is exposed through Convia-owned field names describing the capability rather than the provider, as defined in M13. A field whose name or documentation would have to change if Convia replaced its media provider does not belong in this contract.
