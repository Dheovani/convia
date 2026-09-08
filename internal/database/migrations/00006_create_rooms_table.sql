-- +goose Up

-- A room is a place an application's people meet. It is durable and reusable:
-- the room outlives any single conversation held in it, which is what separates
-- it from the ephemeral call that will occupy it in M09.
--
-- Nothing here names a media provider. A room is a Convia concept that the
-- media plane will later be asked to realize, never the other way round.
--
-- Invariants encoded here:
--   * every room belongs to exactly one application, enforced by a foreign key
--   * an alias, when present, names exactly one room within its application,
--     and stays taken while the room is deleted and awaiting erasure
--   * metadata is a JSON object, never a scalar or an array
--   * capacity, when set, is a positive bound rather than an unchecked number
--   * the lifecycle is limited to the states Convia knows how to serve
--   * a row can never claim it was updated before it existed
CREATE TABLE rooms (
    id TEXT PRIMARY KEY,
    application_id TEXT NOT NULL REFERENCES applications (id),
    alias TEXT,
    name TEXT NOT NULL,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    max_participants INTEGER,
    status TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,

    CONSTRAINT rooms_id_format CHECK (id ~ '^room_[A-Z2-7]{26}$'),
    CONSTRAINT rooms_alias_length CHECK (alias IS NULL OR char_length(alias) BETWEEN 1 AND 120),
    CONSTRAINT rooms_name_length CHECK (char_length(name) BETWEEN 1 AND 120),
    CONSTRAINT rooms_metadata_object CHECK (jsonb_typeof(metadata) = 'object'),
    CONSTRAINT rooms_max_participants_positive
        CHECK (max_participants IS NULL OR max_participants BETWEEN 1 AND 1000),
    CONSTRAINT rooms_status_allowed CHECK (status IN ('open', 'closed', 'deleted')),
    CONSTRAINT rooms_updated_at_order CHECK (updated_at >= created_at)
);

-- An alias names one room for the life of the room, including while it is
-- deleted and awaiting erasure. Keeping it reserved is deliberate: a client
-- that cached an alias must never find it silently pointing at a different
-- room. A room created without an alias is unconstrained, because NULLs do not
-- collide in a unique index, which is what makes anonymous rooms possible.
CREATE UNIQUE INDEX rooms_application_alias_key
    ON rooms (application_id, alias)
    WHERE alias IS NOT NULL;

-- Listing is always scoped to one application and pages newest first.
CREATE INDEX rooms_application_created_at_id_idx
    ON rooms (application_id, created_at DESC, id DESC);

-- Filtering a listing by lifecycle state is the one filter the API offers, so
-- it is the one that must not scan.
CREATE INDEX rooms_application_status_created_at_id_idx
    ON rooms (application_id, status, created_at DESC, id DESC);

-- +goose Down

DROP TABLE rooms;
