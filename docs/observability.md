# Observability

This is `M22` — **what a Convia installation says about itself while it is running**, and what somebody can do with that at three in the morning.

It is written against what exists. Tracing and metrics are named here where they belong and marked as not built, rather than described as though they were.

## Every line says where it came from

Four attributes are on every log line, attached to the logger rather than passed at call sites:

| Attribute | What it answers |
| --- | --- |
| `service.name` | always `convia` |
| `service.version` | which build |
| `deployment.environment` | `development` or `production` |
| `service.instance.id` | **which process** |

They are the OpenTelemetry resource attributes by name, so that logs and the traces `M22-003` will add describe the same thing in the same words rather than in two vocabularies somebody has to join by hand.

**The version comes from the build, not from a flag.** Go embeds the revision a binary was built from, so `go build`, the Dockerfile and a contributor's laptop all produce a binary that knows its own commit without anybody remembering a linker flag. A working tree with uncommitted changes is marked `-modified`, because "which commit" is a lie when the answer is "that commit and some edits". A binary built without version control information — which is what `go test` produces — says `unknown`, which is honest and is why nothing depends on this beyond describing a process.

**`service.instance.id` is the one that matters later.** It defaults to the hostname, which is right in a container. It is useless while an installation is one process and impossible to add retrospectively to logs already written, which is why it is here before anybody needs it. **Set `CONVIA_SERVICE_INSTANCE` when running two on one host**, or both answer to the same name.

## Every line written while serving a request carries it

```
CONVIA_LOG_LEVEL=info
```

`request_id` is added by the log handler from the request context. It used to be written by hand: **162 lines across 47 files named it, and 175 other warnings and errors carried nothing at all.** Correlation that each call site has to remember is correlation missing exactly where somebody was in a hurry, which is the same place the interesting failures are.

A line written by a background janitor carries no `request_id`, because there is no request. Absent is the answer; an empty field would be something somebody could try to correlate by.

**This is why call sites say `ErrorContext(ctx, …)` rather than `Error(…)`.** `slog` hands the handler a background context for the plain form, and there is nothing in it. That is the whole cost, and it was paid once across about 150 calls.

## The level

`debug`, `info`, `warn` or `error`, defaulting to `info`. **A value that is none of those stops startup** rather than falling back — somebody who wrote `verbose` meant something, and ignoring it silently answers the next incident with fewer lines than whoever configured it believes they have.

**Debug is not a setting to leave on.** Those lines name identifiers, addresses, and the shape of what somebody is doing, which is the class [`data-protection.md`](data-protection.md) calls the social graph and warns is frequently more revealing than any single message. They exist to answer a question during an incident and to be turned off afterwards.

## Metrics

```
CONVIA_METRICS_ENDPOINT=https://collector.internal:4318
```

**Empty is off, and off is a real no-op** — no collection goroutine, no accumulation, no periodic flush. That is what makes it safe for the instruments to be called on every request unconditionally, and it is why a laptop and a test run pay nothing for telemetry they never asked for.

The variable is named `CONVIA_` like everything else rather than reusing OpenTelemetry's own, because a deployment that sets the standard one is usually setting it for several processes at once, and Convia picking it up would be Convia joining a decision nobody made about it.

Two instruments, which is fewer than it looks:

| Instrument | What it answers |
| --- | --- |
| `http.server.request.duration` | how long, how many, and how many failed |
| `http.server.active_requests` | whether Convia is keeping up |

`M22-006` asks for request, error and latency separately. **A histogram carries its own count**, so all three are the one instrument cut by status — and there is no counter beside it that can disagree about the total. The second is saturation, which is a different question: not whether Convia is fast, but whether it is behind.

The gauge carries **no labels at all**. Saturation is a property of the process; cutting it by route makes a series per route that is almost always zero, and the sum is the only number anybody reads.

### What Convia is carrying

| Instrument | What it answers |
| --- | --- |
| `convia.calls.active` | how many calls are happening |
| `convia.participants.active` | how many people are in one |

**Both are asked of the database when somebody collects, not counted as calls start and end.** A number kept in memory starts at zero when a process starts, so an instance that restarts while ten calls are running would report zero — a graph showing an outage that did not happen. Asking gives the answer that is true whoever asks and however long they have been up.

