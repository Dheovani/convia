-- The files of the channels this peer holds (ADR 0043): what each is, how much of it is here, and whether it is wanted.
CREATE TABLE space_files (
    id         BLOB PRIMARY KEY REFERENCES space_messages (id),
    space_id   BLOB NOT NULL REFERENCES spaces (id),
    channel_id BLOB NOT NULL CHECK (length(channel_id) = 16),
    size       INTEGER NOT NULL CHECK (size > 0 AND size <= 104857600),
    sha256     BLOB NOT NULL CHECK (length(sha256) = 32),
    held       INTEGER NOT NULL DEFAULT 0,
    complete   INTEGER NOT NULL DEFAULT 0 CHECK (complete IN (0, 1)),
    wanted     INTEGER NOT NULL DEFAULT 0 CHECK (wanted IN (0, 1))
) STRICT;
