-- +goose Up

-- An application is a tenant of Convia: the standalone product itself, or an
-- external product such as an online learning platform that integrates through
-- the public API. Every tenant-scoped resource added later references this table.
--
-- Invariants encoded here:
--   * the public identifier is opaque, prefixed, and never a sequential key
--   * a name is always present and bounded
--   * the lifecycle is limited to the states Convia knows how to serve
--   * a row can never claim it was updated before it existed
CREATE TABLE applications (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    status TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,

    CONSTRAINT applications_id_format CHECK (id ~ '^app_[A-Z2-7]{26}$'),
    CONSTRAINT applications_name_length CHECK (char_length(name) BETWEEN 1 AND 120),
    CONSTRAINT applications_status_allowed CHECK (status IN ('active', 'suspended', 'deleted')),
    CONSTRAINT applications_updated_at_order CHECK (updated_at >= created_at)
);

-- +goose Down

DROP TABLE applications;