**Neither carries a label**, which is `M22-007` in its strongest form: there is no user, no room, no call and no tenant in either series. Anybody asking *which* room is asking a question the API answers, not one a time series should. The numbers cross applications on purpose — what they mean is "what is this installation carrying", which belongs to the installation rather than to any tenant on it.

They are read together, in one callback, because an operator compares them: people per call is what says whether an installation is holding a few large calls or many small ones. Reporting one from this moment and the other from the last would make that ratio a number that was never true.

**Not knowing is not zero.** A count that fails is logged and reported as no measurement, because zero is a claim that nothing is happening and a database that could not be reached is not that claim.

### The event streams

| Instrument | What it answers |
| --- | --- |
| `convia.event.streams.active` | how many streams this instance is serving |
| `convia.event.delivery.duration` | how long an event took to reach them |
| `convia.event.streams.ended` | how many finished, and **why** |

**The ending counter is the one worth watching.** `behind` means somebody's view of a conversation had a hole in it — and it is invisible in every other signal, because the subscriber reconnects, fills the gap from the journal, and everything looks healthy again. `reader` and `shutdown` are ordinary.

`M14-013` asked for "dropped events". **Convia does not drop events and carry on**: a subscriber that falls behind is *ended*, with a reason saying its view is now incomplete, so that reconnecting with its last cursor fills the gap. The instrument is named for what happens rather than for what was asked, because counting "dropped events" would describe a design Convia deliberately does not have.

**Delivery is timed from when the change happened**, not from when the event was published: the interesting delay is the transaction that had to commit first, not the microseconds spent fanning out afterwards. Events arriving from another instance are **not** timed — they were stamped by that instance's clock, and a latency computed across two clocks measures the skew between them at least as much as it measures Convia. A negative reading is discarded rather than recorded, because a delivery that arrived before the thing it describes happened is worse than a gap: somebody would believe it.

### Presence

| Instrument | What it answers |
| --- | --- |
| `convia.presence.claims` | how much presence the deployment holds |
| `convia.presence.overdue` | how much of it has gone stale |

**`overdue` is the one that says something is wrong.** A claim past its deadline changes no answer — every read already ignores it — so it never affects what anybody sees. What it says is whether the sweeper is keeping up, and a number that climbs means subscribers are not being told that people went away.

The two are read together, from one moment, because overdue on its own is a number without a scale: a hundred stale claims mean nothing until somebody knows whether the deployment holds a hundred and one or a million.

**They count claims, not people.** `M17-011` asked for active users; a person with a phone and a laptop holds two claims, and counting distinct people would mean either walking every claim on a schedule or keeping a second tally that can disagree with the first. A number named for what it counts is worth more than one named for what was asked.

**Neither carries a label**, which matters most here: presence is the signal with the most people in it, so a series per person would be the largest cardinality mistake available in Convia.

Where the count comes from follows where presence lives. One instance walks its own memory; several read the sorted set of deadlines that the sweeper already uses, with a cardinality and a range count — constant work rather than a scan of a keyspace that grows with the installation.

### The shared store

| Instrument | What it answers |
| --- | --- |
| `convia.redis.command.duration` | how long a command took, and whether it worked |
| `convia.redis.pool.connections` | how many connections are idle and in use |
| `convia.redis.pool.waits` | how often a caller waited for one, and how often waiting timed out |

**These answer one question: is it Convia, or is it Redis.** Without them the answer is a guess, because every symptom appears somewhere else — a pool with no free connections looks like a slow handler, and a Redis that times out looks like presence being wrong.

`pool.waits` with the `timed_out` outcome is the one worth alerting on. It is saturation: Convia asked for a connection and there was none.

**Both uses are measured through one set of instruments**, labelled `presence` and `events`. They point at the same Redis, so the address cannot say which is slow; what Convia is doing with it can. The labels are Convia's own words, so the set is as large as the number of places Convia connects — two.

**A missing key is an answer, not a failure.** `redis.Nil` is how Redis says a key is not there, and counting it as an error would put the error rate near a hundred percent while nothing was wrong — presence is mostly people who are not present.

A pipeline is timed once, as `pipeline`. Its members were sent together and waited together, so the only honest duration is the round trip they shared. A dial is not timed at all: connection setup in the same histogram as the work it enables would make both unreadable.

### The label is the route, never the path

