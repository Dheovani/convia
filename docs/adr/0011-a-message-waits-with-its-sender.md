# 0011 — A message waits with its sender until both peers are online

## Status

Accepted

## Date

2026-10-13

## Context

When Alice writes to Bob while Bob's computer is off, the message has to be somewhere until it can be delivered. The [`README.md`](../../README.md) rules out the usual answer: no server owns or holds conversations. Convia keeps running while its window is closed ([ADR 0007](0007-closing-the-window-hides-convia.md)), so a peer is online for as long as its computer is on.

## Decision

**A message waits on its sender's machine until both peers are online at the same time**, and is then delivered over their connection. Nothing else ever holds it: no server, no relay, no other peer. Because it never leaves a DTLS session between the two, the protection of [ADR 0009](0009-the-cryptography.md) is unchanged.

**A peer learns that a contact is back through the rendezvous service.** It subscribes there to the reachability of the contacts it has messages waiting for, and the service tells it when one becomes reachable. A subscription is ephemeral operational state ([ADR 0005](0005-peers-connect-over-webrtc-with-auxiliary-rendezvous-and-turn.md)): it lasts while the subscriber is connected, and the service keeps no record of it afterwards.

**A message is delivered once the recipient has stored it.** The recipient acknowledges a message only after writing it to its own database ([ADR 0010](0010-a-conversation-is-kept-on-each-peer.md)), and the sender shows it as delivered only on that acknowledgement. Until then the interface says it is waiting for the recipient to be online.

**A waiting message never expires.** It waits for as long as it takes, unless the person who wrote it removes it.

Two guarantees hold throughout:

- **Waiting messages survive restarts** — of the application and of the machine. A message is stored before it is shown as sent, and the queue is that storage, not memory.
- **Delivery is idempotent.** A message is sent again until it is acknowledged, and receiving one whose identifier the recipient already holds stores nothing new and is acknowledged again. However many times a message is retransmitted, it appears once.

## Consequences

- Two people whose computers are never on at the same time cannot exchange messages. That is the cost of no server holding them, and the interface says so rather than hiding it.
- The rendezvous service learns, while a subscription lasts, that one key waits to reach another — no more than signaling already reveals.
- Who may watch whose reachability is decided in [ADR 0012](0012-only-contacts-can-tell-when-a-person-is-online.md): only contacts.
- This holds for conversations between two people. In a space, any member allowed to read a message may deliver it ([ADR 0013](0013-a-space-is-replicated-by-its-members.md)).
