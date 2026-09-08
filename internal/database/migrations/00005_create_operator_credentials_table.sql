-- +goose Up

-- An operator credential authenticates the party that runs Convia itself: the
-- one that creates tenants, suspends them, and issues their first keys.
--
-- This is a separate table from `credentials` on purpose, not a row there with
-- a null application. Every query in the credentials package carries an
-- application in its WHERE clause; if an operator credential could live in
-- that table, a single forgotten predicate would turn an omission into a
-- privilege escalation. Kept apart, the tenant store cannot return one at all.
--
-- Invariants encoded here:
--   * a credential belongs to no application, and no column offers to relate
--     it to one
--   * the secret is never stored, only its SHA-256 digest
--   * scopes are explicit and never empty, so a credential cannot exist with
--     implicit unrestricted authority
--   * revocation and expiry are timestamps rather than a status column, so the
--     lifecycle state is derived from the facts and expiry needs no scheduled
--     job to take effect
CREATE TABLE operator_credentials (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    secret_hash BYTEA NOT NULL,
    scopes TEXT[] NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ,
    revoked_at TIMESTAMPTZ,

    CONSTRAINT operator_credentials_id_format CHECK (id ~ '^oper_[A-Z2-7]{26}$'),
    CONSTRAINT operator_credentials_name_length CHECK (char_length(name) BETWEEN 1 AND 120),
    CONSTRAINT operator_credentials_secret_hash_length CHECK (octet_length(secret_hash) = 32),
    CONSTRAINT operator_credentials_scopes_not_empty CHECK (cardinality(scopes) > 0),
    CONSTRAINT operator_credentials_expires_after_creation
        CHECK (expires_at IS NULL OR expires_at > created_at),
    CONSTRAINT operator_credentials_revoked_after_creation
        CHECK (revoked_at IS NULL OR revoked_at >= created_at)
);

-- Authentication reads one row by identifier on every operator request, which
-- the primary key already serves. This index serves the listing, which pages
-- newest first and is not scoped to any tenant.
CREATE INDEX operator_credentials_created_at_id_idx
    ON operator_credentials (created_at DESC, id DESC);

-- +goose Down

DROP TABLE operator_credentials;
