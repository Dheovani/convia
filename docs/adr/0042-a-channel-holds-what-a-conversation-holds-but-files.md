# 0042 — A channel holds what a conversation holds, but files

## Status

Accepted

## Date

2026-10-17

## Context

A conversation between two people has text written in a subset of Markdown, replies, reactions, editing and deleting ([ADR 0030](0030-messages-are-written-in-a-subset-of-markdown.md), [ADR 0034](0034-a-message-is-edited-and-deleted-by-its-author.md), [ADR 0035](0035-replies-and-reactions.md)), and files that wait with their sender ([ADR 0036](0036-files-wait-with-their-sender-up-to-100-mb.md)). A channel's entries are group messages, signed by their author and passed on by any member ([ADR 0013](0013-a-space-is-replicated-by-its-members.md)).

## Decision

**A channel's entries are those of a conversation, with the same rules**: messages of 1 to 4,000 characters in the same Markdown, replies, reactions of one emoji, and edits and deletions by a message's author — each the content of a group message, signed by whoever made it. An edit or deletion wipes the text it supersedes on every member, but in the message that carries it.

**Files are not sent in channels yet.** A file waits with its sender until its recipient pulls it; in a space, every member allowed to read the channel would pull it, from whichever member holds it, and that is decided on its own.

## Consequences

- Moving between a conversation and a channel changes nothing in how a message is written, answered or edited.
- The interface offers no attachment in a channel until files in spaces are decided.
