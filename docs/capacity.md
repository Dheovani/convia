# Capacity, and when to add an instance

**One instance of 2 vCPU and 4 GB carries the hosted service's whole size at about 0.9 of its processors, and two carry it at about half a processor each.** Processor is the first thing to run out, and the database pool the first thing to show it. This is `M25-013`: what that capacity rests on, what bounds an instance before its processors do, and the numbers that say it is time for another one. [`scaling.md`](scaling.md) says what the size is and why several instances behave as one; [`load.md`](load.md) is every measurement quoted here.

## What one instance carries

Measured on 2026-10-11 at the size in [`scaling.md`](scaling.md#what-convia-is-sized-for), for five minutes, every target met:

| | one instance | each of two |
| --- | --- | --- |
| Processor, average | 0.89 of 2 | 0.53 of 2 |
| Memory held, peak | 309 MB of 4 GB | 293 MB |
| Event streams | 1 000 | 500 |
| Goroutines, peak | 2 177 | 1 099 |
| Queries waiting for a database connection | 15 a second | none |

The load was 100 messages a second posted by tenants and 50 by people, 200 and 100 reads, 32 presence heartbeats, 2 sign-ins, and 100 calls held open, each joined by somebody who is handed a credential and leaves before it ends — with every event fanned out to the streams that should hear it. Two instances divide it almost exactly in half, so capacity is close to linear in instances until something they share runs out.

**Most targets were met more than tenfold, and calls by the least.** On one instance, leaving a call took 181 ms at p95 against 300 and starting one 82 ms; on two, 122 ms and 84 ms. A call is the one thing that waits on another server — the media provider — and it is where headroom runs out first.

## What that rests on

Each of these is true of the run, and a deployment where one is not true should measure rather than read this page:

- **Messages are short.** A few hundred bytes. A deployment that carries large bodies spends more on parsing, the journal and every stream that hears them.
- **Fan-out is modest.** A tenant holds four streams and a person one per device; a room has a handful of people. One room with hundreds of people in it costs every message hundreds of deliveries.
- **Signing in is rare.** Two a second. Each one is an argon2id hash of 64 MiB, which is the most expensive thing Convia does on purpose; see below.
- **Every instance has its own 40 database connections**, and they share one PostgreSQL that is not itself the limit. The run's PostgreSQL had 200 connections and was never busy.
- **Redis is small and close.** Presence, two budgets and the notices between instances; under 5 ms at p95 throughout.
- **The media provider is somebody else's.** Calls are measured as the control plane sees them, against a provider that answers; media quality and its capacity are the provider's ([`media.md`](media.md)).

## What bounds an instance before its processors

These are ceilings in the code, each refused with a clear answer rather than degraded into, and each counted per instance — so another instance raises them:

| Bound | Per instance | When it is reached |
| --- | --- | --- |
| Applications' event streams | 1 024 (`events.MaxStreams`) | a new stream is refused `429` with `Retry-After` |
| People's event streams | 4 096 (`events.MaxPersonStreams`) | the same |
| Database connections | 40 (`CONVIA_DATABASE_MAX_CONNECTIONS`) | queries wait their turn, for as long as their request lasts |
| Passwords hashed at once | 4 | a sign-in or registration waits briefly, then is answered `503` with `Retry-After` |

**The hashing bound is what limits registration**: about ten people a second per instance, with both processors full ([`load.md`](load.md#the-baseline)). An import of many accounts at once, or a launch, is the case that meets it first, and it is answered with `503` rather than with an instance out of memory.

## When to add an instance

Watch these at the busiest hour of the week. The thresholds leave the headroom a target needs, so these are warnings, not alarms. **At the hosted service's full size, one instance already shows the third**: its processors are at 44% and its streams well inside their ceilings, but its queries have begun to wait for a connection — which is why that size is stated for several instances.

| Signal | Threshold | What it means |
| --- | --- | --- |
| The instance's processor, from the platform | above 60% of its processors for fifteen minutes | the load is growing toward the one limit that is not a ceiling. **Add an instance.** |
| `convia.event.streams.active` | above 75% of a ceiling above: 768 applications', 3 072 people's | the next streams will be refused. **Add an instance.** |
| `convia.database.pool.waits` | above zero, steadily | queries are queueing for a connection: one instance at the full size waited 15 times a second, with no latency to show for it yet. Add an instance if its processors are busy too; raise `CONVIA_DATABASE_MAX_CONNECTIONS` if they are not and PostgreSQL has room. If PostgreSQL is the one that is busy, another instance makes it worse. |
| PostgreSQL connections | instances × `CONVIA_DATABASE_MAX_CONNECTIONS` above 80% of its `max_connections` | the next instance will not get its connections. Raise `max_connections`, or put a pooler in front, **before** adding one. |
| `http.server.request.duration`, p95 | over the ceilings in [`ceilings.json`](../cmd/convia-load/ceilings.json) | something is slower than it was measured at, whatever the processors say. Find out what before adding anything: an instance hides a regression, it does not fix one. |
| `passwords are being verified faster than they can be hashed` in the log | more than occasionally | sign-in and registration are being turned away. Add an instance if it is load; look at the addresses if it is one of them. |

**Adding an instance needs nothing but Redis being configured** on every instance ([`scaling.md`](scaling.md)): the load balancer spreads requests and streams, and each new instance takes its share of both. Removing one closes its streams with "this instance is shutting down", and their clients reconnect elsewhere and resume from the journal.

## When one PostgreSQL is the limit

Not at this size: the run's database answered every query in well under a millisecond on average, and the busiest statement took 9 seconds of PostgreSQL's time in five minutes ([`load.md`](load.md#the-database-under-the-load)). The signs that it is becoming one are `convia.database.pool.wait.duration` rising on every instance at once while their processors are not busy, and the statements in `pg_stat_statements` taking longer at the same rate. What to do then is a database decision — a larger server, read replicas, or partitioning the journal and the messages — and none of it is something an instance can be added to fix.

## How this stays true

The nightly [load workflow](../.github/workflows/load.yml) runs the hosted service's size against the ceilings, and against Redis and the media provider in trouble (`M25-012`). A change that makes Convia slower fails it the next morning; a change that makes this page wrong is a change to `ceilings.json` and to the numbers above, made together.
