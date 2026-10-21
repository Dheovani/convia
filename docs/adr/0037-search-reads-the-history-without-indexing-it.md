# 0037 — Search reads the history without indexing it

## Status

Accepted

## Date

2026-10-16

## Context

What was said is encrypted on disk ([ADR 0010](0010-a-conversation-is-kept-on-each-peer.md)). An index for search would be a second copy of it, which would have to be protected as well, or not be.

## Decision

**Searching decrypts the history in memory as it searches**, and keeps no index anywhere. It finds text regardless of case and accents, in messages as they read now — edited text, not deleted text.

## Consequences

- A search reads every message it might find, so it slows as a history grows; it is never a copy of the history on disk.
