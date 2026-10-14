# 0022 — The rendezvous service logs nobody

## Status

Accepted

## Date

2026-10-14

## Context

The rendezvous service sees who is reachable, which identities try to reach which, and the addresses they connect from ([ADR 0005](0005-peers-connect-over-webrtc-with-auxiliary-rendezvous-and-turn.md)). A log of any of it would outlive the ephemeral state the service is allowed to keep, and would expose the people in it to whoever reads the log — including through blocks provoked on purpose to make the service record somebody.

## Decision

**The service never logs an identity, an address, a reachability identifier, or who tried to reach whom.** It logs only when it starts and stops, its own errors, and counters added up over all peers — connections, messages relayed, refusals, blocks — written once an hour. Its log is kept at most seven days and 10 MB, as the application's is ([ADR 0016](0016-how-convia-runs-on-windows.md)).

## Consequences

- Nothing the service writes down can say who uses it, who they talk to, or from where; an operator compelled to hand over the log hands over counts.
- Investigating one peer's abuse is impossible from the log; the limits of [ADR 0021](0021-the-rendezvous-service-limits-every-peer.md) answer abuse without it.
- Errors from the libraries the service uses can carry addresses, so the service never passes them on verbatim.
