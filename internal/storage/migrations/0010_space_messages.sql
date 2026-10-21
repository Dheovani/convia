-- The entries of every channel this peer holds (ADR 0013), each sealed under its identifier.
CREATE TABLE space_messages (
    id          BLOB PRIMARY KEY CHECK (length(id) = 16),
    space_id    BLOB NOT NULL REFERENCES spaces (id),
    channel_id  BLOB NOT NULL CHECK (length(channel_id) = 16),
    author      BLOB NOT NULL CHECK (length(author) = 32),
    sequence    INTEGER NOT NULL CHECK (sequence > 0),
    hlc_wall    INTEGER NOT NULL,
    hlc_counter INTEGER NOT NULL,
    message     BLOB NOT NULL,
    UNIQUE (space_id, channel_id, author, sequence)
) STRICT;
