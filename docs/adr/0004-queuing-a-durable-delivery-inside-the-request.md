# ADR 0004 — Queuing a durable delivery inside the request

**Status:** Accepted; its queueing outside the domain transaction is superseded by [ADR 0017](0017-events-are-recorded-with-the-change-that-caused-them.md)
**Date:** 2026-09-10
**Milestone:** M15 — Webhooks for External Applications

## Context

[ADR 0003](0003-a-one-directional-in-process-control-event-stream.md) gave the event stream a property that was the whole point of its design: **publishing cannot block and cannot fail.** `Publish` took no context and returned no error, so a domain service could announce what happened and carry on, and a subscriber that stopped reading cost that subscriber a connection and cost the request nothing.

M15's goal begins with a different word: *notify server-side consumers of durable Convia events **reliably***. A delivery that exists only in memory is a delivery a restart loses, so a webhook has to be written down. That is a database write, and a database write can block and can fail.

The two cannot both be true of the same call.

## Decision

**Split the guarantees, and let the announcement carry a context.**

`Publish(ctx, event)` still returns nothing, and one thing composes the two halves:

- the **broker** reaches whoever is connected right now, cannot block, cannot fail, holds nothing;
- the **durable sink** records what is owed to registered destinations, runs before the response is returned, and can fail.

`events.Announcer` is that composition, and the domains depend on it rather than on both.

### Why the queue write is synchronous

The alternative was to hand the event to a buffered channel drained by a writer goroutine, which would have kept the old signature exactly. It was rejected because it is the same failure the durability was supposed to remove, moved one step later: a full buffer drops the delivery, a crash drops everything still in it, and both are silent.

The cost of doing it synchronously is one statement. For a tenant with no endpoints — every tenant until one registers — it is a single indexed lookup that returns no rows and no insert happens at all, which is what makes it acceptable on the path of every announced event.

### Why it is not in the same transaction

The honest ideal is a transactional outbox: write the delivery in the transaction that wrote the domain change, so that either both happen or neither does. Convia's stores each own their transactions and none of them is threaded through another package, so this would mean every write path in `calls`, `participants`, and `invitations` accepting a hook that runs inside its transaction — a parameter added to a dozen methods and a coupling between domains that currently do not know about each other.

That is a large change to close a small window: a crash in the milliseconds between the domain transaction committing and the delivery being inserted. Under `AGENTS.md`'s instruction to keep the design small, it is not the change to make on the way past.

### Why the failure is not returned

`Publish` returns nothing, so a queue write that fails does not fail the request. The domain change has already committed. Failing the response now would tell the caller that nothing happened when something did, and a caller that retried would produce a second call, a second participant, or a second invitation — which is worse than a missed notification.

What happens instead is that the failure is logged at error level, naming the event identifier, so it can be matched with the audit entry for the same occurrence. `docs/webhooks.md` says so plainly under a heading of its own, because a consumer that believes deliveries are guaranteed will build on that belief.

## Consequences

- **A missed queue write is an event no destination ever receives**, and the only trace is a log line. That is the cost of this decision, and it is written down in the operator-facing documentation rather than only here. Closing it means the transactional outbox above, and `M15-016` records that.
- **Announcing can now be slow.** It could not before. The window is one statement against an index, but it is on the path of every call started, every person admitted, and every removal. If it ever shows up in a latency profile, the fix is the outbox — which removes the extra round trip as well as the gap.
- **Webhooks are safe across instances and the stream is not.** The queue is a table with `FOR UPDATE SKIP LOCKED`, so workers divide a backlog rather than duplicating it, and a worker that dies mid-attempt strands nothing because its lease expires. That is a nice contrast to draw for anyone deciding between the two: the durable path was multi-instance from the first commit, and the live one still needs `M16`.
- **Delivery is at-least-once, and consumers are told so first.** A destination that received a body and failed to answer is indistinguishable from one that never received it. Every delivery therefore carries a stable identifier, and the documentation leads with recording it rather than mentioning it at the end.
- **`internal/events` gained an interface it does not implement.** `Sink` is declared there and satisfied by `internal/webhooks`, because the dependency has to run one way: webhooks imports events for the envelope. The boundary test that keeps `internal/events` a leaf still passes, and the interface is what makes that possible. *Amended by [ADR 0020](0020-the-event-vocabulary-is-separate-from-its-delivery.md): `Sink` is declared in `internal/events/serving`, which is where announcing moved. Everything else here holds.*
