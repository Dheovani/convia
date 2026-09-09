# ADR 0002 — Speaking LiveKit's HTTP API rather than linking its Go SDK

**Status:** Accepted
**Date:** 2026-09-09
**Milestone:** M12 — LiveKit Adapter

## Context

[ADR 0001](0001-control-plane-media-plane-boundary.md) established the boundary and left the implementation for M12. `M12-001` asks to *pin a supported LiveKit server and Go SDK version*. This ADR records that the server version was pinned and the Go SDK was not adopted, and why.

Convia's control plane asks the media plane for exactly two things, and ADR 0001 explains at length why it is only two:

- create the room a call happens in;
- delete it when the call ends.

Both are one `POST` carrying JSON to LiveKit's room service, authorized by a short-lived HMAC-signed token. That is the entire surface Convia needs today. `M13` adds a third — a token a participant connects with — which is the same signing code and no new transport.

## Decision

**Speak LiveKit's documented room service HTTP API directly, from `net/http`.** Mint the tokens with `github.com/golang-jwt/jwt/v5`. Pin the server to `livekit/livekit-server:v1.13.6` in `docker-compose.yml` and in CI.

### Why not the SDK

Two packages were considered, and the dependency footprint of each was measured rather than guessed:

| | Modules added | What comes with it |
| --- | --- | --- |
| `github.com/livekit/server-sdk-go/v2` | more than `protocol` | everything below, plus a Go *client* for connecting to rooms |
| `github.com/livekit/protocol` | **161** | the complete `pion` WebRTC stack, NATS, Redis, Prometheus, OpenTelemetry, `zap` |
| `github.com/golang-jwt/jwt/v5` | **1** | nothing; it has no dependencies of its own |

Convia's whole module graph is around fifteen modules today. The measurement is not the argument on its own — a big dependency that carries its weight is fine — but of those 161, Convia would use the token signer and two generated request types.

The decisive part is *what* comes with it. `AGENTS.md` says plainly: **do not implement raw media transport in the Go control-plane service**, and lists *custom WebRTC implementations* among the things not to introduce. Linking a complete WebRTC implementation, an SFU client, and a media-server infrastructure kit into the control-plane binary is not a violation of that rule in letter, since none of it would run — but it makes the binary a thing that *can* transport media, which is the property the rule exists to prevent. It also widens the supply-chain surface of a service that authenticates every request it serves.

`AGENTS.md` also says to prefer the standard library where it gives a clear and maintainable solution, and to accept dependencies that *materially* improve correctness or interoperability. Two JSON `POST`s do not clear that bar. The token does, which is why one dependency was added rather than none.

### Why `golang-jwt` and not hand-rolled signing

Signing HS256 is ten lines of `crypto/hmac`, so this was a real choice:

- it is the library LiveKit itself signs and verifies these tokens with, so the claim encoding is guaranteed to match rather than inferred from a document;
- it costs nothing transitively;
- the webhook verifier this milestone defers will parse **attacker-supplied** tokens, where hand-rolling is a well-known way to accept a forgery. Adding the library at the point where only signing is needed means it is already there, reviewed, when verification arrives.

The signing method is pinned to HS256 at the call site rather than read from anywhere, and the test parser pins it too.

### What is given up

- **Protocol drift.** If LiveKit changes the room service wire format, the SDK would have absorbed it and this adapter will not. The mitigation is `internal/media/livekit/integration_test.go`, which runs against a real pinned server in CI and fails on exactly that.
- **Breadth.** The SDK covers the whole server API. This adapter covers two calls, and a third operation is a small amount of new code rather than a method that already exists.

Neither is free, and both are bounded by the same thing that makes the decision safe: everything here is inside one package that nothing else imports.

## Consequences

**The tests changed shape.** Because the wire format is Convia's responsibility now, the unit tests assert what Convia *sends* — path, method, body fields, the exact grant in the token — and not only what it does with the answer. An integration test against a real server is no longer optional coverage; it is what confirms the format.

That paid for itself immediately. The obvious reading of LiveKit's permission model is that `roomAdmin` scoped to a room authorizes deleting it. It does not: `roomAdmin` governs acting *inside* a room, `DeleteRoom` requires `roomCreate`, and a token carrying only `roomAdmin` is answered `401`. Nothing but a real server would have said so, and an SDK would have hidden the question rather than answered it.

**One permission is coarser than intended.** `roomCreate` is not scoped to a room, so the token that deletes one room could create another. That is the provider's model, not a choice; what bounds it is the one-minute token lifetime and the fact that the token never leaves the request it was signed for.

**Revisiting this.** The SDK becomes the better trade if Convia ever needs a substantial part of the server API — egress, ingest, SIP — or if the room service format proves unstable in a way the integration test catches repeatedly. Neither is true today. Nothing outside `internal/media/livekit` would change if it were: that is what ADR 0001 bought, and `internal/media/boundary_test.go` keeps it true.
