# 0012 — Only contacts can tell when a person is online

## Status

Accepted

## Date

2026-10-13

## Context

A peer subscribes through the rendezvous service to learn when a contact becomes reachable ([ADR 0011](0011-a-message-waits-with-its-sender.md)). If subscriptions named a person's public key, anybody holding that key could watch when the person is online — and an invitation, which carries the key, is reusable and travels freely ([ADR 0009](0009-the-cryptography.md)). Letting the person tell the service whom to allow would make the service keep their contacts while they are away: a social graph held on a server, which the [`README.md`](../../README.md) rules out.

## Decision

**Reachability is announced and watched under identifiers only two contacts can compute.** When two peers first connect, they agree on a secret that belongs to that pair alone, over their authenticated connection ([ADR 0008](0008-the-protocol-between-peers.md)). From it each derives an opaque identifier that changes over time. A peer that wants to know when its contact is back subscribes to that identifier; a peer that comes online announces the identifiers of its contacts. The rendezvous service matches identifiers it cannot interpret, and keeps them only while the peers that sent them are connected.

## Consequences

- Holding somebody's invitation is not enough to watch them: only a contact the person has connected with can compute the identifier.
- Before two peers have connected for the first time there is nothing to watch: the first connection is attempted when both happen to be online.
- The service learns that some identifier was watched and some identifier was announced; it holds no list of anybody's contacts.
- The exact secret, the derivation and how often the identifier changes are specified when it is built, encoded canonically as [ADR 0009](0009-the-cryptography.md) requires.
