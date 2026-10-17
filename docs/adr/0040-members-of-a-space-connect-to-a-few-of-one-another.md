# 0040 — Members of a space connect to a few of one another

## Status

Accepted

## Date

2026-10-17

## Context

A space has up to 50 members ([ADR 0013](0013-a-space-is-replicated-by-its-members.md)). If each connected to every other online, a full space would hold 1 225 connections, 49 on each peer. Which peers are connected is not part of the space, so it can be chosen for cost alone, as long as content still reaches every member allowed to read it, and only them.

## Decision

**Each online member keeps about four connections in a space**: to its two neighbours on a ring of the online members, ordered by a hash of each member's identity under the space's current epoch ([ADR 0014](0014-members-of-a-space-share-a-secret-to-tell-who-is-online.md)), and to two members it picks at random among the others. A connection to a contact made for another reason counts as well. Content travels over these connections, each member passing on what its neighbours lack.

**A member passes a channel's content only to members allowed to read it** ([ADR 0038](0038-a-space-has-three-ranks-and-roles-that-open-channels.md)). So for a restricted channel, whose readers may not all be neighbours, its readers connect to one another on demand, as a conversation between two contacts does ([ADR 0024](0024-peers-connect-on-demand-and-reconnect-when-they-can.md)), to carry what the ring cannot.

## Consequences

- A full space holds about 100 connections, not 1 225, and each peer about four per space.
- The ring changes with the epoch and with who is online, so the members neighbouring a given member change too.
- Content takes several hops to reach every member; each hop is an authenticated connection between members, and each message is signed by its author ([ADR 0013](0013-a-space-is-replicated-by-its-members.md)).