This is the part worth getting right, and it is about the bill as much as the graph.

`/v1/rooms/{room_id}` is **one** series. `/v1/rooms/rom_ABC` is one series **per room** — an unbounded number, chosen by whoever can send a request. A metrics backend does not refuse that; it accepts it until it falls over, and the bill arrives either way.

So the label comes from the router's matched pattern, which means the set of values is the route table. Three things collapse into a single `other`:

- a request that **matched no route**, which anybody can send without a credential;
- the **catch-all** the interface is served from, which is not a route in the sense this label means;
- a **method Convia does not serve**, since the method is whatever bytes a client put on the request line.

The measuring middleware sits outside the router, so this only works because the matched pattern survives back out of it. That is a property of `net/http` rather than of Convia, and there is a test for it: if it stopped being true, every series would silently collapse into `other` and the graphs would still draw.

## Traces

```
CONVIA_TRACES_ENDPOINT=https://collector.internal:4318
```

Separate from the metrics endpoint rather than one setting for both, because they are separate decisions: metrics are cheap and constant and a deployment usually wants them always, while traces are voluminous and are often turned on while somebody is looking at something. One setting would make turning one off turn the other off with it.

**Every request is a span, named for the route and never the path.** Same rule as the metrics label and for the same reason: a span named `/v1/rooms/rom_ABC` is a distinct operation per room, which makes every aggregate across a route empty and every trace search a scan.

**The caller's trace is continued, not replaced.** An application that traced its own call into Convia gets one trace across both, and Convia's work appears underneath the operation that asked for it rather than as an unrelated trace nobody can join. The propagation is W3C `traceparent` and **nothing else** — no vendor headers, because a second format is a second thing that can disagree about which trace a request belongs to.

**Baggage is deliberately not propagated.** It carries arbitrary key-value pairs to every service a request touches and, with a careless exporter, into their telemetry — a channel for personal data to leave the boundary [`data-protection.md`](data-protection.md) draws, opened by default and closed by nobody.

**Only a server fault marks a span as an error.** A `404` or a `401` is Convia answering correctly; marking those would make the error rate of every trace search the rate at which people mistype URLs and let sessions expire.

### What a span says about the database

Every query is a span underneath the request that made it, named `postgresql select` and so on — the first word, because the name is what somebody scans a trace by, and the whole statement as a name would be a heading per query.

**The statement is recorded and the arguments are not.** This is the load-bearing distinction. Convia builds no SQL from caller data — nothing is interpolated, and the only string building in a statement is placeholder numbering — so the text is written by this repository and is safe in telemetry, and it is how somebody finds which query was slow. The **arguments** are the opposite: they are the message somebody wrote, the name they chose, the token they presented. A tracer that recorded them would ship every one of those to a collector.

**No rows found is not a failure.** `pgx.ErrNoRows` is how a query says the thing is not there, which Convia acts on constantly — a resolve that creates, a lookup that reports missing. Marking it would make the error rate of every trace the rate at which people ask about things that do not exist.

### What a span says about Redis

The command name, and nothing else. A key here is `convia:presence:v1:<application>:<user>` — **it names a person**, and putting one in a span would ship the social graph to a collector one span at a time. Argument values are worse and equally absent.

Measuring without tracing is a supported configuration rather than half of one, for the reason the two endpoints are separate.

### What Convia sends, and what it puts on it

A webhook delivery and a call to the media plane are spans too, so a slow trace says which of them was slow rather than that something was.

**Whether the trace is carried outward is a separate decision from whether it is recorded**, and Convia answers it differently by destination:

| Destination | Chosen by | Carries `traceparent` |
| --- | --- | --- |
| A webhook endpoint | a tenant the operator admitted | **yes** |
| The media plane | operator configuration | **yes** |
| Another installation | anybody who can send an invitation | **no** |

Carrying it outward is the point of propagating at all: an application receiving a webhook can join its own trace to the operation that caused it, and that is the application's own data.

**A home on another installation is different.** [`threat-model.md`](threat-model.md) says a valid signature proves who is asking and nothing about whether they should be served. Handing one a trace identifier would let it correlate several of Convia's requests as a single operation — a small channel, and one Convia gets nothing back for.

**The span is named for the operation, never the address.** A webhook destination is a tenant's choice and a home is anybody's, so a span named for the address would be a span name per destination — an unbounded set decided outside Convia. The query string would be worse: it is whatever the tenant put in the URL.

