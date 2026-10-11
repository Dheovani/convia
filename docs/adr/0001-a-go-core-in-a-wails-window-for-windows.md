# 0001 — A Go core in a Wails v2 window, with a React interface, for Windows first

## Status

Accepted

## Date

2026-10-11

## Context

Convia is a desktop application that is itself a peer ([`README.md`](../../README.md)). It needs native logic on the user's machine — identity, cryptography, the protocol between peers, connectivity, storage and the operating system — and a rich, Discord-like interface. The README separates the two: the technology of the window must not decide the architecture of the network.

## Decision

- **Go is the language of the local core**: identity, cryptography, the P2P protocol, signaling, storage, discovery and connectivity, and integration with the operating system. The core is part of the application, not a web backend it consumes.
- **The window is Wails v2**, the stable line. Wails v3 is not used while it is in beta.
- **The interface is React and TypeScript, built with Vite and styled with Tailwind CSS.**
- **Media lives in the interface in the first version.** WebRTC, the camera, the microphone and screen capture use the WebView2 APIs from TypeScript; Go is not required to do all of it. How peers find each other, and how a media session is negotiated between them, is not decided here.
- **The first version is Windows only.** The core is written without needless coupling to Windows, but no effort goes into building, testing or distributing for macOS or Linux until they are supported. The core may gain tests on Linux later; a macOS or Linux build is not a requirement until those systems are.

## Consequences

- One binary per release: the Go core and the built interface compiled together by Wails, running in WebView2, which Windows 10 and 11 provide.
- Everything that compiles the application is built and checked on Windows ([ADR 0002](0002-what-a-change-must-pass.md)).
- This decides the stack and nothing about the protocol: identity, discovery, NAT traversal, storage, synchronization, offline delivery, groups and cryptography are still open, and are decided by records of their own.
