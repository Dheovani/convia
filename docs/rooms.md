# Rooms

A **room** is a place an application's people meet. This document records the decisions of milestone M08 in [`TODO.md`](../TODO.md). The domain lives in [`internal/rooms`](../internal/rooms), its schema in [`internal/database/migrations`](../internal/database/migrations), and its contract in [`api/openapi.yaml`](../api/openapi.yaml).

> **No media provider appears anywhere here.** A room is a Convia concept that the media plane will later be asked to realize, never the other way round. Nothing in this document or in the public contract names LiveKit.

## A Room Is Durable; a Call Is Not

The room is the **place**; the call is the **occasion**. A room outlives every conversation held in it, which is the whole reason it is a resource rather than a parameter of a call.

That split is what M09 depends on. Creating a room is an administrative act an application does once; joining a call is something people do repeatedly. Fusing them would mean re-deciding capacity, naming, and access every time someone dials in.

## The Alias Decides Which Kind of Room You Have

An alias is optional, and its presence is the only difference between the two ways applications use rooms.

| | With an alias | Without one |
| --- | --- | --- |
| Addressed by | The name the application chose | The Convia identifier |
| Typical use | The weekly standup, a support queue, a classroom that lasts a term | One meeting, one incident, one lesson |
| Lifetime | Returned to indefinitely | Created for the occasion, then deleted |

An alias is unique **within one application**. Two applications may both use `weekly-standup` and the resulting rooms have no relationship, exactly as two applications may know the same person as two unrelated users.

**An alias is refused, not resolved.** Creating a room with an alias another room holds answers `409 conflict` rather than returning that room. Creating and looking up are different questions, and answering both from one endpoint would leave a caller unable to tell whether it just created something. Looking a room up is `GET /v1/rooms?alias=…`.

**A deleted room keeps its alias.** The name stays reserved until erasure. Freeing it on deletion would let a client that cached `weekly-standup` find it pointing at a different room, which is a worse failure than being told the name is taken. Clearing the alias with an update *does* free it, because that is an application deliberately giving the name up.

An alias carries no whitespace. A value that rendered identically to another would make two rooms indistinguishable in a listing while remaining distinct keys.

## What Is Mutable

| Field | Mutable | Why |
| --- | --- | --- |
| `alias` | Yes | An application may rename or give up a name it chose |
| `name` | Yes | A display label, free text |
| `metadata` | Yes | Application-owned annotations |
| `max_participants` | Yes | Capacity is an operational decision that changes |
| `status` | **No, not by writing it** | Transitions have their own operations and audit events |
| `id`, `application_id`, `created_at` | No | Convia owns them |

Updates are partial: omitting a field leaves it alone, sending it empty clears it. They accept `If-Match` for optimistic concurrency, as described in [`api-compatibility.md`](api-compatibility.md).

**The lifecycle is deliberately not a writable field.** Closing a room means something an operator acts on, and it emits its own audit event. Reachable by `PATCH {"status": "closed"}`, that meaning would be lost and the event easy to forget.

## Lifecycle

```
        create              close                delete
   ────────────────▶ open ─────────▶ closed ─────────────▶ deleted
                       ▲                │                     │
                       └────────────────┘                     ▼
                            reopen                    erasure (M06 window)
```

`open` and `closed` are used rather than the `active` and `suspended` of applications and users, because the words describe what is happening: a closed room is not being punished, it is finished.

**Closing is reversible and lossless**, and it is what archiving a room means here. A fourth retained-but-invisible state would duplicate `deleted` without adding a distinction anyone could act on.

**Every transition is repeatable.** Closing an already-closed room succeeds, as does deleting an already-deleted one, so a client retrying after a timeout is never punished for it.

**A deleted room refuses further change** with `409 conflict` rather than being silently revived. Deletion is an application's decision and must not be undone by a routine update.

## Closing and Calls in Progress

Closing stops the room accepting **new** calls. A conversation already in progress runs to its end.

This is the decision M09 must implement, and it is recorded here because it is a product judgment rather than an implementation detail: ending a conversation people are having because an administrator tidied a listing would be the wrong default. An application that needs to eject participants will need an operation that says so, and that operation belongs to the call domain, not this one.

## Capacity

`max_participants` is optional and bounded between 1 and 1000. Absent means the room states no limit of its own.

**Convia records it; it does not yet enforce it**, because there are no calls to enforce it against. It is a domain rule now and a media-plane check when the media plane exists. Saying so plainly is better than implying Convia is policing something it cannot observe.

## Isolation

Every room operation is scoped to one application, and how the tenant is decided differs by surface:

- **`/v1/rooms`** — the tenant comes from the presented credential. There is no request field that could name another application, so a tenant-crossing bug at the transport layer is unrepresentable rather than merely unlikely.
- **`/v1/applications/{id}/rooms`** — an operator acts on someone else by definition, so the application is named in the path. What bounds it is the key: an operator credential carrying a tenants scope, which no application key can hold.

Reaching another application's room answers `404`, not `403`. A `403` would confirm the room exists.

Scopes are `rooms:read` and `rooms:write` on the tenant surface, and `tenants:read` / `tenants:write` on the operator surface. See [`authentication.md`](authentication.md).

## Retention and Deletion

A deleted room is **retained, not destroyed**. The row stays for the erasure window defined in [`users.md`](users.md), which keeps the deletion recoverable and the alias reserved.

Erasure — the job that actually removes retained rows and frees the aliases they hold — does not exist yet for any domain. It is one mechanism serving users and rooms alike, so it belongs in the milestone that builds it rather than being half-built twice.

## Listing and Filtering

`GET /v1/rooms` returns rooms newest first, using the cursor pagination defined in [`api-conventions.md`](api-conventions.md).

- **Deleted rooms are excluded** unless `status=deleted` asks for them, so a routine listing shows what an application can still use.
- **`status`** filters by lifecycle state and is indexed.
- **`alias`** answers with the single room holding it, still inside the `data` array so one response shape serves both forms of the request.

## Audit

Room creation, closure, reopening, and deletion are audited. The record names the room, its application, and its new state.

**Neither the alias nor the name is recorded.** Both are labels an application chose, and either may say something about the people using the room — `private-therapy-group` is an alias someone could plausibly write. What an operator needs is which room changed and into what state, and a test asserts the labels stay out.

## Not Yet Implemented

- **`Idempotency-Key`** on room creation (`M08-007`). The behavior is already specified in [`api-compatibility.md`](api-compatibility.md); the store that makes a repeated key return the original response is shared infrastructure that M09 will also need, so it is being built as its own slice.
- **Membership and access policy** (`M08-010`). Convia holds no credentials for an application's people, so it cannot decide who may enter a room; the application already knows. Inventing a policy model Convia could not enforce would be worse than having none.
- **Erasure**, as above.
- **Capacity enforcement**, which needs calls to enforce against.
