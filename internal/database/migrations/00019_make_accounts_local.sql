-- +goose Up

-- An account belongs to its installation and to the person who made it.
--
-- `00014` modelled an account as something an operator created, identified by
-- an email address, with a password Convia generated and handed over out of
-- band. That shape assumed a mailer and an administrator, and a person who
-- installs Convia on their own machine has neither. Somebody now registers
-- from the sign-in page with a username and a password they choose.
--
-- **The accounts from `00014` cannot be carried across.** Their identifier
-- becomes the fingerprint of a key, and the key has to be sealed by the
-- account's password, which Convia never held. There is no way to derive
-- either from what the old rows contain, so the tables are replaced rather than
-- altered. The user rows those accounts pointed at stay where they are, with
-- the rooms and messages that name them: nothing signs in as them any more,
-- and erasing what somebody said is a decision for erasure, not for a schema
-- change.
--
-- Invariants encoded here:
--   * a username names exactly one account on this installation, whatever its
--     lifecycle state, so a deleted account does not free its name for
--     somebody else to receive invitations meant for the first person
--   * a username is lowercase ASCII, so two names cannot look identical while
--     being different
--   * the identifier is the fingerprint of the stored public key; the shape is
--     enforced here and the derivation by the code that writes it
--   * the password is never stored, only an argon2id digest, and the private
--     key is never stored, only sealed by a key derived from the password --
--     each carrying its own parameters
DROP TABLE sessions;
DROP TABLE accounts;

CREATE TABLE accounts (
    id TEXT PRIMARY KEY,
    username TEXT NOT NULL,
    password_digest TEXT NOT NULL,
    public_key BYTEA NOT NULL,
    sealed_private_key TEXT NOT NULL,
    user_id TEXT NOT NULL REFERENCES users (id),
    status TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,

    CONSTRAINT accounts_id_format CHECK (id ~ '^acc_[A-Z2-7]{26}$'),
    CONSTRAINT accounts_username_format CHECK (username ~ '^[a-z0-9][a-z0-9._-]{2,31}$'),
    CONSTRAINT accounts_password_digest_present CHECK (char_length(password_digest) BETWEEN 1 AND 512),
    CONSTRAINT accounts_public_key_length CHECK (octet_length(public_key) = 32),
    CONSTRAINT accounts_sealed_private_key_present CHECK (char_length(sealed_private_key) BETWEEN 1 AND 512),
    CONSTRAINT accounts_status_allowed CHECK (status IN ('active', 'suspended', 'deleted')),
    CONSTRAINT accounts_updated_at_order CHECK (updated_at >= created_at)
);

-- Signing in reads one row by username on every attempt, and it is the only
-- query on this table that runs on an unauthenticated request. Unique rather
-- than merely indexed because the name is what a person types to sign in.
CREATE UNIQUE INDEX accounts_username_idx ON accounts (username);

-- Unchanged from `00014`: a session is one browser holding one account's
-- authority. It is recreated only because it depends on the table above.
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

CREATE INDEX sessions_account_idx ON sessions (account_id);

-- +goose Down

-- Reverting restores the shape `00014` created, empty. The accounts made under
-- this migration cannot become operator accounts either: they have no email.
DROP TABLE sessions;
DROP TABLE accounts;

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

CREATE UNIQUE INDEX accounts_email_idx ON accounts (email);
CREATE INDEX accounts_created_at_id_idx ON accounts (created_at DESC, id DESC);

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

CREATE INDEX sessions_account_idx ON sessions (account_id);
