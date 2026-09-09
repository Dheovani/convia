# Calls

A **call** is a conversation held in a room. This document records the decisions of milestone M09 in [`TODO.md`](../TODO.md). The domain lives in [`internal/calls`](../internal/calls), its schema in [`internal/database/migrations`](../internal/database/migrations), and its contract in [`api/openapi.yaml`](../api/openapi.yaml).

> **No media provider appears anywhere here.** A call is a Convia concept that the media plane will later be asked to realize, never the other way round. Nothing in this document or in the public contract names LiveKit.

## The Room Is the Place; the Call Is the Occasion

A room outlives every conversation held in it. That is the whole reason the two are separate resources, and it is what [`rooms.md`](rooms.md) promised M09 would build on.

Creating a room is an administrative act an application does once. Starting a call is something people do repeatedly, in a place that already exists, with capacity and naming already decided.

A call takes its identity from the room it happens in and the moment it starts. There is very little to ask for when starting one, and that is deliberate.

## Two States, Both Reachable

```
        start                     end
   ───────────▶ active ────────────────────▶ ended
```

`ended` is terminal. A conversation is happening, or it is over.

**Convia deliberately does not model `ringing`, `ending`, or `failed`.** Each describes something Convia cannot currently observe:

| State | What it would describe | What it needs first |
| --- | --- | --- |
| `ringing` | Someone being summoned | Invitations — M10 |
| `ending` | A teardown in flight | A media plane that takes time to tear down — M11 |
| `failed` | A call that could not be established | Something that can fail to establish one — M11 |

Adding them now would put states in the vocabulary that nothing could enter, and a lifecycle with unreachable states teaches a client rules that are not true. Why a call ended is recorded as **`end_reason`** rather than as a state, so the record says what happened without the vocabulary claiming more than Convia can see.

## A Room Holds One Call at a Time

A second conversation in the same place would mean two groups of people talking past each other. Starting a call in a room that already has one answers `409 conflict`.

**It is refused rather than answered with the running call**, for the same reason an alias collision is refused rather than resolved: a caller that received the current call could not tell whether it had just started something. Reading the current call is `GET /v1/rooms/{room_id}/calls?status=active`, which answers with at most one.

**The rule is enforced by a partial unique index**, not by a check the service performs:

```sql
CREATE UNIQUE INDEX calls_room_active_key ON calls (room_id) WHERE status = 'active';
```

Two requests arriving together cannot both win. An application-level check would read, decide, and lose the race in between; PostgreSQL settles it. Ended calls do not participate in the index, which is what lets a room accumulate a history.

## Who Ended It, and Why

Every ending records an **actor** and, optionally, a **reason**.

The actor is `application` or `operator`. It names the authority that made the request, not the person who was talking — who was in the call is a participant, which is M10.

**The actor is decided by the verified credential, never by a request field.** A body that could name the actor would let an application record an operator's name against its own decision, which would make the history worthless exactly when it matters.

**Ending is repeatable.** A second request finds nothing to change and returns the call as it stands. It does not overwrite the first answer: who ended a conversation is whoever actually ended it, not whoever asked again afterwards.

### Why the history is columns rather than a table

The lifecycle is linear and terminal: a call starts, then ends. A separate transitions table would hold exactly one row per ended call, joined on every read, to express what `ended_at`, `ended_by`, and `end_reason` already say.

A table becomes the right shape once a call can move between states more than once, which is what the media plane will bring. Until then it would be a structure built for states that do not exist. Moving to one later is a data migration, not a redesign.

## Starting and Retrying

`POST /v1/rooms/{room_id}/calls` accepts an **`Idempotency-Key`**, so a retry after a timeout starts at most one call and a repeat returns the original response. The mechanism is described in [`api-compatibility.md`](api-compatibility.md).

**Ending does not accept one, and does not need it.** Repeating an end already succeeds and returns the call unchanged, so a key would only add a way for a retry to be refused.

## What a Closed or Deleted Room Does

This is the decision [`rooms.md`](rooms.md) recorded and this milestone implements.

- **A closed room refuses a new call** with `409 conflict`.
- **A conversation already in progress runs to its end.** Closing a room does not touch it. Ending a conversation people are having because an administrator tidied a listing would be the wrong default.
- **A call in a closed room can still be ended**, by either surface.
- **A deleted room answers `404 not found`**, not `409`. Closed is a state the application chose and can undo; deleted is gone from the API, and reporting it as closed would invite a caller to reopen something that is not there.

## What an Operator May Do

An operator can **read** a tenant's calls and **end** one. An operator **cannot start** one.

Every other resource an operator administers, it can also create, and this breaks that symmetry on purpose. Starting a conversation between an application's people is not administration; it would put Convia in the position of originating something nobody asked for. Ending one is administration: it is the lever an operator needs when a conversation must stop and the application cannot stop it.

The absence is asserted by a test, because the natural instinct of anyone extending this surface will be to restore the symmetry.

## Isolation

Every call operation is scoped to one application, and how the tenant is decided differs by surface:

- **`/v1/calls` and `/v1/rooms/{room_id}/calls`** — the tenant comes from the presented credential. There is no request field that could name another application.
- **`/v1/applications/{id}/calls`** — an operator acts on someone else by definition, so the application is named in the path, bounded by an operator credential carrying a tenants scope.

Reaching another application's call answers `404`, not `403`. A `403` would confirm the call exists. The same is true of a room-scoped history: a room that is not the caller's is **missing**, not empty — an empty page would read as "no calls yet".

Scopes are `calls:read` and `calls:write` on the tenant surface, and `tenants:read` / `tenants:write` on the operator surface. **`rooms:write` does not imply `calls:write`**: a key granted to manage rooms was not granted to originate conversations in them, and conflating the two would make least privilege unexpressible.

## Listing and Filtering

`GET /v1/calls` returns calls newest first, using the cursor pagination defined in [`api-conventions.md`](api-conventions.md). `GET /v1/rooms/{room_id}/calls` narrows it to one room.

**Every call is returned, ended ones included.** This is the opposite of rooms, where a deleted room is hidden unless asked for, and the difference is the point: a deleted room is one an application can no longer use, while an ended call is exactly what an application wants to look back at.

`status` narrows to one kind. Filtering by `active` is how a caller asks what is happening right now.

## Capacity

`max_participants` on a room is still **recorded and not enforced**. Calls did not change that: enforcing a headcount needs participants to count, which is M10.

## Audit

Starting and ending a call are audited. The record names the call, its room, its application, the new state, and the actor.

**Neither the metadata nor the end reason is recorded.** Both are composed by the application and either may say something about the people in the call — `patient-consultation-ended` is a reason someone could plausibly write. A test asserts they stay out.

## Not Yet Implemented

- **Reconciliation of stale active calls** (`M09-015`). A stale active call is one whose conversation is over but whose record was never ended, because a process died between the two. Convia cannot detect one today: with no media plane, it has no evidence about a call independent of the requests it received, so every active call is active as far as anything can tell. When the media plane exists, reconciliation ends such calls with a `system` actor — an actor deliberately absent until something produces it — and the room is freed by the same ending that frees it now.
- **Behavior when the media provider is unavailable** (`M09-010`). The call record is the control-plane truth, and the media session is realized from it. When realizing one fails, the call is ended with a reason rather than left occupying its room, because a room blocked by a call that never happened is the worse failure. Nothing implements this yet because there is no provider to be unavailable.
- **Participants** (`M10`), and with them capacity enforcement and any notion of who was present.
- **Media** (`M11`). When a provider session identifier exists it belongs in the `calls` table and never in the public representation; a contract test already asserts the published schema carries only Convia-owned fields.
