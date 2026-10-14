# 0021 — The rendezvous service limits every peer

## Status

Accepted

## Date

2026-10-14

## Context

The rendezvous service accepts connections from anybody on the Internet ([ADR 0005](0005-peers-connect-over-webrtc-with-auxiliary-rendezvous-and-turn.md)). Without limits, one machine could exhaust it for everybody, by holding connections, flooding messages or filling its memory with subscriptions.

## Decision

The service enforces these limits:

| Limit | Value |
| --- | --- |
| Connections per identity | 1: a new one replaces the old |
| Connections per address | 20 |
| Time to answer the challenge | 30 seconds |
| Size of a message | 64 KiB |
| Messages per connection | 50 a second, in bursts of up to 100 |
| Identifiers a peer subscribes to | 5,000 |
| Identifiers a peer announces | 5,000 |

**A peer that breaks a limit is disconnected, and its address is blocked**: for one minute the first time, twice as long each time after, up to one hour. Its offences are forgotten a day after the last one. Breaking a limit includes failing the challenge, sending a message the protocol does not allow, and sending more than the service accepts. The blocks are **kept in memory only**, and forgotten when the service restarts.

An address is an IPv4 address, or the /64 network of an IPv6 address, since one machine commonly holds a whole /64.

## Consequences

- An abusive machine is kept out within one message, and cannot simply reconnect; one behind the same address as others shuts them out with it while the block lasts.
- A peer slower than the challenge's 30 seconds cannot connect, and is disconnected without being blocked.
- The application stays within these limits by design, so a person using it is never blocked.
- Blocking an address permanently, by hand, is left for later; it must not need the service to log who it blocked ([ADR 0022](0022-the-rendezvous-service-logs-nobody.md)).
