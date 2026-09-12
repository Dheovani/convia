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

This is a product judgment rather than an implementation detail: ending a conversation people are having because an administrator tidied a listing would be the wrong default. M09 implements it — see [`calls.md`](calls.md) — and a test asserts that closing a room leaves the call in it running. An application that needs to eject participants will need an operation that says so, and that operation belongs to the call domain, not this one.

## Capacity

`max_participants` is optional and bounded between 1 and 1000. Absent means the room states no limit of its own.

**Convia records it, and M10 enforces it.** Joining a call counts the people already in it against this limit, under a lock on the call so two simultaneous arrivals cannot both take the last seat. Capacity bounds who is present rather than who has ever been, which is what lets a small room host a long conversation people come and go from. See [`participants.md`](participants.md).

## Retrying a Creation

A client that receives no response cannot tell whether its request was lost on the way out or on the way back. Retrying is the only thing it can do, and without help that retry creates a second room.

`POST /v1/rooms` accepts an **`Idempotency-Key`**. With one, the room is created at most once: a repeat of the same request returns the original response, entity tag included, and the handler is never reached. The full rules are in [`api-compatibility.md`](api-compatibility.md); what matters here is the shape of the guarantee.

- **The header is optional**, and a request without one is served exactly as before.
- **A key reused for a different body** is refused with `409 conflict`. Replaying would answer a question the caller did not ask; performing it would defeat the key.
- **A key whose first request is still running** is refused with `409 conflict` rather than queued, and succeeds once that request has finished.
- **A key belongs to the caller that presented it.** An application's keys are scoped to the application; an operator's belong to its credential, because an operator acts on many tenants and its keys are its own.

This is why an alias collision answers `409` rather than resolving to the existing room. Creation says whether it created something, and the key — not the alias — is what makes retrying safe.

**Only creation accepts a key.** Closing, reopening, and deleting are already repeatable, so a key would add a failure mode to operations that have none.

The mechanism lives in [`internal/idempotency`](../internal/idempotency) and knows nothing about rooms. Starting a call adopted it in M09 by marking one route.

## Isolation

Every room operation is scoped to one application, and how the tenant is decided differs by surface:

- **`/v1/rooms`** — the tenant comes from the presented credential. There is no request field that could name another application, so a tenant-crossing bug at the transport layer is unrepresentable rather than merely unlikely.
- **`/v1/applications/{id}/rooms`** — an operator acts on someone else by definition, so the application is named in the path. What bounds it is the key: an operator credential carrying a tenants scope, which no application key can hold.

Reaching another application's room answers `404`, not `403`. A `403` would confirm the room exists.

Scopes are `rooms:read` and `rooms:write` on the tenant surface, and `tenants:read` / `tenants:write` on the operator surface. See [`authentication.md`](authentication.md).

## Retention and Deletion

A deleted room is **retained, not destroyed**. The row stays for the erasure window defined in [`users.md`](users.md), which keeps the deletion recoverable and the alias reserved.

Erasure — the job that actually removes retained rows and frees the aliases they hold — does not exist yet for any domain. It is one mechanism serving users and rooms alike, so it belongs in the milestone that builds it rather than being half-built twice.

Idempotency keys are retained for 24 hours and reclaimed by the request that reuses one, so an expired key never stands between a caller and its answer. Removing keys nobody comes back for is bulk work for that same retention job; until it exists, the table grows with the creations that asked for the guarantee.

## Listing and Filtering

`GET /v1/rooms` returns rooms newest first, using the cursor pagination defined in [`api-conventions.md`](api-conventions.md).

- **Deleted rooms are excluded** unless `status=deleted` asks for them, so a routine listing shows what an application can still use.
- **`status`** filters by lifecycle state and is indexed.
- **`alias`** answers with the single room holding it, still inside the `data` array so one response shape serves both forms of the request.

## Audit

Room creation, closure, reopening, and deletion are audited. The record names the room, its application, and its new state.

**Neither the alias nor the name is recorded.** Both are labels an application chose, and either may say something about the people using the room — `private-therapy-group` is an alias someone could plausibly write. What an operator needs is which room changed and into what state, and a test asserts the labels stay out.

