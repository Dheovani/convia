-- +goose Up

-- A message is something somebody said in a room.
--
-- **There is no conversations table, and that is the decision this migration
-- records.** A room is already "the place, the call is the occasion" -- the
-- words are from 00006 -- and a conversation held in a place over time is what
-- a room's messages are. A separate table one-to-one with rooms would be a
-- second name for the same thing, and every read would join it to learn
-- nothing.
--
-- So messages hang off the room, not off the call. Chat has to survive a call
-- ending: the history is what somebody sees when they open a room where
-- nothing is happening, and a call is one afternoon in a place that outlives
-- it.
--
-- Invariants encoded here:
--   * every message belongs to one application, one room, and one author
--   * an author is a user or a guest's invitation, and exactly one of the two
--   * `sequence` orders a room's messages, and no two messages share one
--   * a message that is deleted carries no body, and one that is not, does
--   * nothing claims to have been edited or deleted before it was written
CREATE TABLE messages (
    id TEXT PRIMARY KEY,
    application_id TEXT NOT NULL REFERENCES applications (id),
    room_id TEXT NOT NULL REFERENCES rooms (id),

    -- The ordering of a room's messages, allocated by the database rather than
    -- taken from any instance's clock. Since M16 a deployment is several
    -- instances, and `created_at` is each one's opinion of when it handled a
    -- request; two of those opinions are not the same clock and cannot settle
    -- which of two messages came first. This can, because the unique index
    -- below makes the allocation the one place the question is answered.
    --
    -- It is strictly increasing within a room. It is deliberately **not**
    -- promised to be dense: erasing a message for real, rather than
    -- tombstoning it, leaves a hole, and a client that counted on contiguity
    -- would break the day retention is implemented.
    sequence BIGINT NOT NULL,

    -- An author is one of Convia's users, or a guest identified by nothing but
    -- the invitation they redeemed. This mirrors `participants` exactly,
    -- including the shape of the constraint, because it is the same question
    -- about the same people and answering it two ways would be the drift that
    -- makes rosters and history disagree about who was there.
    author_user_id TEXT REFERENCES users (id),
    author_invitation_id TEXT REFERENCES invitations (id),

    -- NULL once deleted, which is what makes a tombstone a row that no longer
    -- holds what was said rather than a row with a flag beside the text.
    body TEXT,

    created_at TIMESTAMPTZ NOT NULL,
    edited_at TIMESTAMPTZ,
    deleted_at TIMESTAMPTZ,

    CONSTRAINT messages_id_format CHECK (id ~ '^msg_[A-Z2-7]{26}$'),

    CONSTRAINT messages_sequence_positive CHECK (sequence >= 1),

    CONSTRAINT messages_author_identified CHECK (
        (author_user_id IS NOT NULL AND author_invitation_id IS NULL)
        OR (author_user_id IS NULL AND author_invitation_id IS NOT NULL)
    ),

    -- The two shapes a message may take, spelled out rather than left to the
    -- service. A row with a body and a deletion, or a tombstone still carrying
    -- what it was meant to erase, are both states no caller could make sense
    -- of, and neither is reachable from here.
    CONSTRAINT messages_body_matches_lifecycle CHECK (
        (deleted_at IS NULL AND body IS NOT NULL)
        OR (deleted_at IS NOT NULL AND body IS NULL)
    ),

    CONSTRAINT messages_body_length CHECK (body IS NULL OR char_length(body) BETWEEN 1 AND 4000),

    CONSTRAINT messages_edited_at_order CHECK (edited_at IS NULL OR edited_at >= created_at),
    CONSTRAINT messages_deleted_at_order CHECK (deleted_at IS NULL OR deleted_at >= created_at)
);

-- The ordering guarantee, enforced rather than intended.
--
-- Appends take the room's row lock, so two instances writing to one room are
-- serialized and never reach for the same position. This index is therefore
-- not the arbiter of a race -- it is the assertion that the lock is doing its
-- job. It should never fire, and an append that trips it is reported rather
-- than retried, because a retry would paper over an ordering guarantee that had
-- already been broken.
--
-- Allocating the position optimistically, without the lock, was tried first and
-- measured: of sixteen simultaneous appends to one room, nine or ten failed,
-- because every writer that loses a collision re-reads the same highest
-- sequence as every other loser and collides again.
--
-- It also serves every read of a room's history. A B-tree scans backwards as
-- cheaply as forwards, so paging newest-first needs no second index, and the
-- keyset cursor is a position in this index rather than a value compared
-- against one.
CREATE UNIQUE INDEX messages_room_sequence_key ON messages (room_id, sequence);

-- +goose Down

DROP TABLE messages;
