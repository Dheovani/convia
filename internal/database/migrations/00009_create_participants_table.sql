-- +goose Up

-- A participant is one person's presence in one call. It is the third of three
-- ideas M10 keeps apart:
--
--   * room membership -- who belongs to a place over time. Convia does not
--     model this. It holds no credentials for an application's people, so it
--     could not enforce a policy about who belongs; the application already
--     knows. See docs/rooms.md.
--   * an invitation -- permission to join that has not been used yet. It
--     arrives with the join sessions of M13, where the party presenting it is
--     no longer the party that granted it.
--   * participation -- who actually joined, and what happened to them. That is
--     this table.
--
-- Nothing here names a media provider. Whether someone may join is a Convia
-- decision; handing them a media token is a later and separate one.
--
-- Invariants encoded here:
--   * every participant belongs to one application, one call, and one user
--   * a user is in a call at most once at a time, settled by the index below
--   * a participant is present or gone, and the columns agree with which
--   * a departure that was forced records who forced it
--   * nobody leaves before they arrived
CREATE TABLE participants (
    id TEXT PRIMARY KEY,
    application_id TEXT NOT NULL REFERENCES applications (id),
    call_id TEXT NOT NULL REFERENCES calls (id),
    user_id TEXT NOT NULL REFERENCES users (id),
    role TEXT NOT NULL,
    status TEXT NOT NULL,
    removed_by TEXT,
    removed_by_participant_id TEXT REFERENCES participants (id),
    removal_reason TEXT,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    left_at TIMESTAMPTZ,

    CONSTRAINT participants_id_format CHECK (id ~ '^part_[A-Z2-7]{26}$'),

    -- Two roles, and the difference between them is one Convia can enforce:
    -- a moderator may remove someone and change a role. Roles about what a
    -- person may do with media belong to the media plane, which does not exist.
    CONSTRAINT participants_role_allowed CHECK (role IN ('moderator', 'member')),

    -- Three states, all reachable. `removed` is not a reason for `left`: it is
    -- terminal with a policy of its own, because someone a moderator removed
    -- must not simply rejoin.
    CONSTRAINT participants_status_allowed CHECK (status IN ('joined', 'left', 'removed')),

    CONSTRAINT participants_removed_by_allowed
        CHECK (removed_by IS NULL OR removed_by IN ('application', 'operator', 'participant')),

    CONSTRAINT participants_removal_reason_length
        CHECK (removal_reason IS NULL OR char_length(removal_reason) BETWEEN 1 AND 200),

    -- A participant that claimed to be gone without saying when, or one still
    -- present carrying a removal, would make a roster unreadable. The three
    -- shapes a row may take are spelled out rather than trusted to the
    -- application.
    CONSTRAINT participants_lifecycle_consistent CHECK (
        (status = 'joined'
            AND left_at IS NULL AND removed_by IS NULL
            AND removed_by_participant_id IS NULL AND removal_reason IS NULL)
        OR (status = 'left'
            AND left_at IS NOT NULL AND removed_by IS NULL
            AND removed_by_participant_id IS NULL AND removal_reason IS NULL)
        OR (status = 'removed' AND left_at IS NOT NULL AND removed_by IS NOT NULL)
    ),

    -- A removal attributed to a participant has to name which one, and one
    -- attributed to the application or an operator must not name any.
    CONSTRAINT participants_remover_identified CHECK (
        (removed_by = 'participant' AND removed_by_participant_id IS NOT NULL)
        OR (removed_by IS DISTINCT FROM 'participant' AND removed_by_participant_id IS NULL)
    ),

    CONSTRAINT participants_updated_at_order CHECK (updated_at >= created_at),
    CONSTRAINT participants_left_at_order CHECK (left_at IS NULL OR left_at >= created_at)
);

-- One person is in one conversation once. This is what makes a reconnection
-- return the participant that is already there instead of creating a second:
-- the client that dropped and came back is the same person, and a roster
-- showing them twice would be wrong in a way users notice immediately.
-- Departed rows do not participate, so a person may leave and come back later
-- and the call keeps both stints in its history.
CREATE UNIQUE INDEX participants_call_user_present_key
    ON participants (call_id, user_id)
    WHERE status = 'joined';

-- A call's roster, newest first, which is the listing every caller reads.
CREATE INDEX participants_call_created_at_id_idx
    ON participants (call_id, created_at DESC, id DESC);

-- Filtering a roster by state is the one filter the API offers, so it is the
-- one that must not scan. It also serves the capacity count taken on every
-- join.
CREATE INDEX participants_call_status_created_at_id_idx
    ON participants (call_id, status, created_at DESC, id DESC);

-- +goose Down

DROP TABLE participants;