**An outbound `4xx` marks its span, unlike an inbound one.** Inbound, a `404` is Convia answering correctly; outbound, it is the thing Convia asked for not happening.

### Both identifiers, and why

A log line written while serving carries `request_id` **and** `trace_id`. They are not redundant:

- `request_id` is Convia's own and is in the answer a client received, so somebody holding a failed response can find its lines.
- `trace_id` is the caller's and spans every service the request touched, so somebody holding a slow trace can find what Convia was doing inside it.

Either alone leaves one of those searches impossible.

## Sampling

```
CONVIA_TRACE_SAMPLE=1
```

One is every trace, and it is the default: **sampling that throws away the trace somebody needed is worse than a collector that costs a little more**, and an installation this is built for produces few enough traces for that to be true. Lower it when it stops being.

**A caller's decision is honoured whatever this says.** If an application already decided to record an operation, Convia records its part — otherwise a sampled trace arrives at whoever reads it with Convia missing from the middle, which is worse than not sampling at all because it looks like Convia did nothing. The reverse holds too: a caller that decided against recording does not get Convia's spans arriving on their own.

The consequence is worth stating plainly: **a caller that samples everything makes Convia trace everything it asks for.** That is a lever somebody outside holds, and what bounds it is the one that already bounds them — [`authentication.md`](authentication.md)'s per-tenant request budget caps how much they can ask for at all.

A value outside zero to one stops startup rather than being clamped. Somebody who wrote `50` meant half and would otherwise get every trace.

## Retention

**Convia keeps none of this.** Logs go to standard output and telemetry goes to a collector, so how long any of it lives is the deployment's decision — made in the collector, not here. What this section does is say what that decision is about.

| Signal | What it carries | Suggested |
| --- | --- | --- |
| Metrics | no identifiers at all | as long as you like |
| Traces | routes, SQL statements, identifiers | **30 days** |
| Logs | identifiers, addresses, what happened | **30 days**, and less for `debug` |

**Metrics are the safe one and it is not an accident.** Every instrument here is unlabelled or labelled with Convia's own words — no user, no room, no call, no tenant — so a metrics store is a set of numbers about an installation and nothing about anybody in it. It is the one signal that can be kept forever without becoming a record of people.

**Traces and logs are the class [`data-protection.md`](data-protection.md) calls the social graph.** They do not carry conversations, credentials or arguments — that is enforced, not hoped for, and the section below says how — but they do carry who acted, when, and how often. Thirty days is the same window a deleted person waits before Convia forgets them, which makes the two rules one rule rather than two numbers somebody has to reconcile.

**A debug line is a shorter-lived thing than the level suggests.** Those name identifiers and the shape of what somebody is doing; they exist to answer a question during an incident. A deployment that leaves `debug` on and retains for thirty days has built a detailed record of its people by accident.

## What Convia promises

Three objectives, and each is a statement about what somebody experiences rather than about a machine.

| Objective | Measured as | Target |
| --- | --- | --- |
| **Availability** | share of requests answered without a `5xx` | **99.9%** over 30 days |
| **Latency** | 95th percentile of `http.server.request.duration` | **under 300 ms** |
| **Joining a call** | 95th percentile of the three join routes below | **under 1 s** |

**Joining a call is separated out because it is the one a person feels.** Everything else in Convia happens while somebody is looking at a screen that is already drawn; joining is somebody waiting to be in a conversation, and a second is roughly where waiting becomes noticing. The three routes are `POST /v1/participants/{participant_id}/session`, `POST /v1/me/rooms/{room_id}/call/join` and `POST /v1/peer/rooms/{room_id}/call/join` — a tenant's backend admitting somebody, a person joining here, and a visitor joining from elsewhere.

**A `4xx` is not unavailability.** A refused credential, a room somebody is not in, a rate limit: those are Convia working. Counting them would make the objective measure how often clients are wrong, which is not a promise Convia can keep or break.

**These are targets, not measurements.** No installation has run long enough to say what Convia actually does, and the numbers above are what it should aim at rather than what it has shown. The first deployment to run for a month replaces them with what it saw.

## What should wake somebody

Each condition is written over the instruments named above, so it can be expressed in whatever the deployment's backend speaks. Each points at what to do, because an alert without one is a notification.

