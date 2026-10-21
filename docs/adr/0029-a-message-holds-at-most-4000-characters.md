# 0029 — A message holds at most 4,000 characters

## Status

Accepted

## Date

2026-10-16

## Context

A message waits with its sender, is retransmitted until it is acknowledged, and is kept by both peers ([ADR 0010](0010-a-conversation-is-kept-on-each-peer.md), [ADR 0011](0011-a-message-waits-with-its-sender.md)). Without a bound, one message could fill the outbox, the data channel and the history.

## Decision

**A message holds at most 4,000 characters** — Unicode code points, so never more than 16 KiB of UTF-8. Longer text is never cut or split by Convia: the interface shows how many characters are left as the limit nears, and does not send a message above it; the person divides it themselves. The core refuses a longer message too, and one from a peer is a breach of the protocol, neither stored nor acknowledged.

## Consequences

- Text longer than a message, such as a document, waits for files ([ROADMAP](../../ROADMAP.md) M13).
- The limit is part of version 1 of the protocol ([ADR 0019](0019-the-protocol-version-is-a-whole-number.md)); raising it needs a new version, since an older peer would refuse what a newer one sends.
