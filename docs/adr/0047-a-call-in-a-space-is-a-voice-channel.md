# 0047 — A call in a space is a voice channel

## Status

Accepted

## Date

2026-10-21

## Context

A call with more people than two is a full mesh among them, of at most six while a camera or a screen is on ([ADR 0015](0015-calls-and-screen-sharing-ride-the-peer-connection.md)). Such a call could begin in a channel, ringing its members, or be a channel of its own that people join and leave when they like, as in Discord. A call between two people rings ([ADR 0044](0044-a-call-rings-for-30-seconds.md)); ringing every member of a space would disturb all of them.

Who is in such a call changes all the time, and matters only while they are: it is not part of what a space keeps ([ADR 0013](0013-a-space-is-replicated-by-its-members.md)).

## Decision

**A call in a space is a voice channel**: a channel created as one, with `create-voice-channel`, whose kind never changes. Who sees it is decided by roles as for any channel ([ADR 0038](0038-a-space-has-three-ranks-and-roles-that-open-channels.md)). Nobody is rung: people join it and leave it, and are in one voice channel at a time, and in no call with one person meanwhile.

**Who is in a voice channel is a presence, not history.** A member who joins signs a presence naming the space, the channel and the moment, says it again every 30 seconds, and signs one more when it leaves. Presences travel as a space's content does ([ADR 0040](0040-members-of-a-space-connect-to-a-few-of-one-another.md)), to the members who may see the channel alone, and each member believes the latest a member signed, for 90 seconds at most. Nothing of it is stored.

**Joining connects to each other member there**, and sends them the microphone, and the camera if on: a full mesh, over the connections they share or make for it.

**A member muted, or who may not write in the channel, only listens.** Their own Convia sends nothing, and the others play nothing of theirs.

**A voice channel holds at most 6 people while a camera is on, and 12 by voice alone.** A seventh does not join while a camera is on, nor is a camera turned on among seven or more. The 12 is provisional, until it is measured (M19-004).

## Consequences

- Two who join at the same moment may make one more than the limit; each sees the other and either may leave.
- A Convia that closes without leaving is taken to be there for up to 90 seconds.
- An older Convia refuses `create-voice-channel`, as any kind it does not know; members of a space run versions that know it.
- The screen is shared in a call between two people only, for now.
