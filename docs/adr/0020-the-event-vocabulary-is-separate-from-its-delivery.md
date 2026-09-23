# ADR 0020 — The event vocabulary is separate from its delivery

**Status:** Accepted
**Date:** 2026-09-21
**Milestones:** M35 — Desktop Application

## Context

Convia's desktop application holds the person's event stream in its Go process, because the session travels in a header and a page cannot set one on a handshake ([ADR 0019](0019-a-session-travels-in-a-cookie-or-a-header.md)). To read what arrives, it imported `internal/events` — the obvious thing to do, and the thing `M35-004` asked for: one vocabulary, not two.

A tripwire written the same day showed what that cost. `internal/events` held two unrelated things:

- **the vocabulary** — what an event is, which events exist, what a cursor is, and the broker that fans them out. It depends on nothing;
- **the delivery** — announcing an event inside the transaction that caused it, deciding who is entitled to receive one, and serving the stream over a WebSocket. Those need `internal/transaction`, `internal/credentials`, `internal/sessions` and `internal/api`.

Because they were one package, the application linked the second to get the first: a **Postgres driver, the accounts domain and argon2 in the binary somebody installs on their laptop**, to read a JSON envelope.

The product owner's framing decided it. Convia is a call service, and the service is the part with a commercial future; the application is the interface an ordinary person opens, and it reaches the service the same way any other client does. Keeping the two binaries' dependencies apart is worth a package boundary.

## Decision

**`internal/events` is the vocabulary, and it imports nothing of Convia's.** `internal/events/serving` is announcing, authorizing and serving, and imports what those need.

The tripwire that used to list four permitted imports for `internal/events` now lists none, and carries the old list for `serving`. An empty allowlist is the strongest form of the rule it was always trying to express: events flow *into* the vocabulary from the domains and never back out.

Four things the handler reached for became part of the `Stream` contract rather than package-private: `ApplicationID`, `Outgoing`, `Replays` and `Replayed`, plus `Broker.SubscribePerson`. `SubscribePerson` was unexported so that a person's stream could only be opened through the authorization beside it; that was a convention rather than a guarantee, since `Subscribe` was already exported, and it is now stated in the doc comment instead.

## Consequences

- **A client can speak Convia's event vocabulary without carrying the service.** The desktop application links the vocabulary, the public error shape, and nothing else of Convia's. The SDK in `M19` inherits this.
- **One definition, not two.** A new event type is added in one place and every client sees it. The alternative considered — the application declaring the shapes it decodes, with a contract test — was rejected for the duplication, although it is what a client outside this repository will have to do.
- **`Sink` moved.** [ADR 0004](0004-queuing-a-durable-delivery-inside-the-request.md) declared it in `internal/events`; it is in `internal/events/serving` now, satisfied by `internal/webhooks` as before.
- **The vocabulary is a leaf that can never stop being one.** Anything that needs a session, a credential or a transaction to decide who hears what now has an obvious place to go, and the test says so before the import lands.
