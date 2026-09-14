-- +goose Up

-- A room can now have members who live on another installation.
--
-- The room stays where it was created -- its home -- and a person from
-- elsewhere takes part through their own installation, which signs every
-- request it makes on their behalf with the key their account identifier is the
-- fingerprint of. Three things are needed for that, and one existing table
-- changes.

-- A session holds the account's key while it lasts.
--
-- Signing as somebody needs their private key, and the key is sealed by their
-- password, which Convia holds only while signing in. So signing in opens it
-- and the session keeps it, wrapped under a key derived from the session's own
-- secret -- which Convia does not store. The database alone opens nothing.
--
-- Every existing session is ended: none of them holds a key, and the only way
-- to obtain one is the password. Everybody signs in again once.
DELETE FROM sessions;

ALTER TABLE sessions ADD COLUMN wrapped_identity BYTEA NOT NULL;
ALTER TABLE sessions ADD CONSTRAINT sessions_wrapped_identity_length
    CHECK (octet_length(wrapped_identity) = 60);

-- An invitation into a room, for one person on any installation.
--
-- It names the person by their handle's two halves -- the account identifier,
-- which only the holder of the key can answer to, and the username, which is
-- what the person inviting recognized. It carries no secret: accepting needs a
-- signature by the invitee's key, so the link that points at it is safe to send
-- through any channel.
--
-- Invariants encoded here:
--   * an invitation lasts a bounded time, and ends at most one way: accepted,
--     or revoked
--   * an accepted invitation names the user the invitee became here
CREATE TABLE room_invitations (
    id TEXT PRIMARY KEY,
    application_id TEXT NOT NULL REFERENCES applications (id),
    room_id TEXT NOT NULL REFERENCES rooms (id),
    inviter_user_id TEXT NOT NULL REFERENCES users (id),
    invitee_account_id TEXT NOT NULL,
    invitee_username TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    accepted_at TIMESTAMPTZ,
    accepted_user_id TEXT REFERENCES users (id),
    revoked_at TIMESTAMPTZ,

    CONSTRAINT room_invitations_id_format CHECK (id ~ '^rin_[A-Z2-7]{26}$'),
    CONSTRAINT room_invitations_invitee_format CHECK (invitee_account_id ~ '^acc_[A-Z2-7]{26}$'),
    CONSTRAINT room_invitations_username_format CHECK (invitee_username ~ '^[a-z0-9][a-z0-9._-]{2,31}$'),
    CONSTRAINT room_invitations_expires_after_creation CHECK (expires_at > created_at),
    CONSTRAINT room_invitations_accepted_together CHECK ((accepted_at IS NULL) = (accepted_user_id IS NULL)),
    CONSTRAINT room_invitations_one_ending CHECK (accepted_at IS NULL OR revoked_at IS NULL)
);

CREATE INDEX room_invitations_room_idx ON room_invitations (room_id);

-- Signed requests already seen, so none can be replayed.
--
-- A signature proves who sent a request and not that it was sent once. Each
-- carries a nonce, and the nonce is claimed here; a second claim of the same one
-- is a replay. A row is only needed while its request could still be accepted,
-- which the timestamp window bounds, so rows expire with it.
CREATE TABLE peer_nonces (
    account_id TEXT NOT NULL,
    nonce TEXT NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,

    PRIMARY KEY (account_id, nonce),
    CONSTRAINT peer_nonces_account_format CHECK (account_id ~ '^acc_[A-Z2-7]{26}$'),
    CONSTRAINT peer_nonces_nonce_format CHECK (nonce ~ '^[A-Z2-7]{26}$')
);

CREATE INDEX peer_nonces_expires_at_idx ON peer_nonces (expires_at);

-- A room somebody here belongs to, which lives on another installation.
--
-- It is a pointer and a label, and nothing that was said: the conversation is
-- read from its home every time, so there is no second copy to disagree with the
-- first. `user_id` is who this person is at the home, so the interface can tell
-- their own messages apart.
--
-- Invariants encoded here:
--   * one person holds at most one pointer to a given room at a given home
--   * a pointer dies with the account that holds it
CREATE TABLE remote_rooms (
    id TEXT PRIMARY KEY,
    account_id TEXT NOT NULL REFERENCES accounts (id) ON DELETE CASCADE,
    home TEXT NOT NULL,
    room_id TEXT NOT NULL,
    user_id TEXT NOT NULL,
    name TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,

    CONSTRAINT remote_rooms_id_format CHECK (id ~ '^rrm_[A-Z2-7]{26}$'),
    CONSTRAINT remote_rooms_home_length CHECK (char_length(home) BETWEEN 8 AND 300),
    CONSTRAINT remote_rooms_room_format CHECK (room_id ~ '^room_[A-Z2-7]{26}$'),
    CONSTRAINT remote_rooms_user_format CHECK (user_id ~ '^usr_[A-Z2-7]{26}$'),
    CONSTRAINT remote_rooms_name_length CHECK (char_length(name) BETWEEN 1 AND 120)
);

CREATE UNIQUE INDEX remote_rooms_account_home_room_idx ON remote_rooms (account_id, home, room_id);

-- +goose Down

DROP TABLE remote_rooms;
DROP TABLE peer_nonces;
DROP TABLE room_invitations;

ALTER TABLE sessions DROP CONSTRAINT sessions_wrapped_identity_length;
ALTER TABLE sessions DROP COLUMN wrapped_identity;
