# 0028 — Removing or blocking a contact is silent

## Status

Accepted

## Date

2026-10-15

## Context

A person needs to stop talking to somebody, and to stop somebody from reaching them. Whatever the other side is told reveals the decision; whatever it is not told leaves its messages waiting ([ADR 0011](0011-a-message-waits-with-its-sender.md)).

## Decision

**Removing a contact deletes them and stops connecting to them**, and the person is asked whether to delete the conversation too. **Blocking does the same and keeps their key**, so that their requests and connections are refused without an answer. Either can be undone only by a new invitation, and, for a block, by unblocking first.

**The other side is never told.** To them, the person is simply no longer reachable, as after deleting an identity ([ADR 0018](0018-replacing-protecting-and-deleting-an-identity.md)).

## Consequences

- A removed or blocked contact's messages wait with them, never delivered, until they remove them.
- Their pairwise reachability identifiers are no longer announced, so the rendezvous service sees nothing more of the pair ([ADR 0012](0012-only-contacts-can-tell-when-a-person-is-online.md)).
