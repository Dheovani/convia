# ADR 0015 — An owner names moderators, and may hand a room over

**Status:** Accepted
**Date:** 2026-09-16
**Milestone:** M18 — Standalone Web Application

## Context

[ADR 0013](0013-a-room-a-person-opens-has-an-owner.md) gave a room a person opens exactly one owner, changed only by succession, and listed more moderators and a deliberate handover as not built. A room with one moderator has nobody to act while that person is away, and an owner who wants to step back can only leave the room to pass it on.

The product owner decided (`M18-030`):

- **the owner names moderators**, who remove and ban people, lift bans, take down what others said, and moderate the room's calls;
- **a moderator does not act on the room itself**: renaming, closing, reopening and deleting stay the owner's, and so do naming moderators and handing the room over;
- **the owner may hand the room to another member**, and stays in it as a member.

## Decision

### A moderator is a flag on the membership

`room_members.moderator`. It cannot outlive the place it belongs to: somebody who leaves stops moderating, and somebody added back starts as a member. A table of moderators would have needed its own rule to clear it whenever a membership ends, in every path that ends one.

The owner is never flagged. The flag is cleared on whoever becomes the owner, by a handover or by succession, because the owner already does everything a moderator does.

### Only somebody who could own the room can moderate it

The rule is the owner's: an active member with an account on this installation. A visitor never moderates, for the reason a visitor never owns: moderation stays with the room's home installation. Naming anybody else is `404`, as adding somebody who cannot be added is.

### A moderator does not act on the owner or another moderator

Otherwise two moderators could remove each other, and a moderator could remove the owner, who can only be removed by the application. A moderator asking to is refused with `403`, because they know the person is in the room.

A member asking for a moderator's act is refused with `403` too, with its own error, so that the message says a moderator could do it.

### Succession prefers a moderator

When the owner goes, the room passes to its longest-standing moderator, and only then to its longest-standing member. The owner already trusted a moderator to act on the room's people, which is the nearest thing to a choice the owner made.

### A handover checks the owner under the room's lock

The update names the current owner in its condition, so of two handovers at once the second finds the owner changed and is refused. A test runs them concurrently.

### Roles are announced

`room.member_role_changed` names the room, and carries the person and what they are now. A handover announces both people. It needs `members:read`, as membership changes do, and a person receives it for the rooms they are in.

### A moderator's removal reads as the owner's

`messages.deleted_by` stays `author` or `owner`. It records whether the author took their words back, never which person moderated, and a moderator taking something down is the room's moderation, as the owner's is.

### A call's role is decided when somebody joins

The owner and the moderators join a room's call as its moderators. Naming somebody during a call takes effect from their next join, because a seat's role is set when it is issued.

## Consequences

- ADR 0013's "there is one owner" consequence no longer holds.
- A moderator reads the room's bans, because lifting a ban needs them.
- An application's rooms still have neither owner nor moderators.
- Unnaming a moderator does not take away a call seat they already hold as a moderator.
