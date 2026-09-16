-- +goose Up

-- A room's owner may name moderators, and may hand the room to somebody else.
--
-- `00021` gave a room a person opened exactly one owner, changed only by
-- succession. The product owner decided a room may also have moderators, who
-- remove and ban people and moderate its calls, but do not rename, close,
-- reopen or delete it; and that the owner may hand the room over deliberately.
-- See docs/adr/0015.
--
-- A moderator is a flag on the membership rather than a table of its own,
-- because it cannot outlive the place it belongs to: somebody who leaves stops
-- moderating, and somebody added back does not start again. The default is
-- false, because a newcomer moderates nothing.
--
-- Handing a room over needs no column: the owner is already one, on the room.

ALTER TABLE room_members ADD COLUMN moderator BOOLEAN NOT NULL DEFAULT false;

-- +goose Down

ALTER TABLE room_members DROP COLUMN moderator;
