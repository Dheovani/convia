# 0039 — A person joins a space when an administrator adds a contact

## Status

Accepted

## Date

2026-10-17

## Context

Only an administrator or the owner may add a member to a space ([ADR 0038](0038-a-space-has-three-ranks-and-roles-that-open-channels.md)), and adding one is an operation naming the member's identity ([ADR 0013](0013-a-space-is-replicated-by-its-members.md)). An invitation format of a space's own would be a new secret that can leak, be forwarded or be used by whoever holds it.

## Decision

**A person is invited into a space by an administrator, or the owner, who has them as a contact** ([ADR 0026](0026-a-contact-needs-the-inviters-consent.md)). The invitation is the `add-member` operation itself, sent to the person over their connection with the inviter, with the space's genesis and the operations before it.

**The person accepts or declines.** Until they accept, they are sent nothing more of the space: no channel, no history. Declining is leaving the space, with a `leave` operation.

**An invitation is used once by nature**: it adds one identity, once. There is no code to forward and nothing for anyone else to use.

## Consequences

- An administrator cannot invite a person they are not a contact of; somebody who is has to.
- Membership is decided by the space's signed history alone, with no secret besides it.
- A person added and away is a member who has not yet accepted; the space counts them among its 50.
