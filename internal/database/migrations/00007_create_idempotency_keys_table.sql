-- +goose Up

-- An idempotency key lets a caller retry a creation after a timeout without
-- risking a second resource. The client generates the key, Convia remembers
-- what the first request produced, and a repeat of the same request is
-- answered with that original response rather than performed again.
--
-- The behavior this table serves is specified in docs/api-compatibility.md.
--
-- Invariants encoded here:
--   * a key names one attempt within one scope, so two callers cannot collide
--   * only the digest of the request is kept, never the request itself
--   * a row is either in progress or finished, never half of each
--   * a stored status is one a server could actually have sent
--   * a key cannot expire before it was created
CREATE TABLE idempotency_keys (
    scope TEXT NOT NULL,
    key TEXT NOT NULL,
    request_digest TEXT NOT NULL,
    response_status INTEGER,
    response_headers JSONB,
    response_body BYTEA,
    created_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    completed_at TIMESTAMPTZ,

    PRIMARY KEY (scope, key),

    CONSTRAINT idempotency_keys_scope_length CHECK (char_length(scope) BETWEEN 1 AND 128),
    CONSTRAINT idempotency_keys_key_length CHECK (char_length(key) BETWEEN 1 AND 255),

    -- The digest is a SHA-256 of the method, path, and body. Storing the
    -- fingerprint rather than the request means a body an application chose --
    -- room names, metadata -- is not retained a second time here.
    CONSTRAINT idempotency_keys_digest_format CHECK (request_digest ~ '^[0-9a-f]{64}$'),

    CONSTRAINT idempotency_keys_status_range
        CHECK (response_status IS NULL OR response_status BETWEEN 100 AND 599),

    -- A row that claimed to be finished without a response to replay would
    -- answer a retry with nothing, which is worse than performing it again.
    CONSTRAINT idempotency_keys_response_complete CHECK (
        (completed_at IS NULL AND response_status IS NULL AND response_body IS NULL)
        OR (completed_at IS NOT NULL AND response_status IS NOT NULL AND response_body IS NOT NULL)
    ),

    CONSTRAINT idempotency_keys_expiry_order CHECK (expires_at > created_at)
);

-- An expired key is reclaimed by the request that reuses it, so the ordinary
-- path never scans. This index exists for the bulk removal of keys nobody
-- comes back for, which belongs to the retention job that also owes erasure to
-- users and rooms.
CREATE INDEX idempotency_keys_expires_at_idx ON idempotency_keys (expires_at);

-- +goose Down

DROP TABLE idempotency_keys;
