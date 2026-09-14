# ADR 0013 — A room a person opens has an owner

**Status:** Accepted
**Date:** 2026-09-14
**Milestone:** M18 — Standalone Web Application

## Context

`M18-003` gave room membership no role. Any member could add somebody they already shared a room with and could leave; only the application could remove somebody, rename, close or delete a room. That was decided without the product owner, and it left a room nobody could moderate: a person who opened a room for friends had no way to remove somebody unwanted, and calls held in it would have had nobody to moderate them either.

The product owner decided the shape before anything was built:

- **whoever opens a room owns it**;
- **the owner moderates it**: they moderate its calls, remove members, ban them, take down what others said, and rename, close, reopen and delete the room;
- **a removal is not a ban**: anybody in the room may add a removed person back, while a banned person comes back only once the owner lifts the ban;
- **an owner who leaves passes the room to its longest-standing member**, and only members who sign in on the room's own installation inherit it;
- **existing rooms get the owner that rule implies**;
- **a message the owner takes down says so**.

## Decision

### Ownership is a column on the room, and the database keeps it true

`rooms.owner_user_id`, with `(id, owner_user_id)` a foreign key into `room_members`. The key is checked at commit, so a room and its first member are written together, and it clears the owner when the owner's membership goes, so nobody can be left owning a room they are not in.

A role on every membership was the alternative. It would allow a room with two owners, or with a member marked as owner after leaving, in states the database could not refuse.

### Succession happens in the transaction that removed the owner

Every change to who is in a room, or kept out of it, takes the room's row lock first, and then gives an ownerless room to its longest-standing active member who has an account on this installation. Leaving, removal by the application, a ban and erasure all pass a room on the same way, because they all go through the same statement.

**A visitor never inherits.** Moderation stays with the people whose installation the room lives on; letting a visitor hold it would mean carrying an owner's powers across the signed surface between installations. A room left with only visitors has no owner until somebody who signs in here is added, and they take it.

### Only a room a person opened has an owner

`rooms.personal` marks it. An application's rooms stay the application's to manage; without the mark, the first local member added to one of them would become its owner.

### A ban is a row that outlives the membership it ended

`room_bans`. Adding somebody and accepting an invitation both check it **under the room's lock**, so a ban and an addition racing each other cannot both succeed; a test runs them concurrently and fails without the lock.

The application's own `AddMember` does not consult bans. A ban is a room owner's decision about people, and an application acting on its own rooms keeps the authority it always had.

A member trying to add somebody banned is told the one answer every refusal to add gives, so members do not learn the owner's decision. Inviting somebody banned is refused with `403`, because the inviter named that person deliberately and a link that can never be used helps nobody.

### A member is told `403`, and anybody else `404`

A member already knows the room exists, so refusing them an owner's act reveals nothing. Somebody outside the room is told it is not there, as on every route a person reaches.

### A tombstone records who took the message down

`messages.deleted_by` is `author` or `owner`, and never names the person. Erasure clears a message without setting it.

### Existing rooms

Which rooms a person opened was never recorded, so `00021` recognizes them by what only such a room looks like: it belongs to Convia's own application, the only one people sign in to, and has no alias, metadata or capacity. Its owner is the member who joined in the instant it was created, or, if they have left, whoever the succession rule would have chosen.

## Consequences

- `00018`'s "there is no role here", and `M18-003`'s "no removing anybody else", are reversed. `docs/rooms.md` says so where they were.
- Renaming, closing and deleting a room announce nothing yet. The owner's page updates at once; other members' sidebars update on their next read.
- Removing somebody and banning them are the same `room.member_removed` event, because membership records no actor.
- Members cannot remove or ban the owner. Only the application can remove an owner, and the room passes on.
- A suspended owner stays the owner. Suspension is reversible, and they cannot act while it lasts.
- There is one owner. More moderators, and handing a room over deliberately, are not built.
- The rooms store reads the `accounts` table to tell who signs in here. It is the one place it does, and the reason is written beside it.
