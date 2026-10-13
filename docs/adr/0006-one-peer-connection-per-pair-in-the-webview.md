# 0006 — One peer connection per pair of peers, in the WebView

## Status

Accepted

## Date

2026-10-13

## Context

[ADR 0005](0005-peers-connect-over-webrtc-with-auxiliary-rendezvous-and-turn.md) chose WebRTC. Convia's window is WebView2 ([ADR 0001](0001-a-go-core-in-a-wails-window-for-windows.md)), which already provides the whole of it — `RTCPeerConnection`, `RTCDataChannel`, ICE, `getUserMedia`, `getDisplayMedia` and media tracks — and ADR 0001 already placed media there. That leaves a question to answer before any WebRTC dependency is added: which side owns the connection itself.

- **A.** One `RTCPeerConnection` per pair of peers, in the WebView, carrying data channels and every media track, while Go remains the application core.
- **B.** A second, separate WebRTC stack in the Go core (Pion) for data, beside the WebView's for media: two ICE negotiations, two connection lifecycles, two reconnections and two ways to fail between the same two peers.

B is justified only by a requirement A cannot meet. Each candidate was checked:

| Requirement | Met by A? |
| --- | --- |
| Go holds the identity key and does the cryptography | Yes. What travels on a data channel is encrypted and decrypted by Go; the page passes opaque bytes through the Wails bridge. |
| A connection is bound to an identity | Yes. Signaling passes through Go, which sees both session descriptions, so it can sign its own DTLS fingerprint and check the other peer's. |
| Go owns storage and protocol state | Yes. The page is a transport; every decision about what arrived is Go's. |
| Messages arrive while the window is hidden | Yes, while the WebView lives. Hiding a window keeps it; ending the process ends every connection, and would under B too. |
| Transport tests in plain Go | No — but that is a cost of testing, not a requirement of the product. |

## Decision

**A.** Each pair of peers has one `RTCPeerConnection`, owned by the WebView, carrying the application's data channels, audio, video and screen sharing. The Go core owns everything else: identity, cryptography, signaling orchestration, persistence, contacts and the state of the application protocol. The WebView owns transport, and only transport.

```text
Go core: identity · cryptography · signaling · storage · contacts · protocol state
   │
   │  Wails bridge — negotiation in, opaque payloads both ways
   ▼
WebView2: RTCPeerConnection ── data channels · audio · video · screen
```

This narrows ADR 0001, which placed connectivity in the Go core: Go orchestrates the connection — it decides when to connect, to whom, and whether the peer is who it claims — and the WebView carries it out. **No Go WebRTC library is used.** A later record may introduce one if a concrete requirement appears that this cannot meet.

## Consequences

- No WebRTC dependency enters `go.mod`.
- Connections live as long as the WebView does, which is why closing the window hides Convia rather than ending it ([ADR 0007](0007-closing-the-window-hides-convia.md)).
- Testing the transport needs a WebView or a browser, not just `go test`; the protocol above it is still tested in Go, with the transport replaced.
- A reload of the page — in development, or after a renderer failure — drops every connection, and peers reconnect through signaling as they would after any interruption.
