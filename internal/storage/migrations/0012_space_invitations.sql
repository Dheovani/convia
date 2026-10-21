-- Spaces this peer was added to and has not yet accepted or declined (ADR 0039), each sealed under its space.
CREATE TABLE space_invitations (
    space_id   BLOB PRIMARY KEY CHECK (length(space_id) = 16),
    inviter    BLOB NOT NULL CHECK (length(inviter) = 32),
    invitation BLOB NOT NULL
) STRICT;
