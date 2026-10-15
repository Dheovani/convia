# 0027 — A new key is a new person

## Status

Accepted

## Date

2026-10-15

## Context

A person is the key their application made ([ADR 0004](0004-a-person-is-a-key-their-application-made.md)), and can export it, import it elsewhere, or replace it ([ADR 0018](0018-replacing-protecting-and-deleting-an-identity.md)). A contact's key could therefore stop being the one their peer proves.

## Decision

**A contact is their key, and a different key is a different person.** The same identity imported on another machine is the same contact, and nothing changes. A new or different identity is reached only through a new invitation, and becomes a new contact, unverified, beside the old one, which simply stops answering. No key vouches for another.

## Consequences

- A connection whose proof does not match the key it was meant to reach is refused, as it already is ([ADR 0008](0008-the-protocol-between-peers.md)), so nobody can take over a contact by claiming to have changed keys.
- A person who loses their identity starts again with every contact, who must verify them again.
