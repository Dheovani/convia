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

### The label is the route, never the path

This is the part worth getting right, and it is about the bill as much as the graph.

`/v1/rooms/{room_id}` is **one** series. `/v1/rooms/rom_ABC` is one series **per room** — an unbounded number, chosen by whoever can send a request. A metrics backend does not refuse that; it accepts it until it falls over, and the bill arrives either way.

So the label comes from the router's matched pattern, which means the set of values is the route table. Three things collapse into a single `other`:

- a request that **matched no route**, which anybody can send without a credential;
- the **catch-all** the interface is served from, which is not a route in the sense this label means;
- a **method Convia does not serve**, since the method is whatever bytes a client put on the request line.

The measuring middleware sits outside the router, so this only works because the matched pattern survives back out of it. That is a property of `net/http` rather than of Convia, and there is a test for it: if it stopped being true, every series would silently collapse into `other` and the graphs would still draw.

## What is never written

The rule is older than this milestone and is enforced by types rather than by care:

- **Nothing anybody said.** An audit line records that somebody wrote in a room, which room, and when. A test asserts the body never reaches an event or a log. See [`messages.md`](messages.md).
- **No credential, in any form.** Every secret type renders as `[redacted]` to `fmt` and to `slog`, with compile-time assertions that make dropping one a build error rather than a silent leak. See [`threat-model.md`](threat-model.md).
- **No external subject or display name in audit records**, so the trail stays useful without accumulating personal data in a place that is shipped and retained.

## Not built

- **Traces** (`M22-003`, `M22-004`, `M22-005`). No span is created and nothing is exported. Metrics came first because several other milestones are waiting on them and nothing is waiting on traces.
- **The other domains.** The Redis pool (`M16-010`) and presence (`M17-011`) are still unmeasured.
- **Dashboards, SLOs and alerts** (`M22-011` to `M22-013`), which need more than the HTTP surface first.
- **Telemetry retention and sampling** (`M22-014`).
