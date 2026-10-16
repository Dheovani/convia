# 0035 — Replies and reactions

## Status

Accepted

## Date

2026-10-16

## Context

Discord, whose look Convia takes ([`README.md`](../../README.md)), lets a person answer a particular message and react to one.

## Decision

- **A reply is a message that names the message it answers**, shown with a short excerpt of it. It is a message in every other way; if the message it answers is deleted, the reply says so.
- **A reaction is a Unicode emoji a person puts on a message, or takes off again**, sent as an entry of the conversation like an edit ([ADR 0034](0034-a-message-is-edited-and-deleted-by-its-author.md)). For each person, emoji and message, the entry with the latest timestamp decides whether the reaction is on. A reaction is at most one emoji: no custom images, which would have to be fetched or carried.

## Consequences

- Reactions and replies wait, are delivered and caught up like messages, so both peers see the same ones.
