-- +goose Up

-- How far somebody has read in a room.
--
-- **This is durable, and presence is not, and the difference is the whole
-- decision.** Presence is a claim with an expiry, and the honest description of
-- it is "this was true a moment ago". Read state is the opposite: it is a fact
-- about something a person did, it does not decay, and a badge that reset to
-- unread because a laptop closed would be wrong in a way people notice
-- immediately. So it lives in PostgreSQL beside the messages it points at,
-- rather than in the Redis that holds presence.
--
-- It is a **position, not a set**. Convia records the furthest sequence read,
-- not which messages were read. Recording the set would be the same size as the
-- history and would grow with every reader, and nobody has ever needed to know
-- that message 12 was read while 11 was not.
--
-- Only users have read state. A guest has no Convia user, their stint is one
-- call, and they do not come back to find a badge waiting -- so the row is keyed
-- on a user and there is deliberately no guest equivalent.
--
-- Invariants encoded here:
--   * one row per person per room, enforced by the primary key
--   * a position is a real one, never zero or negative
--   * the room and the person both belong to the application that owns the row
CREATE TABLE room_read_state (
  application_id TEXT NOT NULL REFERENCES applications (id),
  room_id TEXT NOT NULL REFERENCES rooms (id),
  user_id TEXT NOT NULL REFERENCES users (id),

  -- The furthest sequence this person has read in this room.
  sequence BIGINT NOT NULL,

  updated_at TIMESTAMPTZ NOT NULL,

  PRIMARY KEY (room_id, user_id),

  CONSTRAINT room_read_state_sequence_positive CHECK (sequence >= 1)
);

-- Somebody's rooms with something new in them, which is what a sidebar asks
-- for. Reading a person's whole read state is the one query that is not scoped
-- to a single room, so it is the one that must not scan.
CREATE INDEX room_read_state_user_idx ON room_read_state (application_id, user_id);

-- +goose Down

DROP TABLE room_read_state;
