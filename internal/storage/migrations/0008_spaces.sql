-- The spaces this peer belongs to (ADR 0013), each kept as its sealed, signed genesis.
CREATE TABLE spaces (
    id      BLOB PRIMARY KEY CHECK (length(id) = 16),
    genesis BLOB NOT NULL
) STRICT;
