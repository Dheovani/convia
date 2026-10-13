# 0008 — The protocol between peers

## Status

Accepted

## Date

2026-10-13

## Context

Two peers that have connected ([ADR 0005](0005-peers-connect-over-webrtc-with-auxiliary-rendezvous-and-turn.md), [ADR 0006](0006-one-peer-connection-per-pair-in-the-webview.md)) share a DTLS channel that is encrypted end to end, relays included. That channel proves only that each side holds the certificate it advertised; it says nothing about which person is on the other end. The protocol has to settle that, say what travels on the channel, and let two different versions of Convia tell whether they can talk at all.

## Decision

**A peer proves who it is with a challenge, answered over the connection and bound to its DTLS session.** Once the data channel opens, each peer sends the other a fresh random challenge, and each answers by signing, with its identity key ([ADR 0004](0004-a-person-is-a-key-their-application-made.md)), the challenge together with the DTLS fingerprints of both sides, as both session descriptions carried them. WebRTC itself guarantees that the DTLS session uses exactly the certificates those fingerprints name, so a signature over them is valid for this session and no other: a party in the middle cannot reuse an answer it relays. Until both answers verify, nothing but the handshake is accepted on the connection, and a peer whose answer does not verify is disconnected.

**Messages are Protocol Buffers.** Every message has an explicit schema in the repository, from which the Go and the TypeScript code are generated.

**The version is negotiated when a connection opens.** Each peer states the versions of the protocol it speaks, and the highest both speak is used. When they share none, the connection ends, and the interface says that one of the two has to update rather than failing silently.

**The first version uses one data channel, reliable and ordered**, for the handshake, control and messages alike. A feature that needs another channel — files, for instance — adds it when it is built.

**A peer reaches the rendezvous service over WSS, and proves who it is with an Ed25519 challenge.** The service sends a challenge, and the peer signs it with its identity key. The service learns a public key and nothing else: no name, no account, no contact.

## Consequences

- Protocol Buffers bring code generation into the build, for Go and for TypeScript, and their libraries into the dependencies, under [ADR 0003](0003-dependencies-carry-permissive-licenses.md).
- A peer that connects but never proves its identity holds a connection that carries nothing, and is dropped.
- Still open, and decided with the cryptography (M02-004): the exact bytes a challenge signs, its size and how fresh it must be; the format of an invitation; and what, if anything, is encrypted on top of DTLS.
