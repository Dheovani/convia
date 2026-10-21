# 0038 — A space has three ranks, and roles that open channels

## Status

Accepted

## Date

2026-10-17

## Context

Every change to a space is an operation signed by its author, and every member checks that its author held the authority it needed in the state before it ([ADR 0013](0013-a-space-is-replicated-by-its-members.md)). The more ways authority can be held, the more each member has to check, and the more cases concurrent operations open. A space still has to say who administers it and who reads each channel.

## Decision

**Authority comes from three fixed ranks: owner, administrator and member.** Nothing else confers authority.

| Rank                | May                                                                                                                                                                                             |
| ------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Owner — exactly one | Everything an administrator may; make and unmake administrators; transfer ownership; delete the space.                                                                                          |
| Administrator       | Create, rename and delete channels; set who sees a channel; add and remove members; create, rename and delete roles; give and take roles. Never remove or demote an administrator or the owner. |
| Member              | Read and write in the channels open to them; leave the space.                                                                                                                                   |

**A space may also have roles of its own**, named by its administrators — "Team", "Moderation". **A role opens channels and does nothing else**: it grants no authority, and a member may hold several.

**A channel is open to every member, or restricted to a list of roles**; a restricted channel is seen by whoever holds at least one of them. The owner and the administrators see every channel, since they administer them. Who sees a channel is who replication sends it to ([ADR 0013](0013-a-space-is-replicated-by-its-members.md)).

**Channels that are read but not written — announcements, rules — come later**, as a second list on a channel: the roles that may write in it. It is a new operation, and changes nothing decided here.

## Consequences

- The authority an operation needs is the rank of its author and, for removing a member or giving a role, the rank of whom it is about; never a role.
- Giving or taking a role, or restricting a channel, may revoke someone's access to a channel, and so advances the space's epoch ([ADR 0014](0014-members-of-a-space-share-a-secret-to-tell-who-is-online.md)).
- A space's own hierarchy of roles, as Discord has, is not possible; a later record may add one.
