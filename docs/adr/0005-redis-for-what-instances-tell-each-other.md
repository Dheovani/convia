# ADR 0005 — Redis for what instances tell each other, and nothing else

**Status:** Accepted
**Date:** 2026-09-10
**Milestone:** M16 — Redis and Distributed Ephemeral State

## Context

`AGENTS.md` lists Redis in the intended stack and immediately constrains it: *do not use Redis as the canonical source of truth for durable domain data*, and *do not add either database until required by an implemented feature*. M16 turns that into a gate — `M16-001` asks for the first concrete use case **before** the dependency is added, and the milestone's own dependency line says it needs *a demonstrated distributed-state requirement*.

That requirement was demonstrated, and written down, before this ADR existed. [ADR 0003](0003-a-one-directional-in-process-control-event-stream.md) built the event stream on an in-process broker and stated the consequence plainly: **a subscriber connected to instance A never sees an event produced on instance B.** `docs/events.md` went further and said what must not happen — several instances behind a round-robin balancer, which would look like it worked.

So the question this ADR answers is not "should Convia use Redis". It is "what exactly, and how do we keep it that."

## Decision

**Redis carries control events between the instances of one deployment, over publish/subscribe, and does nothing else.**

### Why publish/subscribe is the right shape, not just a convenient one

The milestone's exit criterion is that Redis *cannot become an accidental durable authority*. Usually that is a rule somebody has to keep following. Here it is structural:

- **Pub/sub writes no keys.** There is nothing to expire, so `M16-006`'s TTL requirement has no subject; there is nothing for an eviction policy to reclaim, so `M16-011`'s assumptions are that no assumption is needed. A test asserts the key count does not move while events are carried, so the day somebody starts storing something, it fails.
- **A message nobody is listening for is gone the instant it is sent.** That is the strongest bound on data lifetime available, and it is exactly what a live stream already promises: ADR 0003 said an event produced while a subscriber is disconnected is not waiting for them.

The local Redis is configured with persistence off — `--save "" --appendonly no` — so a restart loses no Convia state by construction rather than by luck.

### What this must never be used for

Convia now has three ways to say something happened, and it is worth writing down which is which so a future change does not reach for the nearest one:

| | Where it lives | If the consumer is not there |
| --- | --- | --- |
| Live stream + this relay | nowhere; in flight only | gone |
| Webhook | a row in PostgreSQL | retried, then given up on with a record |
| The resource itself | PostgreSQL | read it |

Anything a consumer must not miss belongs in the second row. Anything Convia must be able to answer belongs in the third. What travels over Redis is a **copy of something already recorded**.

### Broadcasting cannot block or fail

`events.Relay` declares `Broadcast(event)` with no context and no error, which is the same contract `Broker.Publish` has had since M14 and for the same reason: announcing what happened must not be able to slow down or fail the operation that happened.

The implementation therefore queues rather than sends. A full queue drops the event and counts it, because the alternative is putting a network round trip inside starting a call. That is acceptable *here and nowhere else* — this is the best-effort half of Convia's delivery, and the durable half is webhooks, whose queue write is synchronous precisely because it must not be lost ([ADR 0004](0004-queuing-a-durable-delivery-inside-the-request.md)).

### An unreachable Redis is not a reason to stop serving

An instance that refused to start, or that failed readiness, because the shared channel was unreachable would take a working API offline over a stream that degrades to exactly what it was before M16. So:

- startup does not wait for Redis; it pings once and reports loudly if that fails;
- readiness deliberately does not include Redis;
- go-redis re-establishes the subscription underneath, so an outage leaves a gap in what subscribers saw rather than a stream that never recovers.

`M16-012` asks that durable state stay recoverable without Redis. It does, trivially and by construction: nothing durable is in Redis, so there is nothing to recover.

### One channel, not one per tenant

`M16-005` asks for tenant scoping in the key naming. The channel is `convia:v1:events` — a namespace so Convia's traffic is recognizable on a Redis somebody else is also using, and a version so a future envelope can run beside this one during a rolling deployment.

It is one channel for the whole deployment. Tenant scoping is applied where it always was: the broker decides who receives what from the credential that opened the stream, and it decides it the same way whether the event arrived locally or over the wire — which a test asserts directly. A channel per tenant would reduce traffic between machines that already share a database, at the cost of subscribing and unsubscribing as streams come and go. That is an optimization for when the traffic justifies it, not a security boundary; all instances of a deployment are one trust domain and already read the same database.

### Every message names its sender

Redis delivers a published message to every subscriber, including the publisher. Without something to recognize its own copy by, each instance would deliver every local event twice — once directly from the broker and once round-tripped.

Each process therefore generates an origin at startup and drops messages carrying it. It is generated rather than configured on purpose: a value an operator had to set is one they could set the same on two machines, and the failure would be silent duplicate delivery.

### The dependency

**`github.com/redis/go-redis/v9`**, BSD-2-Clause. Its cost was measured rather than assumed: three modules enter the build — the client, `github.com/cespare/xxhash/v2` (MIT), and `go.uber.org/atomic` (MIT). Everything else in its module graph is a test dependency of a dependency.

Hand-rolling RESP and a resubscribing pub/sub loop was the alternative and was rejected for the reason `AGENTS.md` gives: a dependency is acceptable when it materially improves correctness, and reconnection semantics under a network partition is exactly that kind of code.

## Consequences

- **A deployment can now run more than one instance and keep a complete stream**, which is the constraint `docs/events.md` has carried since M14 and which the same document now records as lifted — with the operational requirement stated in its place: `CONVIA_REDIS_URL` must be set on every instance, or each serves only its own subscribers.
- **Convia still runs perfectly well without it.** A nil relay is the ordinary deployment and every local process. The code path that carries events between instances is absent rather than merely unused, so a single-instance Convia cannot fail in a way that only a multi-instance one could.
- **A new tripwire.** `internal/events` may not import `internal/events/redis`, and neither may any domain, handler, or test outside the composition root — the same containment the media provider has, for the same reason. The moment something reaches past it, "Convia works without Redis" stops being a deployment choice and becomes something somebody has to remember.
- **The media boundary test grew three entries.** Its inverted allowlist means any new third-party module is presumed to be media infrastructure until somebody says otherwise with a reason, and go-redis and its two dependencies are now named there. That is the mechanism working, not a workaround.
- **Metrics wait, again.** `M16-010` asks for pool usage, operation latency, and failures. Convia still has no metrics pipeline, and `AGENTS.md` says not to add an observability stack before there is something worth observing. What exists meanwhile is the count of events that could not be carried, reported when the process stops and while it is happening — which is the number those metrics would be derived from.
- **`M17` (presence) now has its foundation.** Presence is the case Redis is usually reached for, and it will want keys with TTLs rather than pub/sub alone. When it does, the conventions this milestone established — the namespace, the version, and the rule that nothing here is a source of truth — are what it inherits.