## Membership

Who belongs to a room.

Migration `00009` recorded that Convia does **not** model this, and gave a
reason: Convia held no credentials for an application's people, so it could not
enforce a policy about who belongs, and the application already knew. **M18
invalidated that reason.** A person now signs in to Convia's own product and
presents a session, and at that moment Convia is the only party that can answer
whether they may open a room. So the concept arrives now, when something finally
depends on it, rather than having been guessed at earlier.

### It gates people, not applications

An application's key already carries full authority over its own rooms.
Requiring it to add itself as a member before posting a deployment notice would
be ceremony that protects nobody, so **the tenant surface is not gated by
membership**. Membership is what the *session* surface checks, where somebody
acts as themselves.

That is the line:

| | says |
| --- | --- |
| The application's key | which application is calling |
| Membership | which of that application's people may be where |

Deriving membership from participation was considered and rejected. A chat-first
room is one somebody joins and reads before any call happens in it, so a rule
built on `participants` would leave every silent room with no members at all.

### No role, no lifecycle

A participant has a role because a call has something to moderate: removing
somebody mid-conversation is an act with a victim and a witness. A room member
has no such power to hold — the application manages membership through its own
surface — and a column nothing reads is one that will acquire a meaning by
accident.

A participation records `joined`, `left` and `removed` because a call's roster
is the record of one occasion. Membership is **current state**: somebody is in
the room or they are not. Leaving and coming back leaves no trail here, and the
audit log already records both acts for anybody who needs the history.

A guest has no membership. They have no Convia user, their stint is one call,
and they reach it with an invitation rather than by belonging anywhere.

### What it costs and what it does not

**Adding is idempotent by the person.** Adding somebody twice is the same
outcome as adding them once, which is why it is a `PUT` on the person's own
address rather than a `POST` to a collection. It answers `201` the first time
and `200` afterwards, so an application reconciling its own list against
Convia's can tell whether it changed anything. Only a change is audited, because
a trail full of events where nothing happened is one nobody reads.

**Removing leaves what they said.** The messages are the room's record of a
conversation that did happen, and withdrawing them would rewrite it for
everybody still there. Removing a person from the record entirely is erasure,
which is a different act and the person's to ask for. Removing somebody who was
not there is not an error.

**A closed room still takes members.** Closing stops new calls and new messages;
it does not evict the people who were there, and adding somebody to a finished
room so that they can read its history is reasonable. A deleted room is refused.

**A suspended person is refused.** Suspension withdraws access, and handing
somebody a room to write in would be the one place it did not.

### The API

| Route | Scope | What it does |
| --- | --- | --- |
| `PUT /v1/rooms/{room_id}/members/{user_id}` | `members:write` | give somebody a place |
| `DELETE /v1/rooms/{room_id}/members/{user_id}` | `members:write` | take it away |
| `GET /v1/rooms/{room_id}/members` | `members:read` | who is in this room |
| `GET /v1/users/{user_id}/rooms` | `members:read` | what rooms is this person in |

**The scopes are new rather than reused, and that is deliberate.** A credential
that creates and renames rooms has never been able to touch people. Folding
membership into `rooms:write` would silently widen every key already issued into
one that can put anybody anywhere.

Listings are ordered by the column their index already holds — the person for a
room's members, the room for somebody's rooms — so the cursor is the last
identifier on the page rather than an encoded pair. A member carries **no
display name**, for the reason a call roster does not.

### Not built yet

The session surface. Membership exists and is enforceable, but nothing enforces
it yet, because no route lets a person act as themselves on a room. That is the
next step, and it is what `M31-009` is finally for: `GET /v1/me/rooms`, and the
message routes underneath it, where the author is the session rather than a
field in the request.

## Not Yet Implemented

- **Membership and access policy** (`M08-010`). Convia holds no credentials for an application's people, so it cannot decide who may enter a room; the application already knows. Inventing a policy model Convia could not enforce would be worse than having none.
- **Erasure**, as above.
- **Membership as a policy Convia enforces.** Still deliberately absent, and M10 restated why: Convia holds no credentials for an application's people. See [`participants.md`](participants.md).
