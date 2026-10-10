# Load testing

**`scripts/load.sh` puts a disposable Convia under the load [`scaling.md`](scaling.md) sizes it for, and says whether it met the targets.** It is `M25-003` and `M25-004`.

```
scripts/load.sh smoke 30s       # checks the harness, in about a minute
scripts/load.sh provider 5m     # the hosted service's size, in about fifteen
```

It needs Docker and Go, and nothing else running on ports 18100 and 4318. Everything it starts — a PostgreSQL, a Redis, one Convia — is removed when it ends.

## What a run does

1. **Builds Convia for Linux and runs it in a container limited to 2 processors and 4 GB**, the instance the targets are stated for. A run on an unlimited machine would measure the machine.
2. **Builds what it will measure through the API**, and nothing behind Convia's back: tenants and their keys by an operator key, users and rooms with members by each tenant's key, people by registering, and people's rooms by inviting each other by handle and joining with the link — the way strangers do in the product, because a person can only add somebody they already share a room with.
3. **Opens every event stream** — a few per tenant, the way a tenant's backend holds them, and one per person — and then applies the load for as long as it was told: messages posted and read by tenants and by people, people signing in, and calls held open at a steady level.
4. **Reports twice.** Once as the client saw it, with exact percentiles from every sample, each row marked against its target; and once as Convia measured it, over OTLP, while the run went on.

A run fails — exits non-zero — when any target is missed.

## What is measured, and how

**An event reaching a subscriber** is measured end to end: from the moment a message is sent to the moment the `message.posted` naming it arrives on each stream that should hear it. An event can arrive before the request that caused it has returned, because Convia announces as it commits; the generator keeps those arrivals until it learns the message's identifier, and measures them from the send. This is the measurement no request-rate tool makes, and the reason the generator is written here rather than borrowed — k6, the one that could come close, is AGPL, which [`AGENTS.md`](../AGENTS.md) does not allow.

**Convia's own view** comes from pointing `CONVIA_METRICS_ENDPOINT` at the generator, which receives OTLP/HTTP like any collector. The server's latencies are read from `http.server.request.duration`, as an upper bound — "under 25 ms" is what a bucket can honestly claim. Event streams are left out of that list: a stream is a request that lasts as long as somebody listens. What the process cost comes from [the runtime instruments](observability.md#the-process-itself), and the processor from Docker, because the runtime's own estimate of it only moves when the collector runs.

**People arrive from many addresses.** The generator is trusted as a proxy in the disposable Convia and gives each person an address in `198.18.0.0/15`, the range set aside for benchmarking. A thousand people from one address is not what a thousand people look like: every budget keyed by address would see one caller and the run would measure refusals.

**Budgets are raised above what the run asks for**, for the same reason: a run measures what Convia takes to serve, not what it is configured to refuse.

## The baseline

The first run at the hosted service's size, on 2026-10-10, against one instance of 2 processors and 4 GB with PostgreSQL and Redis beside it. It is the number later runs are compared against.

**What it carried:** 10 tenants, 10 000 users, 500 tenant rooms, 960 people in 240 rooms of their own, **1 000 event streams open at once** (40 tenant streams, 960 people's), and for five minutes 100 tenant messages, 200 tenant reads, 50 people's messages, 100 people's reads and 2 sign-ins a second, with 100 calls held open throughout.

| | p95, client | p95, Convia | target |
| --- | --- | --- | --- |
| An event reaching a person's stream | 13 ms | — | 250 ms |
| An event reaching a tenant's stream | 9.4 ms | — | 250 ms |
| Reading a page of messages | 3.7 ms | under 5 ms | 200 ms |
| Posting a message as a tenant | 8.5 ms | under 10 ms | 300 ms |
| Posting a message as a person | 12 ms | under 25 ms | 300 ms |
| Starting a call | 45 ms | under 5 ms | 300 ms |
| Signing in | 188 ms | under 200 ms | 1 s |

**Every target was met, by at least a factor of five** — twenty for events — and no request failed. The difference between the client's figure for starting a call and Convia's is the generator's own queueing behind a hundred calls ending and starting at once, not Convia's.

**What it cost, during the load alone:** about **0.9 of the 2 processors** on average and 1.1 at the peak, 300 MB of memory of the 4 GB, 2 100 goroutines for 1 000 streams, 290 MB a second allocated and two collections a second.

**What it says about the next limit:** the database pool — 40 connections — had a query waiting for a connection **21 times a second** at this load. Nothing was slow because of it yet, and it is the first number to rise as load grows; it is `M25-006`'s subject.

Setting up is a separate cost and a large one: registering the 960 people took 1 minute 40 seconds with both processors full, because each registration derives the password twice with argon2id. **One instance registers about ten people a second**, which is what an account is supposed to cost.

What the run found, before any of the above could be read:

- **Every duration Convia exported was unreadable.** The histograms were in seconds with bucket boundaries chosen for milliseconds, so every request under five seconds shared one bucket. They now have boundaries in seconds, including one at each target.
- **Nothing Convia measured reached a collector.** Metrics and traces were posted to the address configured, path and all, so `https://collector:4318` received them at `/` and answered 404. Each signal now goes to its OTLP path.
- **Memory used counted memory already given back**, reading 627 MB where the container held 145 MB.
- **A client that hangs up is logged as a server error.** The four errors of the run were requests the generator cancelled as it stopped. That is `M25-016`.
