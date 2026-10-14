# The rendezvous protocol

How a peer talks to a rendezvous service ([ADR 0005](../adr/0005-peers-connect-over-webrtc-with-auxiliary-rendezvous-and-turn.md)). The messages are in [`proto/convia/v1/rendezvous.proto`](../../proto/convia/v1/rendezvous.proto); the service is [`cmd/rendezvous`](../../cmd/rendezvous/main.go), and a peer's side of it is [`internal/rendezvous`](../../internal/rendezvous/rendezvous.go).

## Transport

A peer connects to the service's `wss://` address over WebSocket. Every WebSocket message is binary and holds exactly one Protocol Buffers message: `ToService` from the peer, `FromService` from the service. Anything else breaks the protocol.

## Connecting

```text
peer                                   service
 │ ◄──── hello: versions, challenge ────── │
 │ ── authenticate: version, identity, ──► │
 │    signature                            │
 │ ◄────────────── welcome ─────────────── │
```

1. The service sends `hello`: the versions of the protocol it speaks ([ADR 0019](../adr/0019-the-protocol-version-is-a-whole-number.md)) and a fresh 32-byte challenge, valid for this connection only and for 30 seconds.
2. The peer chooses the highest version both speak, and sends `authenticate`: that version, its 32-byte Ed25519 public key, and its signature of the [rendezvous answer](canonical-encodings.md#the-answer-to-a-rendezvous-service) over the address it connected to and the challenge. If the two share no version, the peer closes the connection with code 4002.
3. The service verifies the signature against its own address, and sends `welcome`.

A peer that connects again with the same identity replaces its earlier connection, which is closed with code 4000.

## Signaling

A peer sends `signal` with `peer` set to the identity it wants to reach, and an offer, an answer or an ICE candidate. The service delivers it to that identity with `peer` set to the sender's identity, and changes nothing else. When the identity is not connected, the service answers `unreachable` with it.

## Reachability

Reachability identifiers are opaque, 32 bytes each ([ADR 0012](../adr/0012-only-contacts-can-tell-when-a-person-is-online.md), [ADR 0014](../adr/0014-members-of-a-space-share-a-secret-to-tell-who-is-online.md)).

- `subscribe` asks to be told when an identifier is announced; if another peer already announces it, the service says so at once.
- `announce` tells every other subscriber of an identifier, with `reachable`.
- `unsubscribe` and `withdraw` undo them.

Everything a peer subscribed to and announced is forgotten when its connection ends.

## Limits

The service enforces [ADR 0021](../adr/0021-the-rendezvous-service-limits-every-peer.md). A connection from a blocked address, or from one with 20 connections open, is refused with HTTP 429 before it becomes a WebSocket.

## Closing codes

| Code | Meaning |
| --- | --- |
| 4000 | Another connection of the same identity replaced this one |
| 4001 | A limit or the protocol was broken, and the address is blocked |
| 4002 | The two share no version of the protocol |
| 4003 | The challenge was not answered within 30 seconds |
| 4004 | The peer did not read its messages fast enough |

## Running the service

```sh
go run ./cmd/rendezvous -address wss://rendezvous.example -cert cert.pem -key key.pem
```

`-address` is the address peers connect to, exactly as they sign it. The service serves TLS itself, because behind a proxy every peer would appear to come from the proxy's address; plain `ws://` is accepted only for an address on the same machine, for development. It logs to `-logs` (by default `logs`) only what [ADR 0022](../adr/0022-the-rendezvous-service-logs-nobody.md) allows.
