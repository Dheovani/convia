-- +goose Up

-- An invitation is permission to join a call that has not been used yet. It is
-- the second of the three ideas M10 keeps apart, and the last of them to be
-- built, because it only became enforceable once M13 existed.
--
-- The reason it waited is the reason it is worth having. An invitation is only
-- authorization when the party presenting it is not the party that granted it.
-- Before M13 the application created one with its key and joined someone with
-- the same key, so Convia would have been checking the application's homework
-- against itself. Now the invitation is a credential a *client* presents, and
-- expiry and revocation are rules Convia enforces against a party that cannot
-- simply choose to ignore them.
--
-- The secret is stored only as a digest, exactly as an application key and an
-- operator key are. Convia cannot show an invitation again after issuing it.
--
-- Invariants encoded here:
--   * every invitation belongs to one application, one call, and one person
--   * an invitation always has an expiry, and it is after it was created
--   * a redeemed invitation names the participation it produced
--   * declining and redeeming are mutually exclusive
CREATE TABLE invitations (
    id TEXT PRIMARY KEY,
    application_id TEXT NOT NULL REFERENCES applications (id),
    call_id TEXT NOT NULL REFERENCES calls (id),
    user_id TEXT NOT NULL REFERENCES users (id),
    role TEXT NOT NULL,
    secret_digest BYTEA NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    redeemed_at TIMESTAMPTZ,
    participant_id TEXT REFERENCES participants (id),
    declined_at TIMESTAMPTZ,
    revoked_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,

    CONSTRAINT invitations_id_format CHECK (id ~ '^inv_[A-Z2-7]{26}$'),

    -- The role the invitation confers when it is redeemed. The vocabulary is
    -- the participants' own, because redeeming produces a participation and a
    -- second vocabulary would be a second thing to keep right.
    CONSTRAINT invitations_role_allowed CHECK (role IN ('moderator', 'member')),

    -- SHA-256, the one digest internal/secret produces.
    CONSTRAINT invitations_secret_digest_length CHECK (octet_length(secret_digest) = 32),

    -- Every invitation expires. An invitation without an expiry is a permanent
    -- way into a conversation, which is the thing this table exists to avoid.
    CONSTRAINT invitations_expires_at_order CHECK (expires_at > created_at),

    -- A redemption names what it produced, and an unredeemed invitation names
    -- nothing. This is what lets an application see which participation came
    -- from which invitation without keeping its own ledger.
    CONSTRAINT invitations_redemption_identified CHECK (
        (redeemed_at IS NULL AND participant_id IS NULL)
        OR (redeemed_at IS NOT NULL AND participant_id IS NOT NULL)
    ),

    -- Somebody who declined did not also join. Declining is the invitee saying
    -- no, and it is terminal.
    CONSTRAINT invitations_declined_or_redeemed CHECK (
        declined_at IS NULL OR redeemed_at IS NULL
    ),

    CONSTRAINT invitations_updated_at_order CHECK (updated_at >= created_at),
    CONSTRAINT invitations_redeemed_at_order CHECK (redeemed_at IS NULL OR redeemed_at >= created_at),
    CONSTRAINT invitations_declined_at_order CHECK (declined_at IS NULL OR declined_at >= created_at),
    CONSTRAINT invitations_revoked_at_order CHECK (revoked_at IS NULL OR revoked_at >= created_at)
);

-- There is deliberately no uniqueness on (call_id, user_id). An application may
-- send a second invitation because the first was lost, and refusing that would
-- make a lost link unrecoverable. Two invitations for the same person redeem
-- into the same participation, because joining is idempotent by the person.

-- An application's invitations for one call, newest first, which is the listing
-- a caller reads.
CREATE INDEX invitations_call_created_at_id_idx
    ON invitations (call_id, created_at DESC, id DESC);

-- The same listing across an application, for the tenant-wide page.
CREATE INDEX invitations_application_created_at_id_idx
    ON invitations (application_id, created_at DESC, id DESC);

-- +goose Down

DROP TABLE invitations;
