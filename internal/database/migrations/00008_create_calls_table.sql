-- +goose Up

-- A call is the occasion; the room is the place. A room outlives every
-- conversation held in it, which is what makes the two separate resources
-- rather than one.
--
-- Nothing here names a media provider. A call is a Convia concept that the
-- media plane will later be asked to realize, never the other way round. When
-- that arrives, the provider's session identifier belongs in a column of this
-- table that never reaches the public representation.
--
-- Invariants encoded here:
--   * every call belongs to one application and one room
--   * a room holds at most one call at a time, settled by the index below
--   * a call is either happening or over, and its columns agree with which
--   * an ending records who ended it and when, so history is not guesswork
--   * a call cannot end before it began
CREATE TABLE calls (
    id TEXT PRIMARY KEY,
    application_id TEXT NOT NULL REFERENCES applications (id),
    room_id TEXT NOT NULL REFERENCES rooms (id),
    status TEXT NOT NULL,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    started_by TEXT NOT NULL,
    ended_by TEXT,
    end_reason TEXT,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    ended_at TIMESTAMPTZ,

    CONSTRAINT calls_id_format CHECK (id ~ '^call_[A-Z2-7]{26}$'),
    CONSTRAINT calls_status_allowed CHECK (status IN ('active', 'ended')),
    CONSTRAINT calls_metadata_object CHECK (jsonb_typeof(metadata) = 'object'),

    -- The actor is who caused a transition, not who was talking. Participants
    -- are M10 and are recorded elsewhere.
    CONSTRAINT calls_started_by_allowed CHECK (started_by IN ('application', 'operator')),
    CONSTRAINT calls_ended_by_allowed CHECK (ended_by IS NULL OR ended_by IN ('application', 'operator')),

    CONSTRAINT calls_end_reason_length
        CHECK (end_reason IS NULL OR char_length(end_reason) BETWEEN 1 AND 200),

    -- A call that claimed to be over without saying when, or one still running
    -- that carried an ending, would make history unreadable. The two shapes a
    -- row may take are spelled out rather than trusted to the application.
    CONSTRAINT calls_lifecycle_consistent CHECK (
        (status = 'active' AND ended_at IS NULL AND ended_by IS NULL AND end_reason IS NULL)
        OR (status = 'ended' AND ended_at IS NOT NULL AND ended_by IS NOT NULL)
    ),

    CONSTRAINT calls_updated_at_order CHECK (updated_at >= created_at),
    CONSTRAINT calls_ended_at_order CHECK (ended_at IS NULL OR ended_at >= created_at)
);

-- One conversation at a time in one place. This is the whole of the
-- one-active-call rule: PostgreSQL settles two simultaneous starts, where an
-- application-level check would read, decide, and lose the race in between.
-- Ended calls do not participate, which is what lets a room hold a history.
CREATE UNIQUE INDEX calls_room_active_key
    ON calls (room_id)
    WHERE status = 'active';

-- An application's whole call history, newest first.
CREATE INDEX calls_application_created_at_id_idx
    ON calls (application_id, created_at DESC, id DESC);

-- One room's history, which is the listing an application reads most.
CREATE INDEX calls_room_created_at_id_idx
    ON calls (room_id, created_at DESC, id DESC);

-- Filtering a history by lifecycle state is the one filter the API offers, so
-- it is the one that must not scan.
CREATE INDEX calls_application_status_created_at_id_idx
    ON calls (application_id, status, created_at DESC, id DESC);

-- +goose Down

DROP TABLE calls;
