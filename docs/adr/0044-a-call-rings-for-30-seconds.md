# 0044 — A call rings for 30 seconds

## Status

Accepted

## Date

2026-10-21

## Context

A call between two people is messages of the protocol — calling, accepting, declining, ending — over the connection they share ([ADR 0015](0015-calls-and-screen-sharing-ride-the-peer-connection.md)). Convia often runs hidden in the tray ([ADR 0033](0033-a-hidden-convia-says-who-wrote-not-what.md)), and the person called may not be looking at it. What reading and presence say stays on the reader's machine ([ADR 0032](0032-reading-and-presence-stay-on-the-readers-machine.md)).

## Decision

**A call rings for 30 seconds.** Then it ends unanswered: the caller sees that nobody answered, and the person called that they missed it.

**A person called while Convia is hidden is told by a notification naming who calls**, and nothing else, with a ringing sound and the taskbar button flashing. Choosing the notification shows the call, to answer or decline. The window never comes forward by itself.

**A missed call is a line in the conversation, on the machine of the person who missed it alone** — "Missed call from Ana" and when. It is not sent, and the caller keeps no such line.

## Consequences

- A call nobody can answer within 30 seconds is lost; the caller calls again.
- What the notification says of a call is who calls, as it says of a message who wrote ([ADR 0033](0033-a-hidden-convia-says-who-wrote-not-what.md)).
- A missed call is not part of the conversation both hold: each machine says only what happened on it.
