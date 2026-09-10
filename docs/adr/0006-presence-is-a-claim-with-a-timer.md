# ADR 0006 — Presence is a claim with a timer, and never a record

**Status:** Accepted
**Date:** 2026-09-10
**Milestone:** M17 — Presence

## Context

`M17-002` asks that three ideas be kept apart: application presence, Convia connection presence, and call participation. `M17-012` asks that presence be documented as advisory rather than a durable guarantee. The exit criterion asks that it *converge across instances, respect privacy, expire safely, and not be confused with durable participation history*.

The last clause is the one that decides the design. Every other thing Convia exposes is a **record**: it happened, it is in PostgreSQL, and reading it again gives the same answer. Presence is the first thing that is not.

[ADR 0005](0005-redis-for-what-instances-tell-each-other.md) closed by naming this milestone: *presence is the case Redis is usually reached for, and it will want keys with TTLs rather than pub/sub alone. When it does, the conventions this milestone established — the namespace, the version, and the rule that nothing here is a source of truth — are what it inherits.*

## Decision

**Presence is what an application asserts about one of its people, held for as long as it keeps asserting it, and it is never derived from anything durable.**

### Convia does not observe presence

The obvious design is for Convia to notice a connection going away. Convia cannot, because **no user connects to Convia.**

The only socket Convia serves is the control-event stream, and it belongs to an application's *backend*: one stream carries the whole tenant's events, and it is opened with an application key. A participant's client connects to the media plane, which is a different system that Convia deliberately knows almost nothing about ([ADR 0001](0001-control-plane-media-plane-boundary.md)).

So the honest answer to `M17-002`'s middle case is that **there is nothing there**. Convia connection presence is not modelled, not reported, and not approximated by something adjacent — holding a media session credential is not being connected, and reporting it as if it were would be a signal Convia invented.

That is worth stating rather than leaving as an omission, because the day Convia does serve per-user connections, the field it would add is a different field from the one this ADR defines.

### Call participation is not folded in

This is the clause the exit criterion is about, and the decision is to keep the two apart in the strongest available way: `in_call` is **not a field** on the presence resource, and `internal/presence` may not import `internal/calls` or `internal/participants` — a test enforces it.

The reason is not tidiness. Participation is durable and authoritative: Convia put the person in the call and holds the row. Presence is advisory and expires. Folding the first into the second would put a durable fact behind an advisory expiry, and the first time Redis was unreachable, Convia would report that nobody was in any call — false, and contradicted by `GET /v1/calls/{call_id}/participants` in the same second.

An application that wants both asks two questions, which is one more round trip and one fewer way to be wrong.

### The states, and why `offline` cannot be asserted

`busy`, `online`, `away` are asserted. `offline` is derived: it is what a person is when nothing is saying anything about them.

Refusing `offline` as an input matters because presence aggregates across devices. A client that could assert it would be claiming somebody is unavailable while another of their devices is plainly active, and the aggregate would have to decide which of two contradictory statements to believe. Making it unrepresentable removes the question.

The aggregation order is **`busy`, `online`, `away`** — strongest first, and deliberately not "most available first":

- `busy` is the only state a person asks for on purpose. A request not to be disturbed must not be undone by a laptop in another room reporting activity.
- `away` is the only one an application normally *infers* — an idle timer rather than an act — so it ranks below the two that are assertions.

The rule lives in exactly one place, `presence.States()`, and both stores call the same `Aggregate`.

### Convia's clock, never the caller's

A caller sends a **lifetime**, never a deadline. `presence.Assertion` has no time field at all.

This is `M17-009` answered structurally rather than by assuming NTP. A client with a clock a day fast cannot make somebody permanently available, because it never sends a time. And in a deployment of several instances the deadline is computed by **Redis**, inside the script that writes it, using `TIME` — so two instances whose own clocks differ by a minute agree exactly on when a claim lapses, because neither is asked.

A delayed heartbeat therefore needs no special handling: it is a heartbeat that arrived after the claim lapsed, so the person went offline and came back, which is what happened.

### Redis stores; Go decides

The shared store's Lua scripts read, prune, and write claims. They do **not** compute what a person's presence is.

