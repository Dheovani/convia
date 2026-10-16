# 0034 — A message is edited and deleted by its author

## Status

Accepted

## Date

2026-10-16

## Context

People correct what they wrote and take back what they should not have sent. A conversation is kept on both peers ([ADR 0010](0010-a-conversation-is-kept-on-each-peer.md)), so a change has to reach the other side the way a message does, and nothing can make the other side forget what it already holds.

## Decision

**An edit and a deletion are entries of the conversation, like a message**: each has its own identifier, its author's next sequence number and a timestamp, waits with its author and is delivered, acknowledged and caught up exactly as a message is ([ADR 0011](0011-a-message-waits-with-its-sender.md)). Only a message's author can edit or delete it for both; an entry naming somebody else's message is ignored.

- **Editing** has no time limit. Both peers keep only the latest text — the one whose edit has the latest timestamp — and discard the earlier ones; the message is shown as edited.
- **Deleting for both** wipes the text on both peers and leaves "message deleted" in its place, so the conversation still reads in order. A deleted message cannot be edited.
- **Deleting for oneself** wipes a message on this machine only, and nothing is sent. It applies to messages received, and to one's own once they are delivered; one still waiting is deleted for both, since it never reached the other side.

## Consequences

- An honest peer forgets an edited or deleted text; a modified one may keep it. Convia says what it does, not what the other machine does.
- The author's sequence numbers count edits and deletions too, which costs nothing and keeps catching up a single comparison.
