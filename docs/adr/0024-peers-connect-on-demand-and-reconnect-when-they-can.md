# 0024 — Peers connect on demand, and reconnect when they can

## Status

Accepted

## Date

2026-10-15

## Context

Each pair of peers has one connection, orchestrated by the core ([ADR 0006](0006-one-peer-connection-per-pair-in-the-webview.md)). A connection costs memory, a place in the WebView and possibly a TURN relay, and it drops — when a peer goes away, when a network changes, when a machine sleeps. Something has to decide when a connection exists, and what happens when it ends.

## Decision

**A connection exists while either peer has a reason for it.** A peer connects to a contact when it has something to deliver to them ([ADR 0011](0011-a-message-waits-with-its-sender.md)) or the person has their conversation open. Each side tells the other, over the connection, whether it still wants it; when neither does for 10 minutes, it is closed.

**A contact who is away is waited for, never polled.** When the rendezvous service says a contact is not connected, no further attempt is made until the service says they are reachable again (ADR 0011, [ADR 0012](0012-only-contacts-can-tell-when-a-person-is-online.md)), and then one is made at once.

**An attempt that fails while both are online is retried with a growing wait**: 1 second, then twice as long each time, up to 5 minutes, each wait shortened by a random part of up to half, so that two peers do not retry in step. The wait starts again from 1 second once a connection succeeds. The connection to the rendezvous service is retried the same way, up to 1 minute.

**A change of network, or waking from sleep, retries at once** every connection that is wanted and not open.

**When both peers try to connect to each other at the same moment, the one whose public key is lower, compared byte by byte, keeps its attempt**, and the other answers it.

## Consequences

- Nobody holds connections they do not use; a message to somebody online is delivered within the time it takes to connect, rather than at once.
- Two contacts who are both away cost each other nothing; one who comes back is reached within a notification's delay.
- A peer whose attempts keep failing — a network that blocks even TURN — is retried every 5 minutes for as long as something is waiting for them.
