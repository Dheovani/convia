# 0036 — Files wait with their sender, up to 100 MB

## Status

Accepted

## Date

2026-10-16

## Context

People send each other documents and pictures. A file is far larger than a message ([ADR 0029](0029-a-message-holds-at-most-4000-characters.md)), would hold up the messages behind it on the one ordered data channel ([ADR 0008](0008-the-protocol-between-peers.md)), and a picture can carry where it was taken.

## Decision

- **A file is at most 100 MB.** It is announced by a message of the conversation that names it — its name, size, type and SHA-256 — which waits and is delivered like any message ([ADR 0011](0011-a-message-waits-with-its-sender.md)).
- **Its content travels on a data channel of its own**, beside the one for messages, in pieces the recipient asks for from where it stopped, so an interrupted transfer resumes and messages never wait behind a file. The recipient checks the whole against the SHA-256 before keeping it.
- **A file waits with its sender** until the recipient has it, as a message does, and arrives from a contact without asking.
- **Both peers keep it encrypted**, in pieces sealed with the history's content key ([ADR 0010](0010-a-conversation-is-kept-on-each-peer.md)), beside the database.
- **PNG, JPEG, GIF and WebP pictures are shown in the conversation**; anything else, SVG included, is offered to be saved. **The metadata of a picture is removed before it is sent** — the place and the camera a photograph records, and any text it carries — keeping only what drawing it needs.
- **What the interface shows is served by the core to the window alone**, under a path of the window's own; nothing is fetched from anywhere else.

## Consequences

- A sender who deletes Convia before the recipient comes online takes the file with them.
- Removing a picture's metadata may drop its orientation, so the interface turns a picture as its recorded orientation says before the record goes.
