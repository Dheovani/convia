# 0020 — The rendezvous service lives beside the application

## Status

Accepted

## Date

2026-10-14

## Context

The project operates a rendezvous service, and anybody may run their own ([ADR 0005](0005-peers-connect-over-webrtc-with-auxiliary-rendezvous-and-turn.md)). The service and the application speak one protocol, whose schemas and canonical encodings must be identical on both sides ([ADR 0008](0008-the-protocol-between-peers.md), [ADR 0009](0009-the-cryptography.md)).

## Decision

**The rendezvous service's code lives in this repository and this Go module**, as the command `cmd/rendezvous`. It uses the same generated schemas and the same canonical encodings as the application, so the two cannot drift apart.

**They are compatible through the protocol's version** ([ADR 0019](0019-the-protocol-version-is-a-whole-number.md)), not through matching releases: the service states the versions it speaks, as a peer does, and a peer uses the highest both speak. Both are released under the same tags.

## Consequences

- One change can alter the protocol, the application and the service together, and the checks of [ADR 0002](0002-what-a-change-must-pass.md) cover all three.
- The service runs where it is deployed, not only on Windows, so it is also built for Linux; nothing in it depends on Windows.
- A person running their own service builds it from this repository, under its license.
