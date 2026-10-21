# 0046 — A shared screen may carry the computer's sound

## Status

Accepted

## Date

2026-10-21

## Context

A call can share a screen or a window over the connection it rides ([ADR 0015](0015-calls-and-screen-sharing-ride-the-peer-connection.md)), captured with `getDisplayMedia()` through the system's own picker ([media permissions](../platform/media-permissions.md)). The computer's sound can be captured with it, as when a video or a game is shown. That sound holds everything the computer plays, the other side's voice included, which they then hear again.

The other side receives the screen as one more video track, as it receives the camera, and nothing in the track says which is which.

## Decision

**A shared screen carries the computer's sound when the person chooses it as they share**, with a choice beside the button that shares. Without it, the screen is shared without sound. Where the system's picker offers its own choice of sound, it has the last word.

**The echo is accepted.** The other side may hear their own voice back while sound is shared; the person who shares chooses it, and stops it by sharing without sound.

**The side that shares says which stream carries its screen** with a `screen` message over the authenticated connection, naming the stream and the call, and an empty stream once it stops. The other side shows that stream as the screen, and everything else as the voice and the camera.

## Consequences

- The camera and the screen can be on at once.
- Sound is shared only when asked for: the default shares none.
- Stopping from the system's own control stops sharing as the interface does, and the other side is told.
