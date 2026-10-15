# The protocol between peers

How two peers open a connection and prove who they are on it ([ADR 0008](../adr/0008-the-protocol-between-peers.md), [ADR 0009](../adr/0009-the-cryptography.md)). The messages are in [`proto/convia/v1/peer.proto`](../../proto/convia/v1/peer.proto) and [`handshake.proto`](../../proto/convia/v1/handshake.proto); the core's side is [`internal/peers`](../../internal/peers/peers.go), and the WebView's is [`frontend/src/transport`](../../frontend/src/transport/connections.ts).

## Negotiation

The peer that wants a connection chooses a random 16-byte session, and offers through the rendezvous service ([the rendezvous protocol](rendezvous.md)); every offer, answer and candidate of that connection carries the session. An offer with a session already open renegotiates that connection — an ICE restart, for instance — and must keep its DTLS fingerprint, or the connection is ended.

When both peers offer to each other at once, the one whose public key is lower, compared byte by byte, keeps its own offer and ignores the other's; the other answers it ([ADR 0024](../adr/0024-peers-connect-on-demand-and-reconnect-when-they-can.md)).

A session description must carry exactly one DTLS fingerprint, of SHA-256, SHA-384 or SHA-512.

## The data channel

Each connection has one data channel: negotiated, with id 0, reliable and ordered. Every data channel message is binary and holds one `PeerMessage`.

## The handshake

```text
offerer                                 answerer
 │ ── hello: versions ──────────────────► │
 │ ── challenge: nonce ─────────────────► │
 │ ◄────────────────── hello: versions ── │
 │ ◄────────────────── challenge: nonce ─ │
 │ ── proof: identity, signature ───────► │
 │ ◄───────────── proof: identity, signature ── │
 │ ◄──────────── interest ──────────────► │
```

1. When the data channel opens, each peer sends `hello`, with every version of the protocol it speaks, and `challenge`, with a fresh 32-byte nonce.
2. Once it has both of the other's, each peer uses the highest version both speak. If they share none, it ends the connection, and the person is told which of the two has to update.
3. Each sends `proof`: its identity and its signature of the [peer proof](canonical-encodings.md#the-proof-a-peer-gives-on-a-connection), in its own role, over both identities, both fingerprints and both nonces.
4. Each verifies the other's proof, in the other's role, against the identity it meant to reach. The connection is proven only then.

Before the handshake completes, anything but these messages, a message twice, or a proof that does not verify ends the connection. After it, a handshake message ends it too, and a message this version does not know is ignored ([ADR 0019](../adr/0019-the-protocol-version-is-a-whole-number.md)).

## Interest

Once proven, each peer sends `interest`, saying whether it wants the connection, and again whenever that changes. A connection that neither wants for 10 minutes is closed ([ADR 0024](../adr/0024-peers-connect-on-demand-and-reconnect-when-they-can.md)).
