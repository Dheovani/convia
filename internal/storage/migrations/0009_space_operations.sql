-- Every operation known of each space (ADR 0013), applied or not, sealed under its hash.
CREATE TABLE space_operations (
    hash      BLOB PRIMARY KEY CHECK (length(hash) = 32),
    space_id  BLOB NOT NULL REFERENCES spaces (id),
    operation BLOB NOT NULL
) STRICT;
CREATE INDEX space_operations_space ON space_operations (space_id);
