# ADR 0001 — The boundary between Convia's control plane and its media plane

**Status:** Accepted
**Date:** 2026-09-09
**Milestone:** M11 — Internal Media Boundary

## Context

Convia owns rooms, calls, and participants. It does not transport audio and video, and it should not: [`AGENTS.md`](../../AGENTS.md) is explicit that the control plane and the media plane are separate concerns, that the intended first implementation is LiveKit, and that LiveKit is *an infrastructure implementation detail, not Convia's public domain model*.

By the end of M10, everything Convia does happens without any media plane at all. Rooms exist, calls start and end, people join and are removed — and nobody can talk, because nothing carries their voices. M12 changes that by adding a LiveKit adapter.

The risk is well understood and easy to walk into: an adapter is added, and then a provider concept spreads. A grant appears in a service signature, a provider room name is returned in a response "just for debugging", an SDK type ends up in a handler. Each step is small and each is hard to undo, because the last one is a breaking change for every client that read it.

The question this ADR answers is what to build **before** the adapter exists so that this does not happen, without inventing an abstraction for requirements nothing has yet.

## Decision

### The boundary is a package, and it is narrow

[`internal/media`](../../internal/media) holds the Convia-owned types the control plane uses to talk about media: a `SessionRequest`, a `Session`, and two failures. The interface itself is declared in [`internal/calls`](../../internal/calls), the package that consumes it, as Go convention and `AGENTS.md` both prefer.

### It has exactly the two operations implemented flows need

- **Open a session** — a call begins, so a place for it must be realized.
- **Close a session** — a call ends, so that place must be released.

Issuing a participant the credentials to connect, and disconnecting one who was removed, are **deliberately absent**. Nobody can connect today, so their shape would be a guess, and a guess made now would be the thing M13 has to work around. They arrive with the join sessions of M13, from a flow that exists.

`AGENTS.md` permits an interface for *architectural isolation*, which is exactly this case: the isolation is the requirement, not a side effect.

### It is not a multi-provider abstraction

There is one intended provider. Designing for hypothetical others would be inventing requirements, which M11-012 rules out and `AGENTS.md` rules out generally. What the boundary provides is not portability-in-advance but **containment**: nothing about a provider reaches the domain or the contract, so replacing one later is work confined to one package.

### The provider reference is unrepresentable in the domain

A realized session has an opaque reference belonging to whichever provider realized it. Convia stores it on the call row so it can release the session later, and:

- it is **not** part of the projection the domain reads, so `calls.Call` has no field for it;
- reading it requires asking the store for it by name;
- no public representation has anywhere to put it.

This is a deliberate choice of *unrepresentable* over *tested*, and it is worth being accurate about how much that buys.

The alternative — a field on `calls.Call`, kept out of responses by discipline — would **not** have been dangerous today. Responses are built field by field by `represent`, so a new field does not reach a client by itself; nothing serializes a domain call directly. The realistic leak paths are elsewhere and none of them is imminent: a whole struct logged with `slog` or `%+v` in an error path, a future serializer written from the domain type rather than the contract (a webhook payload, a generated SDK), or a response type produced by conversion or embedding rather than by mapping.

The argument for keeping it out of the projection is therefore modest, and it is not urgency. It costs one extra read when a call ends, and in exchange the guarantee holds for code nobody has written yet, by people who have not read this document. That is enough to justify it; a claim of imminent danger would not have been true.

### The call record is authoritative; the session is realized from it

The order is: create the call row, then ask for a session. It has to be this way — the row is what holds the room, so realizing first would let two callers both realize a session for a room only one of them can have.

**A call whose session cannot be realized is ended, not left standing.** Otherwise the room would be blocked by a conversation that never happened, which [`calls.md`](../calls.md) already named as the worse failure. The attempt stays in the history with a reason rather than being erased.

**An ending never depends on the media plane.** Releasing a session is best-effort: a provider that cannot be reached must not be able to keep conversations open in Convia that ended in reality. A session that could not be released is logged. Reclaiming one belongs to the adapter, because the adapter is the only thing that can list what its provider still holds.

The reference is **not** cleared when a call ends, because clearing it would throw away the only handle a later attempt could use.

### Failures are retryable or terminal, and the difference reaches the client

| | Meaning | Convia's answer |
| --- | --- | --- |
| `media.ErrUnavailable` | Could not be reached, or did not answer in time | `503 unavailable` — retry |
| `media.ErrRejected` | Understood and refused | `500 internal_error`, logged with detail — an operator must act |

Collapsing these would be wrong in both directions. Treating every failure as retryable turns a misconfiguration into an infinite retry loop; treating none as retryable turns a one-second outage into a failed call. `503 unavailable` is a new error code, added because telling a client `internal_error` for a transient dependency outage would mislead it into not retrying.

### Idempotency is arranged so the adapter does not have to guess

Convia asks for a session exactly once per call, when the call begins, and remembers the answer. An adapter is never asked to open a session for a call that already has one, so it never has to decide whether a second request means a second room. Releasing is asked for once, when the call actually transitions to ended; a repeated end releases nothing further.

### Convia ships with no media plane, and that is a real configuration

`media.Absent` is not a stub standing in for something missing. A control plane with no media transport configured is a coherent thing to be, and every operation succeeding-and-doing-nothing means the call lifecycle behaves exactly as it did before this boundary existed. A media plane that failed instead would make the control plane depend on infrastructure it does not have.

### The boundary is enforced by a test, not by review

[`internal/media/boundary_test.go`](../../internal/media/boundary_test.go) asserts that media infrastructure is imported only from under `internal/media`, and that the public contract never names a provider.

The import check works from an **inverted list**: every third-party module Convia depends on is named there with the reason it is not media infrastructure, and anything else is confined to the media plane until someone deliberately says otherwise. Listing the providers instead would mean guessing which ones a future contributor might reach for, and the guess would be wrong exactly when it mattered.

Today it passes trivially, because there is no provider. That is the point: it is a tripwire left for M12.

## Consequences

**What this buys.** A provider can be added, replaced, or supplemented as work inside one package. Public contracts stay Convia's. A media outage degrades in a way clients can act on, and never corrupts call state.

**What it costs.** Two extra writes on the ordinary path — attaching the reference, and reading it back when the call ends. An ended call may leave a session the provider still holds if releasing failed, which is visible in the logs and is not reconciled automatically.

**What is deliberately unfinished.**

- Reconciling sessions a provider still holds. It needs something that can list them, which is the adapter (M12).
- Access credentials and disconnection (M13).
- Whether the media plane should also enforce a room's capacity. The control plane already does; a second enforcement point is defence in depth, and M12 can decide whether its provider makes that cheap.

**Revisiting this.** If a second provider is ever genuinely required, the interface here is where it is discovered whether these two operations were the right shape. Nothing in this ADR assumes they were: it assumes only that they are the ones implemented flows need today.
