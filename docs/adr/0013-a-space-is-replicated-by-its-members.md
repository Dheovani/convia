# 0013 — A space is replicated by its members, under signed authority

## Status

Accepted

## Date

2026-10-13

## Context

Convia has spaces with several people in them, organized as Discord organizes servers ([`README.md`](../../README.md)). There is no server to own a space, its channels, its membership, its roles or its messages, so a space has to be state its members hold and replicate, and cryptography — not a server — has to decide who may change it. The records before this one were written for two peers; this one says what changes when there are more.

## Decision

### A space and its channels

**A group is a space containing channels.** Members and roles belong to the space; each channel is a conversation inside it.

```text
Space
├── members
├── roles
├── channels
└── signed administrative history
```

**A space has an identifier of its own**, generated when it is created and never derived from the owner's key, so that it is the same space after ownership moves. It begins with a genesis:

```text
SpaceGenesis
├── space_id
├── creator_identity
├── creation_metadata
└── creator_signature
```

### Authority

**The creator is the space's first owner, and every change to the space is an operation signed by whoever makes it**: creating, renaming and deleting channels; adding and removing members; granting and revoking roles; granting and revoking administration; transferring ownership. The owner delegates by signing grants, and a holder of a grant may do what it allows and nothing more. **Every member keeps the space's administrative history and verifies it itself** — each signature, and that each signer held the authority the operation needed. No server validates anything, and authority that was never granted cannot be forged.

**Ownership can be transferred**, by an operation the current owner signs naming the new owner. There is no recovery authority: a space whose owner's identity is lost, with no transfer made, can no longer be administered. That is the price of having no centre, until a later record introduces another mechanism.

### Concurrent changes

Two holders of authority may change a space at the same time, and every honest member must reach the same state from the same set of valid operations, whatever order they arrived in. So:

- **Each operation carries a hybrid logical clock timestamp and an identifier** ([ADR 0010](0010-a-conversation-is-kept-on-each-peer.md)), **and names, by hash, the latest operations its author had seen.**
- **Operations are applied in one order every member computes alike**: an operation never before the operations it names, and otherwise by timestamp, then by identifier compared byte by byte.
- **An operation takes effect only if its author held the authority it needs in the state the operations before it produced.** One that does not is kept in the history, so that every member can see it, and changes nothing.

An operation made concurrently with the revocation of its author's authority, without its author having seen that revocation, can still take effect. That is inherent to having no centre; the owner can undo it afterwards.

### How messages travel

**Members replicate a space's messages among themselves.** An author sends a message to the members it can reach; members later pass authenticated copies to members who were away or are missing part of the history. A member that passes a message on only carries it: it does not become its author.

Each author's messages in a channel carry that author's sequence numbers, and **members compare, author by author, how far each holds** — the per-sender catch-up of [ADR 0010](0010-a-conversation-is-kept-on-each-peer.md), with one entry for every author. Any member holding a valid missing message may supply it.

**A message is passed only to a member allowed to read it.** Belonging to a space does not grant every channel: content is sent or synchronized to a member only if that member is authorized for that channel at that moment. Distributing a message needs both its authenticity and its recipient's authorization.

### Every group message is signed by its author

Because a message can arrive through somebody other than its author, **each group message is signed by its original author with Ed25519**, and every member verifies that signature itself rather than trusting whoever passed the message on. The signature covers every field that matters to security, so that a message cannot be replayed into another space, another channel or another position:

```text
GroupMessage
├── protocol_version
├── space_id
├── channel_id
├── author_identity
├── author_sequence
├── message_id
├── logical_timestamp
├── content
└── signature
```

The fields are encoded canonically, as [ADR 0009](0009-the-cryptography.md) requires of everything that is signed.

### Confidentiality

**No encryption is added for groups in the first version.** A message is only ever passed between members allowed to read it, and every hop is an authenticated DTLS connection:

```text
Alice == DTLS ==> Bob == DTLS ==> Carol
```

### Joining and leaving

**A new member may receive the existing history of every channel they are allowed to read**, as part of ordinary synchronization, and of no other. How much of it exists depends on what the members still hold: there is no archive that keeps every message forever.

**Once a valid removal is known, the members stop sending the removed person new content, stop synchronizing history to them, and reject any further action of theirs** unless another valid authorization allows it. **What they already received cannot be taken back**: a person who has read a message holds it, and no protocol can make it disappear from their machine.

### Size

**A space has at most 50 members in the first version.** It is a conservative limit of the product, not a measured capacity: a mesh of 50 peers all connected at once is 1 225 connections, and 49 for each of them. It is revised against measurements of connections, memory, processor, signaling, synchronization traffic, bandwidth, reconnection storms, catching up after being away, and the replication of administrative state.

**The group is logical; which peers are connected at any moment is not part of it.** Connections on demand, a subset of peers, gossip, or any other way of choosing peers may be used without changing anything above.

## Consequences

- A space exists as long as members hold it. Nothing centralizes a space, its history, its membership, its channels or the fan-out of its messages.
- [ADR 0011](0011-a-message-waits-with-its-sender.md) holds for conversations between two people; in a space, any member allowed to read a message may deliver it, not only its author.
- Every member stores and verifies the whole administrative history of each space it belongs to.
- Members of a space find one another through an epoch-scoped secret of the space ([ADR 0014](0014-members-of-a-space-share-a-secret-to-tell-who-is-online.md)).
- Calls with more than two people are decided in [ADR 0015](0015-calls-and-screen-sharing-ride-the-peer-connection.md).