| Condition | Why it matters | Then |
| --- | --- | --- |
| `5xx` share above 1% for 5 minutes | the availability objective is being spent | [`runbooks/failing-requests.md`](runbooks/failing-requests.md) |
| `convia.event.streams.ended{reason="behind"}` above zero | **somebody's view of a conversation had a hole in it** | [`runbooks/subscribers-falling-behind.md`](runbooks/subscribers-falling-behind.md) |
| `convia.redis.pool.waits{outcome="timed_out"}` rising | Convia is waiting for connections it cannot get | [`runbooks/failing-requests.md`](runbooks/failing-requests.md) |
| `convia.presence.overdue` climbing over 15 minutes | the sweeper is not keeping up, so people appear present after they left | [`runbooks/presence-not-lapsing.md`](runbooks/presence-not-lapsing.md) |
| join latency above 1 s for 10 minutes | people are waiting to get into calls | [`runbooks/failing-requests.md`](runbooks/failing-requests.md) |

**The second one is the alert worth having.** Every other condition shows up somewhere a person would eventually notice; a subscriber that fell behind reconnects, fills the gap from the journal, and leaves every other signal looking healthy. It is the only place that says it happened.

**Nothing alerts on the rate limits.** A `429` is Convia working as designed, and paging somebody because an integration is noisy would train them to ignore the pager. They belong on a dashboard, where a rise is a question rather than an emergency.

## What to put on a screen

A dashboard is written in whatever the deployment runs, so what is durable is which panels and from which instruments. Four rows, in the order somebody reads them during an incident:

1. **Is it up?** `5xx` share and request rate, from `http.server.request.duration` cut by status. Then `http.server.active_requests`, which says whether Convia is behind rather than slow.
2. **Is it fast?** The 95th percentile overall, and the same cut by `http.route` — that cut is the whole reason the label is a route and never a path.
3. **What is it carrying?** `convia.calls.active`, `convia.participants.active`, `convia.event.streams.active`, and `convia.presence.claims`. These are the numbers that make everything else legible: a latency rise means one thing at ten calls and another at a thousand.
4. **What is underneath?** `convia.redis.command.duration` and `convia.redis.pool.waits`, cut by pool, beside the ending counter cut by reason. This row answers "is it Convia or is it Redis" without anybody guessing.

**Rate limits go on the first row and not in the alerting.** A rise in `429`s is worth seeing next to the error rate, because it is the difference between Convia failing and Convia refusing.

## What is never written

The rule is older than this milestone and is enforced rather than remembered:

- **Nothing anybody said.** An audit line records that somebody wrote in a room, which room, and when. A test asserts the body never reaches an event or a log. See [`messages.md`](messages.md).
- **No credential, in any form.** Every secret type renders as `[redacted]` to `fmt` and to `slog`, with compile-time assertions that make dropping one a build error rather than a silent leak. See [`threat-model.md`](threat-model.md).
- **No external subject or display name in audit records**, so the trail stays useful without accumulating personal data in a place that is shipped and retained.
- **No query arguments in a database span**, and no keys in a Redis one.
- **No identifiers in any metric**, because every instrument is unlabelled or labelled with Convia's own words.

### And one test that would notice a new one

Each of those is enforced somewhere different, and until `M22-009` **nothing would have noticed a new instrument or a new span attribute carrying one of them**.

Now something does. A test drives a real request through the real chain with all three signals attached — a log buffer, a metric reader, a span recorder — with a credential, a conversation and somebody's identifier planted in it, and asserts that none of the three appears anywhere in what a collector would receive. It runs the failure paths too, because those log the most and are written in the most hurry.

The `Authorization` header has a test of its own. It is the single most dangerous field to instrument: a tracer that recorded request headers would be a reasonable-looking tracer that shipped every application key in the deployment.

## Not built

- **Sampling** (`M22-014`). Every request is traced. That is right while an installation is small and wrong as soon as one is not, and the setting to change it does not exist.
- **The database.** PostgreSQL's pool and query latency are not measured; Redis is, and the same argument applies to the store Convia actually depends on.
- **Dashboards, SLOs and alerts** (`M22-011` to `M22-013`), which need more than the HTTP surface first.
- **Telemetry retention and sampling** (`M22-014`).
