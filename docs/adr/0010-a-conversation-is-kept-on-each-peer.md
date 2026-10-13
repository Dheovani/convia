# 0010 — A conversation is kept on each peer, in the same order on both

## Status

Accepted

## Date

2026-10-13

## Context

There is no server holding a conversation ([`README.md`](../../README.md)): each of the two peers keeps its own copy, on its own machine, and the two have to show the same history in the same order. Neither machine's clock can be trusted to agree with the other's, and either peer can be away while the other writes.

## Decision

### Where it is kept

**Each peer keeps its conversations in SQLite**, through `modernc.org/sqlite` — a database in a single file, in Go, with no C compiler needed to build it.

### How it is protected on the machine

- **The identity's private key is protected with DPAPI**, Windows' protection bound to the user's account on that machine: only that account, there, can read it.
- **The content of the history is encrypted with a key of its own** — not the identity key — generated at random, used with XChaCha20-Poly1305 ([ADR 0009](0009-the-cryptography.md)), and itself protected with DPAPI. What is encrypted is what was said; the metadata needed to find and order it — which conversation, which peer, which position — stays readable to whoever can read the file.

### The order of a conversation

Every message carries:

- **an identifier** of 16 random bytes, the same on both peers;
- **its sender's sequence number** — 1, 2, 3, … for each sender in each conversation — which shows when one is missing;
- **a hybrid logical clock** timestamp, which follows real time where the clocks allow it and never runs backwards where they do not.

A conversation is shown ordered by the hybrid logical clock, and **two messages with the same timestamp by their identifiers, compared byte by byte**. Both peers hold the same messages with the same values, so both show the same order without either being the authority over it.

### Catching up

**When two peers reconnect, each tells the other the highest sequence number it holds from it, and each sends what the other is missing**, in order. With one device per person ([ADR 0004](0004-a-person-is-a-key-their-application-made.md)), a sender's own sequence is the whole of what has to be compared.

### Editing and deleting

Not decided here. They are decided with those features, when they are built.

## Consequences

- An identity protected with DPAPI cannot be copied to another computer as a file: moving it is what exporting it is for ([ADR 0009](0009-the-cryptography.md)), and history moves with neither.
- Anybody who can read the database file — another program running as the same user, for one — can see who a person talks to and when, though not what was said.
- `modernc.org/sqlite` and what it depends on are checked against [ADR 0003](0003-dependencies-carry-permissive-licenses.md) before they are added.
- In a space, catching up compares how far each member holds every author's messages, not one sender's ([ADR 0013](0013-a-space-is-replicated-by-its-members.md)).
