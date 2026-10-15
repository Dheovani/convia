# 0025 — The transport is tested in three layers

## Status

Accepted

## Date

2026-10-15

## Context

The connection between peers lives in the WebView, where `go test` cannot reach it ([ADR 0006](0006-one-peer-connection-per-pair-in-the-webview.md)), and what it protects — the handshake that proves who is on the other end ([ADR 0008](0008-the-protocol-between-peers.md)) — must be tested against every way it can be forged.

## Decision

The transport is tested in three layers, every one of them in the checks of [ADR 0002](0002-what-a-change-must-pass.md):

1. **The core in Go, with the WebView replaced** by a transport in memory that behaves as two connected WebViews would. It covers orchestration, the handshake, and every forgery and replay of it, against a real rendezvous service.
2. **The WebView's connection manager in Vitest, with `RTCPeerConnection` replaced**, for the order of operations and what is reported to the core.
3. **The same manager with real WebRTC in Chromium**, the engine WebView2 is built on, driven by Playwright: two connections negotiate, open their data channel and exchange messages, directly and through coturn's TURN relay.

## Consequences

- Playwright and the Chromium it drives are development dependencies only, under [ADR 0003](0003-dependencies-carry-permissive-licenses.md).
- The third layer needs coturn running, so its check runs on Linux, where the containers of [`compose.yaml`](../../compose.yaml) run.
- Between two real Convia windows, and across two networks, a connection is still tested by hand, once there are contacts to connect to.
