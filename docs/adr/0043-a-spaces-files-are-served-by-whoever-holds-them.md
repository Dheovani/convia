# 0043 — A space's files are served by whoever holds them

## Status

Accepted

## Date

2026-10-21

## Context

In a conversation between two people, a file waits with its sender until its recipient pulls it ([ADR 0036](0036-files-wait-with-their-sender-up-to-100-mb.md)). A channel's entries are signed by their author and passed on by any member allowed to read the channel ([ADR 0013](0013-a-space-is-replicated-by-its-members.md)), and files were left out of channels until this was decided ([ADR 0042](0042-a-channel-holds-what-a-conversation-holds-but-files.md)). In a space, up to 50 people may want the same file, and its sender may be away.

## Decision

**A file in a channel is an entry**, as in a conversation: its name, its size, its type and its SHA-256, signed by its author. Its content travels apart from it, as the file's does between two people.

**Any member holding a file whole serves it** to any member allowed to read its channel. A member wanting a file asks the members it is connected to first and, if none holds it, connects to the file's author on demand ([ADR 0024](0024-peers-connect-on-demand-and-reconnect-when-they-can.md)). The SHA-256 in the signed entry says whether what arrived is what the author sent, whoever sent it.

**A picture of at most 10 MB is pulled as soon as its entry arrives**, so that it shows in the channel; every other file only when a member asks for it. Once pulled whole, a file is held, and served, as its author's is.

**A file holds at most 100 MB**, as in a conversation, with the same handling of pictures: their metadata removed by their author, their orientation kept.

## Consequences

- A file stays available while any member who pulled it is online, not only while its author is.
- A member who never asks for a large file never receives it, nor serves it.
- What a member holds of a space's files is theirs to keep, as what they read is ([ADR 0013](0013-a-space-is-replicated-by-its-members.md)); deleting the message wipes the file on every member as it wipes its text.
