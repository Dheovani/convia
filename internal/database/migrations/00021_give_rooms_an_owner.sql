-- +goose Up

-- A room a person opens has an owner, and somebody can be kept out of it.
--
-- `00018` gave membership no role, on the ground that a room member held no
-- power a role could name. The product owner decided otherwise: whoever opens a
-- room owns it, and the owner moderates it -- removes and bans people, takes
-- down what others said, and renames, closes, reopens and deletes the room. See
-- docs/adr/0013.
--
-- Invariants encoded here:
--   * only a room a person opened has an owner; an application's rooms stay the
--     application's to manage
--   * an owner is always a member of the room they own: the pair is a foreign
--     key into room_members, checked at commit so that a room and its first
--     member can be written together, and cleared if the membership goes
--   * a person is banned from a room at most once
--   * only a withdrawn message says who withdrew it

ALTER TABLE rooms ADD COLUMN personal BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE rooms ALTER COLUMN personal DROP DEFAULT;

-- Which existing rooms a person opened was never recorded, so it is recognized
-- by what only such a room looks like: it belongs to Convia's own application,
-- the only one people sign in to, and carries none of the attributes a person
-- cannot set.
UPDATE rooms SET personal = true
WHERE application_id = 'app_CONVIAAAAAAAAAAAAAAAAAAAAA'
  AND alias IS NULL
  AND metadata = '{}'::jsonb
  AND max_participants IS NULL;

ALTER TABLE rooms ADD COLUMN owner_user_id TEXT;

-- The owner is whoever opened the room: the member who joined in the instant it
-- was created, because a room and its first member are written together. If
-- they have left, the room passes to its longest-standing active member who
-- signs in here, which is the rule for an owner who leaves. A room left with
-- only visitors from other installations has no owner.
UPDATE rooms SET owner_user_id = coalesce(
    (SELECT member.user_id
     FROM room_members AS member
     WHERE member.room_id = rooms.id AND member.created_at = rooms.created_at
     ORDER BY member.user_id
     LIMIT 1),
    (SELECT member.user_id
     FROM room_members AS member
     JOIN users ON users.id = member.user_id AND users.status = 'active'
     JOIN accounts ON accounts.user_id = member.user_id
     WHERE member.room_id = rooms.id
     ORDER BY member.created_at, member.user_id
     LIMIT 1))
WHERE personal;

ALTER TABLE rooms ADD CONSTRAINT rooms_owner_only_when_personal
    CHECK (owner_user_id IS NULL OR personal);

ALTER TABLE rooms ADD CONSTRAINT rooms_owner_is_member
    FOREIGN KEY (id, owner_user_id) REFERENCES room_members (room_id, user_id)
    ON DELETE SET NULL (owner_user_id)
    DEFERRABLE INITIALLY DEFERRED;

-- A ban outlives the membership it ended, which is the point of it: a removed
-- person can be brought back by anybody, and a banned one by nobody until the
-- owner lifts it.
CREATE TABLE room_bans (
    application_id TEXT NOT NULL REFERENCES applications (id),
    room_id TEXT NOT NULL REFERENCES rooms (id),
    user_id TEXT NOT NULL REFERENCES users (id),
    created_at TIMESTAMPTZ NOT NULL,

    PRIMARY KEY (room_id, user_id)
);

-- A withdrawal an author made and one a room's owner made read differently to
-- everybody in the room, so the tombstone keeps which it was, without saying
-- which person. Erasure clears a message without either and leaves it unset.
ALTER TABLE messages ADD COLUMN deleted_by TEXT;

UPDATE messages SET deleted_by = 'author'
WHERE deleted_at IS NOT NULL
  AND (author_user_id IS NOT NULL OR author_invitation_id IS NOT NULL);

ALTER TABLE messages ADD CONSTRAINT messages_deleted_by_allowed
    CHECK (deleted_by IN ('author', 'owner'));

ALTER TABLE messages ADD CONSTRAINT messages_deleted_by_only_when_deleted
    CHECK (deleted_by IS NULL OR deleted_at IS NOT NULL);

-- +goose Down

ALTER TABLE messages DROP COLUMN deleted_by;
DROP TABLE room_bans;
ALTER TABLE rooms DROP COLUMN owner_user_id;
ALTER TABLE rooms DROP COLUMN personal;