An aggregation rule implemented twice — once in Lua and once in Go for the in-process store — is a rule that will eventually be two different rules, and the disagreement would surface as a roster that changes depending on which implementation answered. So the scripts return raw claims and `presence.Aggregate` decides, for both.

What the scripts *are* for is atomicity. Asserting reads the claims, drops the lapsed ones, decides whether this device is new, enforces the per-person ceiling, and writes the deadline in two places. Two instances heartbeating for the same person at the same instant would otherwise interleave into a state neither asked for.

### Expiry is announced by sweeping, and correctness never depends on it

Redis expires a key without telling anybody, and a person going quiet is exactly the transition an application cannot observe for itself — leaving on purpose is announced by the operation that did it.

So deadlines are indexed in a sorted set and every instance sweeps it every five seconds. **The claim is the removal of the stored device field, not of the deadline**: a script runs alone on Redis, so the instance that read a value is the only one that will, and the others find nothing. No lock, no leader, no lease.

Crucially, **a read ignores a claim past its deadline whether or not anything has swept it.** A sweeper that stops running costs subscribers the announcement, never the answer. That is what makes `M17-008` — unclean disconnects and process crashes — uninteresting: a crashed instance's claims lapse on their own, and the next sweep any instance runs announces them.

### Advisory and durable-with-retries are contradictory

`presence.changed` streams. It is refused on a webhook endpoint, at registration, with a message naming the stream instead.

A webhook is a delivery with attempts behind it. A presence report that failed once arrives after it stopped being true, and after the newer one that replaced it — leaving an application with a roster that never settles. A stream has no such problem: an event reaches whoever is connected at that moment, in order, or not at all.

This makes `M17-012` structural rather than documentary. Presence cannot be treated as durable because Convia will not deliver it durably.

### Only a move is announced

A heartbeat that refreshes a deadline without changing the aggregate publishes nothing.

Without this, presence would produce one event per person per device per twenty seconds, on every stream of the tenant, carried to every instance, for a subscriber to compare against what it already had. The filter is applied where the change is known — the store reports what presence was and what it now is, and the service compares.

Under a race two instances can both announce the same move. That is accepted: a presence event is a **state report** rather than a transition, so applying it twice is applying it once. The alternative is a lock on the hottest write path in Convia.

### Presence is not audited

Everything else Convia announces is written to the audit log beside the event, because it is a change to a record.

This is not, it arrives thousands of times more often, and where a person is at a given minute is the last thing that belongs in a durable log. `M17-005` asks for privacy policy; this is part of the answer, and so is the absence of a device count from every response.

## Consequences

- **A new dependency on the same Redis, with a second client.** `internal/presence/redis` opens its own connection pool rather than sharing the relay's. The duplication is about fifteen lines of URL and timeout handling, and the alternative — a shared `internal/redis` package — would weaken the containment both packages currently have, where the composition root is the only thing that names either.
- **The first keys Convia has ever written.** ADR 0005 could say there was no eviction policy to assume because pub/sub stores nothing. That is no longer true, so the assumption is stated instead: if Redis evicts a presence key, presence reads as offline and nothing else is affected. It is the only assumption presence makes, and it is in the safe direction.
- **A read on the write path.** Asserting looks the user up in PostgreSQL, which puts a primary-key query inside the most frequent request Convia serves. It is deliberate: without it an application could assert presence for any string of the right shape and use the ephemeral store as scratch space keyed by whatever it liked, and presence would stop being about people.
- **`503` where the rest of Convia returns `500`.** An unreachable store is reported as a condition that will pass, with `Retry-After`, because presence has no durable copy to fall back on and answering `offline` would be a false statement about people rather than a degraded one.
- **Readiness still ignores Redis.** An instance that cannot report who is available is answering every other request correctly. Taking it out of the load balancer would turn a narrow failure into an outage — the same reasoning ADR 0005 applied to the event stream.
- **Metrics wait, a third time.** `M17-011` asks for active users and stale entries without high-cardinality labels. Convia still has no metrics pipeline, and `AGENTS.md` says not to add an observability stack before there is something worth observing. `M22` is where all three of these land together.
- **CI grew a Redis.** Everything that spans instances was being skipped: the M16 relay tests said CI would set `CONVIA_TEST_REDIS_URL` and no job did. Presence made that consequential enough to fix, so the integration job now runs a Redis and both sets of cross-instance tests actually execute.
