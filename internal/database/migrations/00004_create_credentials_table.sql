-- +goose Up

-- A credential authenticates one application to Convia's server-to-server API.
--
-- Invariants encoded here:
--   * every credential belongs to exactly one application, enforced by a
--     foreign key, so a credential can never authorize work across tenants
--   * the secret is never stored, only its SHA-256 digest
--   * the identifier is public and the secret is not, so a lookup can be made
--     by identifier before any comparison of secret material
--   * scopes are explicit and never empty, so a credential cannot exist with
--     implicit unrestricted access
--   * revocation and expiry are timestamps rather than a status column: the
--     lifecycle state is derived from them, so it cannot drift from the facts
--     and expiry needs no scheduled job to take effect
CREATE TABLE credentials (
    id TEXT PRIMARY KEY,
    application_id TEXT NOT NULL REFERENCES applications (id),
    name TEXT NOT NULL,
    secret_hash BYTEA NOT NULL,
    scopes TEXT[] NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ,
    revoked_at TIMESTAMPTZ,

    CONSTRAINT credentials_id_format CHECK (id ~ '^cred_[A-Z2-7]{26}$'),
    CONSTRAINT credentials_name_length CHECK (char_length(name) BETWEEN 1 AND 120),
    CONSTRAINT credentials_secret_hash_length CHECK (octet_length(secret_hash) = 32),
    CONSTRAINT credentials_scopes_not_empty CHECK (cardinality(scopes) > 0),
    CONSTRAINT credentials_expires_after_creation CHECK (expires_at IS NULL OR expires_at > created_at),
    CONSTRAINT credentials_revoked_after_creation CHECK (revoked_at IS NULL OR revoked_at >= created_at)
);

-- Authentication looks a credential up by its identifier on every request, so
-- that path must never scan. The primary key already serves it; this index
-- serves the operator-facing listing, which is always scoped to one
-- application and pages newest first.
CREATE INDEX credentials_application_created_at_id_idx
    ON credentials (application_id, created_at DESC, id DESC);

-- +goose Down

DROP TABLE credentials;
