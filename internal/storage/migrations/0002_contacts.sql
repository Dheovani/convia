-- The pair secret two contacts share (ADR 0012), sealed with the content key.
ALTER TABLE contacts ADD COLUMN pair_secret BLOB;

-- A removed contact whose conversation the person kept (ADR 0028): listed nowhere, never reached.
ALTER TABLE contacts ADD COLUMN removed INTEGER NOT NULL DEFAULT 0 CHECK (removed IN (0, 1));

-- Requests this peer sent (ADR 0026), with its sealed part of the pair secret.
CREATE TABLE requests_sent (
    inviter_key     BLOB PRIMARY KEY CHECK (length(inviter_key) = 32),
    inviter_name    TEXT NOT NULL,
    rendezvous_hint TEXT NOT NULL,
    part            BLOB NOT NULL,
    sent_at         INTEGER NOT NULL
) STRICT;

-- Requests this peer received; its own sealed part is kept once it accepts, until the requester confirms.
CREATE TABLE requests_received (
    requester_key  BLOB PRIMARY KEY CHECK (length(requester_key) = 32),
    requester_name TEXT NOT NULL,
    their_part     BLOB NOT NULL,
    our_part       BLOB,
    state          TEXT NOT NULL CHECK (state IN ('waiting', 'accepted', 'declined')),
    received_at    INTEGER NOT NULL
) STRICT;

-- Keys whose requests and connections are refused (ADR 0028).
CREATE TABLE blocked (
    public_key BLOB PRIMARY KEY CHECK (length(public_key) = 32),
    blocked_at INTEGER NOT NULL
) STRICT;
