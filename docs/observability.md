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

## What is never written

The rule is older than this milestone and is enforced by types rather than by care:

- **Nothing anybody said.** An audit line records that somebody wrote in a room, which room, and when. A test asserts the body never reaches an event or a log. See [`messages.md`](messages.md).
- **No credential, in any form.** Every secret type renders as `[redacted]` to `fmt` and to `slog`, with compile-time assertions that make dropping one a build error rather than a silent leak. See [`threat-model.md`](threat-model.md).
- **No external subject or display name in audit records**, so the trail stays useful without accumulating personal data in a place that is shipped and retained.

## Not built

- **Traces** (`M22-003`, `M22-004`, `M22-005`). Nothing is exported and no span is created. The resource attributes above are the half that was worth having first, because they cost nothing and cannot be backfilled.
- **Metrics** (`M22-006`, `M22-007`). Request rate, error rate, latency and saturation are not measured, which is also why `M14-013`, `M16-010` and `M17-011` are still open — they are all the same missing piece. It is the reason `M22-015` cannot yet decide what a signed-in person's request budget should be: the number has to be measured rather than reasoned out.
- **Dashboards, SLOs and alerts** (`M22-011` to `M22-013`), which need the metrics first.
- **Telemetry retention and sampling** (`M22-014`).
