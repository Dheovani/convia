# 0015 — Calls and screen sharing ride the peer connection

## Status

Accepted

## Date

2026-10-13

## Context

Voice, video and screen sharing are native to Convia, and must travel from one peer to another, never through a server by default ([`README.md`](../../README.md)). Each pair of peers already has one connection, in the WebView, meant to carry media as well as data ([ADR 0006](0006-one-peer-connection-per-pair-in-the-webview.md)), and a space can have up to 50 members ([ADR 0013](0013-a-space-is-replicated-by-its-members.md)). What remains is how a call starts and ends, how its media joins that connection, and what a call with more than two people is.

## Decision

**Audio, camera and screen tracks are added to the peer connection that already exists.** Adding or removing one renegotiates that connection with WebRTC's perfect negotiation pattern, and when the two peers are already connected, the renegotiation travels over their authenticated data channel ([ADR 0008](0008-the-protocol-between-peers.md)) — not through the rendezvous service. Two peers that are not yet connected connect first, as any two peers do ([ADR 0005](0005-peers-connect-over-webrtc-with-auxiliary-rendezvous-and-turn.md)).

**A call's lifecycle is messages of the protocol** — calling, accepting, declining and ending — in Protocol Buffers, like every other message.

**A call with more than two people is a full mesh among the people in the call**: each sends their media to each of the others, over the connection the two of them share. **While video or screen sharing is on, a call has at most six people.** That limit is provisional, and an audio-only call's limit is set by measurement.

**A screen is captured with `getDisplayMedia()` in WebView2.** How Wails and WebView2 handle the permission requests for the camera, the microphone and the screen is verified when it is built.

**Media is protected by DTLS-SRTP alone in the first version**, end to end between the two peers of each connection. Nothing in this design forwards anybody's media through a third peer; a design that did would have to reconsider this, as [ADR 0009](0009-the-cryptography.md) reconsiders messages.

## Consequences

- No media server exists, and no peer relays another's media: a call costs each participant upload bandwidth for every other participant, which is what the limit of six bounds.
- The participants of a call are a subset of the space's members; being in the space does not put anybody in the call.
- A call with more people than a mesh can carry needs a different design, recorded when it is needed.
