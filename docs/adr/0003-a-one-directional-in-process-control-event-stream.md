# ADR 0003 — A one-directional, in-process control-event stream

**Status:** Accepted
**Date:** 2026-09-10
**Milestone:** M14 — Real-Time Control Events

## Context

`AGENTS.md` lists "WebSocket where appropriate" as a technology direction and M14 asks Convia to use one *only for control-plane events that materially require low-latency delivery*. Three decisions had to be made before any of that could be written, and each of them is easier to make now than to reverse later: what a stream carries, which way it flows, and where the events come from.

M15 already owns webhooks, and its goal names its audience: *notify server-side consumers*. So this milestone is not "how does an application learn things" in general. It is the narrower question of what a webhook serves badly — something that needs to arrive in the same second, and something a consumer without a public HTTPS endpoint can still receive.

## Decision

### One direction

**The stream carries events to the client and accepts nothing from it.** A client that sends any message — text or binary — has its connection closed with `1003 unsupported data`.

The subscription is settled during the handshake, from the credential's scopes, and cannot be changed afterwards. There is no subscribe message, no filter message, and no acknowledgement.

This is what makes `M14-010` — *prevent WebSocket use for ordinary audio or video payloads* — a property of the design rather than a rule somebody has to keep following. Convia never interprets a client message, so there is no message that could carry media, and no protocol extension that would quietly become the place to put it. The same choice removes a whole category of security question: nothing a client sends can widen what it receives, because nothing it sends is read.

The cost is real and small. A client that wants a narrower stream than its credential permits filters on arrival, and a client that wants a broader one is asking for a scope it was not granted.

### In-process, holding nothing

**The broker is a fan-out over the streams open on this instance.** An event goes to the subscribers connected at the moment it is published, and is then gone. Nothing is stored, nothing is replayed, and there is no cursor.

`M14-006` — reconnect, resume cursor, and missed-event behavior — is therefore only half answered here, deliberately. A resume cursor needs a durable, ordered log of events with a retention policy, which is a table, a migration, a write on every domain operation, and a decision about how long Convia keeps a record of who was in which conversation. That is a larger commitment than this milestone should make on the way past, and `M16` (Redis) and `M15` (webhooks) will both have opinions about its shape.

What is answered is the part a client cannot work around on its own: **a subscriber is always told when its view might be incomplete.** Falling behind closes the stream with a distinct status rather than silently dropping an event, so a client never believes it saw everything when it did not. Reconnecting plus re-reading over REST is the documented recovery, and it is honest about being one.

### Publishing cannot fail

**`Publish` takes no context and returns no error.** A domain service announces what happened and carries on.

This is the property that makes it safe to call from inside `Start`, `Join`, and `Remove`. If announcing could block, a subscriber that stopped reading would slow down the API for everybody; if it could fail, every domain operation would grow a decision about what to do when the announcement did not go out. Instead a subscriber that cannot keep up loses its stream, which costs that subscriber a reconnection and costs the request nothing.

### Announcing where Convia already records

Events are published from the `audit` method of each domain, next to the structured log entry for the same occurrence. The audit vocabulary already existed — `call.started`, `participant.removed` — and the event types are those same strings.

The alternative was a separate publication call at each site, and the reason not to is that an event and its audit entry describe one thing. Two call sites are two places to be right, and the failure mode is quiet: a stream and an audit trail that disagree about a conversation, discovered during an incident.

### A dependency, and which one

**`github.com/coder/websocket`**, ISC-licensed, with **zero dependencies of its own**.

`AGENTS.md` says to prefer the standard library, and there is no WebSocket in it. Implementing RFC 6455 by hand means frame parsing, masking, fragmentation, control-frame interleaving, and UTF-8 validation of close reasons — all of it attacker-facing, none of it Convia's problem to solve. This is the case the same document describes as a dependency that *materially improves correctness*.

`github.com/gorilla/websocket` was the alternative and is equally well maintained and equally free of dependencies. The tie was broken on API: `coder/websocket` is context-first, which matches how the rest of Convia bounds work, and it follows the `Unwrap` method when looking for `http.Hijacker`, so it upgrades cleanly through the access-log middleware that wraps every route.

## Consequences

- **An event stream is not a record.** Anything a client must not miss belongs in a REST read or, when M15 arrives, a webhook with a delivery attempt behind it. The documentation says so plainly rather than leaving a client to discover it.
- **Running more than one instance changes the guarantees.** A subscriber connected to instance A never sees an event produced on instance B. Until that is solved, a deployment either runs one instance or routes each tenant's streams to a fixed one. `docs/events.md` states this as an operational requirement, and it is the first concrete case for Redis pub/sub that `M16-001` asks for before the dependency is added.
- **Shutdown had to grow a step.** A hijacked connection is invisible to `http.Server.Shutdown`, so the composition root stops the broker first, which tells every subscriber why and waits for them within the shutdown deadline.
- **The contract is split, and only in one place.** OpenAPI 3.0.3 cannot describe a stream, but it describes the exchange that opens one and the shape of what travels on it, so `api/openapi.yaml` still holds the endpoint, its refusals, the `Event` schema, and the event-type vocabulary. Only the delivery rules and close codes live in `docs/events.md`, and a test checks that a real event validates against the published schema.
- **Metrics wait.** `M14-013` asks for active connections, delivery latency, and dropped events. Convia has no metrics pipeline yet and `AGENTS.md` says not to add an observability stack before there is something worth observing. Every stream logs a summary when it closes — how many events it carried and why it ended — which is what an operator needs today and is the data those metrics will be derived from.
