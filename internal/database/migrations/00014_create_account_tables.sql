-- +goose Up

-- An account is a person who can sign in to Convia's own product.
--
-- This is the fourth thing that authenticates to Convia, and the first that is
-- a human rather than a program. The three that came before -- an application
-- key, an operator key, an invitation -- are all held by software and all
-- carry a secret Convia generated. A person chooses their own, which is the
-- whole reason this table stores a password digest rather than a SHA-256 one:
-- a secret with roughly 130 bits of entropy cannot be searched, and a password
-- can, so the same reasoning that made a fast digest right for a key makes a
-- slow one necessary here.
--
-- An account is deliberately not a row in `users`. A user is one application's
-- view of one of its people and holds no credentials, by design; giving it a
-- password would make every tenant's directory a place where secrets live.
-- What links the two is `user_id`: the row in the first-party application that
-- represents this person, so rooms, calls, participants, and presence keep
-- working through the domains that already exist.
--
-- Invariants encoded here:
--   * an email identifies exactly one account, whatever its lifecycle state,
--     so a deleted account does not free its address for somebody else
--   * the password is never stored, only an argon2id digest, whose parameters
--     travel inside the encoded string so a future cost increase can be
--     applied per account rather than to everybody at once
--   * an account names the user row it corresponds to, and that row is in the
--     first-party application, which is a deployment-level fact rather than
--     one this table can enforce
CREATE TABLE accounts (
    id TEXT PRIMARY KEY,
    email TEXT NOT NULL,
    password_digest TEXT NOT NULL,
    display_name TEXT NOT NULL,
    user_id TEXT NOT NULL REFERENCES users (id),
    status TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,

    CONSTRAINT accounts_id_format CHECK (id ~ '^acc_[A-Z2-7]{26}$'),
    CONSTRAINT accounts_email_length CHECK (char_length(email) BETWEEN 3 AND 320),
    CONSTRAINT accounts_email_lowercase CHECK (email = lower(email)),
    CONSTRAINT accounts_password_digest_present CHECK (char_length(password_digest) BETWEEN 1 AND 512),
    CONSTRAINT accounts_display_name_length CHECK (char_length(display_name) BETWEEN 1 AND 120),
    CONSTRAINT accounts_status_allowed CHECK (status IN ('active', 'suspended', 'deleted')),
    CONSTRAINT accounts_updated_at_order CHECK (updated_at >= created_at)
);

-- Signing in reads one row by email on every attempt, and it is the only query
-- on this table that runs on an unauthenticated request. Unique rather than
-- merely indexed because the address is the identity: two accounts sharing one
-- would make "which of them did you mean" a question sign-in cannot answer.
CREATE UNIQUE INDEX accounts_email_idx ON accounts (email);

-- The operator listing, which pages newest first and is not scoped to a tenant.
CREATE INDEX accounts_created_at_id_idx ON accounts (created_at DESC, id DESC);

-- A session is one browser holding one account's authority.
--
-- The secret here is Convia's own -- generated, not chosen -- so it goes back
-- to a SHA-256 digest for the reason M07-004 records: roughly 130 bits of
-- entropy cannot be searched, and a slow digest would only add latency to
-- every authenticated request.
--
-- There is no status column. Revocation and expiry are timestamps, so the
-- lifecycle state is derived from the facts and expiry needs no scheduled job
-- to take effect -- the same choice `credentials` and `operator_credentials`
-- already made, for the same reason.
--
-- Invariants encoded here:
--   * a session belongs to exactly one account and dies with it
--   * two deadlines, and they mean different things: `absolute_expires_at` is
--     when the session ends however active it has been, and `last_seen_at`
--     is what an idle timeout is measured against
--   * `last_seen_at` never precedes creation, so an idle window cannot be
--     computed from a moment before the session existed
CREATE TABLE sessions (
    id TEXT PRIMARY KEY,
    account_id TEXT NOT NULL REFERENCES accounts (id) ON DELETE CASCADE,
    secret_hash BYTEA NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    last_seen_at TIMESTAMPTZ NOT NULL,
    absolute_expires_at TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ,

    CONSTRAINT sessions_id_format CHECK (id ~ '^ses_[A-Z2-7]{26}$'),
    CONSTRAINT sessions_secret_hash_length CHECK (octet_length(secret_hash) = 32),
    CONSTRAINT sessions_seen_after_creation CHECK (last_seen_at >= created_at),
    CONSTRAINT sessions_expires_after_creation CHECK (absolute_expires_at > created_at),
    CONSTRAINT sessions_revoked_after_creation CHECK (revoked_at IS NULL OR revoked_at >= created_at)
);

-- Signing out everywhere, and revoking every sibling when a password changes,
-- both read a person's sessions by account. Authentication itself reads one row
-- by identifier, which the primary key already serves.
CREATE INDEX sessions_account_idx ON sessions (account_id);

-- +goose Down

DROP TABLE sessions;
DROP TABLE accounts;
