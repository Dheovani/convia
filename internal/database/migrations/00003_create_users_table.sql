-- +goose Up

-- A user is one application's view of one of its people. Convia never links
-- users across applications: the same human known to two applications is two
-- unrelated Convia users, so Convia cannot become a cross-product identity
-- graph and no application can learn where else a person appears.
--
-- Invariants encoded here:
--   * every user belongs to exactly one application, enforced by a foreign key
--   * an application's external subject resolves to exactly one user
--   * the external subject is opaque to Convia and merely bounded
--   * metadata is a JSON object, never a scalar or an array
--   * the lifecycle mirrors applications: active, suspended, or deleted
CREATE TABLE users (
    id TEXT PRIMARY KEY,
    application_id TEXT NOT NULL REFERENCES applications (id),
    external_subject TEXT NOT NULL,
    display_name TEXT,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    status TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,

    CONSTRAINT users_id_format CHECK (id ~ '^usr_[A-Z2-7]{26}$'),
    CONSTRAINT users_external_subject_length CHECK (char_length(external_subject) BETWEEN 1 AND 255),
    CONSTRAINT users_display_name_length CHECK (display_name IS NULL OR char_length(display_name) BETWEEN 1 AND 120),
    CONSTRAINT users_metadata_object CHECK (jsonb_typeof(metadata) = 'object'),
    CONSTRAINT users_status_allowed CHECK (status IN ('active', 'suspended', 'deleted')),
    CONSTRAINT users_updated_at_order CHECK (updated_at >= created_at)
);

-- One external subject resolves to one user for the life of the mapping,
-- including while the user is deleted and awaiting erasure. Resolving is
-- therefore idempotent, and a subject can never point at two Convia users.
CREATE UNIQUE INDEX users_application_subject_key ON users (application_id, external_subject);

-- Listing is always scoped to one application and pages newest first.
CREATE INDEX users_application_created_at_id_idx ON users (application_id, created_at DESC, id DESC);

-- +goose Down

DROP TABLE users;
