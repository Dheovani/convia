-- Every discovery secret known of each space's epochs (ADR 0014), sealed under its hash.
CREATE TABLE space_secrets (
    hash     BLOB PRIMARY KEY CHECK (length(hash) = 32),
    space_id BLOB NOT NULL REFERENCES spaces (id),
    epoch    BLOB NOT NULL CHECK (length(epoch) = 32),
    secret   BLOB NOT NULL
) STRICT;
CREATE INDEX space_secrets_epoch ON space_secrets (space_id, epoch);
