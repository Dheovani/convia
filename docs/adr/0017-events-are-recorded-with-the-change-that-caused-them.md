# ADR 0017 — Events are recorded with the change that caused them

**Status:** Accepted
**Date:** 2026-09-17
**Milestones:** M14 — Real-Time Control Events, M15 — Webhooks for External Applications

## Context

Two gaps had one cause: nothing kept an event once it was announced.

- **`M15-016`.** [ADR 0004](0004-queuing-a-durable-delivery-inside-the-request.md) queued a webhook delivery in the request, after the change committed. A crash in between lost the delivery for good, and the only trace was a log line.
- **`M14-006`.** A stream carried what happened while it was open, and nothing else. A client that reconnected — after a dropped connection, a laptop waking up, or falling behind — could only re-read everything over REST.

The product owner decided:

- **one journal closes both**;
- **events are kept for 24 hours**, which covers dropped connections, sleeping laptops and deployments without keeping a long record of who did what;
- **live streams are fed from the journal**, in order and without gaps, at the cost of up to about 200 milliseconds;
- **both streams resume**, the application's and the person's.

## Decision

### The event is written by the transaction that makes the change

`event_journal` holds every event except presence. The domain services wrap each change and its announcement in one transaction, and the webhook delivery is queued in that transaction too. Either the change, its record and what it owes all happen, or none does. A change whose announcement cannot be written is refused, which reverses ADR 0004's "a failed queue write does not fail the request": now nothing has committed when the write fails, so failing is the truthful answer.

### The transaction travels in the context

`internal/transaction` puts the transaction in the context, and every store reaches the database through it when there is one. A store that opens its own transaction inside gets a savepoint. Work that must happen only after the commit — releasing a media session, telling a call somebody left, waking a worker — is registered with `AfterCommit` and runs with a context that carries no transaction.

The alternative was a transaction parameter on every write method in every domain, which is the coupling ADR 0004 declined. A context value is less visible, so every store goes through one method, `db(ctx)`, and a store that forgot would still be correct outside a transaction and only lose atomicity inside one.

### Order is the recording transaction, then the position

Positions are handed out at insert and transactions commit in any order, so a reader going by position could pass a row that commits later behind it. A row also records `pg_current_xact_id()`. Readers take only rows whose transaction is older than every transaction still running (`pg_snapshot_xmin`), ordered by transaction and then position. Nothing still running can commit behind such a row, so the order a reader sees is final. The cursor a client receives is that pair.

The price: **a transaction left open anywhere on the database server holds back every newer event** until it ends, because the snapshot's horizon is the server's.

### Each instance follows the journal itself

A follower per instance reads the journal from its head at startup and hands what it reads to its own streams, waking at once when this instance records something and otherwise every 200 milliseconds. Recorded events therefore no longer travel over Redis, and arrive in the same order on every instance. Presence, which is not recorded, still goes straight to the streams and across instances through the relay.

### A stream resumes by subscribing first

A stream that passes `after` subscribes, then asks the follower how far it has delivered, then replays the journal from `after` up to that point, then goes live. The follower moves its position before delivering each event, so an event is either in the replay, or arrives live after the subscription, or both; a live event at or before the last replayed cursor is skipped.

A person is replayed what happened in the rooms they are in now, and every change to their own place, rather than what they were entitled to at the time. That withholds only what they could no longer read.

### Old events are pruned, and the newest removed is remembered

The follower removes events older than 24 hours every ten minutes, and `event_journal_floor` keeps the newest cursor removed. A cursor before it is closed with `4002`, so a client never mistakes a gap for silence.

## Consequences

- Webhook deliveries can no longer be lost between a change and its queueing, and a request whose queue write fails now fails.
- Every announced change costs one more insert, in its own transaction.
- Live events may reach a stream up to about 200 milliseconds after they commit.
- An open transaction anywhere on the database server delays every stream.
- A cursor is global: its numbers reveal roughly how much the whole installation records, though nothing about what.
- Presence is still advisory, unrecorded, and relayed over Redis.
- ADR 0004's section on why the queue write is not in the same transaction, and its consequence that a failed write loses the delivery, no longer hold.
