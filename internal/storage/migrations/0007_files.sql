-- What a file entry describes (ADR 0036); its name is the entry's sealed content, and its pieces are
-- kept sealed beside the database. held is how much of it this peer has.
CREATE TABLE files (
    id          BLOB PRIMARY KEY REFERENCES messages (id),
    size        INTEGER NOT NULL CHECK (size > 0 AND size <= 104857600),
    type        TEXT NOT NULL,
    sha256      BLOB NOT NULL CHECK (length(sha256) = 32),
    orientation INTEGER NOT NULL DEFAULT 1 CHECK (orientation BETWEEN 1 AND 8),
    held        INTEGER NOT NULL DEFAULT 0,
    complete    INTEGER NOT NULL DEFAULT 0 CHECK (complete IN (0, 1))
) STRICT;
