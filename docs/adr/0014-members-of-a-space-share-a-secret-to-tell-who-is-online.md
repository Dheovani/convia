# 0014 — Members of a space share a secret to find one another

## Status

Accepted

## Date

2026-10-13

## Context

Contacts learn when each other are reachable through identifiers only the two of them can compute ([ADR 0012](0012-only-contacts-can-tell-when-a-person-is-online.md)). Members of a space are often not each other's contacts, yet a member holding messages for another needs to know when they are back ([ADR 0013](0013-a-space-is-replicated-by-its-members.md)), and nobody outside the space should be able to watch its members.

## Decision

**Members of a space share a discovery secret scoped to an epoch**, from which they derive rotating rendezvous identifiers exactly as ADR 0012 derives them for contacts: opaque to the rendezvous service, changing over time, and one for each member, so that a member can wait for a particular person. A member announces their identifier when they come online; a member who wants to reach another subscribes to that member's identifier.

**The secret travels only to current members**, as the content of the space does ([ADR 0013](0013-a-space-is-replicated-by-its-members.md)): over authenticated connections, and to nobody not allowed to hold it.

**A change of membership that revokes access advances the epoch and rotates the secret.** Whoever makes the change — an owner, or an administrator with the authority to — generates the secret of the new epoch, and only the members who still belong receive it. Anybody whose access was revoked keeps the secret of an earlier epoch, which from then on derives identifiers nobody announces.

**This exists for reachability and synchronization, and for nothing the interface shows.** That members can find one another does not mean the interface displays whether anybody is online; whether and how it shows presence is a decision of its own.

## Consequences

- Somebody whose access was revoked, or anybody outside the space, cannot find its members through the rendezvous service.
- A change that revokes access is followed by a new secret reaching every remaining member, including those away at the time; a member who has not received it yet cannot find the others until somebody gives it to them.
- The exact derivation, how often the identifier changes within an epoch, and how an epoch is attached to the change that advanced it are specified when it is built, encoded canonically as [ADR 0009](0009-the-cryptography.md) requires.
