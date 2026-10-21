# 0041 — Moderators mute members and delete their messages

## Status

Accepted

## Date

2026-10-17

## Context

Removing a member is the only moderation a space has ([ADR 0038](0038-a-space-has-three-ranks-and-roles-that-open-channels.md)). A member who writes what should not stay, or writes too much, is either removed or left alone. Whatever else a moderator does has to travel as everything in a space does: signed, and applied by every member alike ([ADR 0013](0013-a-space-is-replicated-by-its-members.md)).

## Decision

**A moderator is the owner or an administrator.** A moderator moderates those of a lower rank: an administrator moderates members; the owner moderates members and administrators.

**Muting is an operation**, `mute-member`, undone by `unmute-member`, made by a moderator about someone they moderate. A muted member still reads every channel open to them, and writes in none: every member refuses their new entries, as it refuses those of anybody who may not write in a channel. **A mute lasts until it is undone**; it has no term, since a term would hang on clocks that disagree.

**A moderator deletes another's message** with a deletion entry of their own, signed by them, in the message's channel. It counts if, when it arrives, its author is a moderator of the message's author. Every member then wipes the message's text, as it does when an author deletes their own ([ADR 0034](0034-a-message-is-edited-and-deleted-by-its-author.md)); it is shown as deleted by a moderator.

## Consequences

- A member muted keeps what they read, and is heard again once unmuted; what they wrote while muted was refused, and is lost.
- A moderator's deletion, like an author's, cannot take a message back from a member who already read it.
- Muting and deleting are as visible to members as every other operation and entry in a space.
