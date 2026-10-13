CREATE TABLE meta (
    name  TEXT PRIMARY KEY,
    value BLOB NOT NULL
) STRICT;

-- The one identity of this peer (ADR 0004); its private key is sealed with DPAPI.
CREATE TABLE identity (
    id                 INTEGER PRIMARY KEY CHECK (id = 1),
    public_key         BLOB NOT NULL CHECK (length(public_key) = 32),
    sealed_private_key BLOB NOT NULL,
    display_name       TEXT NOT NULL,
    created_at         INTEGER NOT NULL
) STRICT;

CREATE TABLE contacts (
    public_key      BLOB PRIMARY KEY CHECK (length(public_key) = 32),
    display_name    TEXT NOT NULL,
    rendezvous_hint TEXT,
    verified        INTEGER NOT NULL DEFAULT 0 CHECK (verified IN (0, 1)),
    added_at        INTEGER NOT NULL
) STRICT;

CREATE TABLE conversations (
    id          BLOB PRIMARY KEY CHECK (length(id) = 16),
    contact_key BLOB NOT NULL UNIQUE REFERENCES contacts (public_key),
    created_at  INTEGER NOT NULL
) STRICT;

-- Ordered by hlc_wall, hlc_counter, then id (ADR 0010); content is sealed, bound to id.
CREATE TABLE messages (
    id              BLOB PRIMARY KEY CHECK (length(id) = 16),
    conversation_id BLOB NOT NULL REFERENCES conversations (id),
    author_key      BLOB NOT NULL CHECK (length(author_key) = 32),
    author_sequence INTEGER NOT NULL CHECK (author_sequence > 0),
    hlc_wall        INTEGER NOT NULL,
    hlc_counter     INTEGER NOT NULL,
    content         BLOB NOT NULL,
    stored_at       INTEGER NOT NULL,
    UNIQUE (conversation_id, author_key, author_sequence)
) STRICT;

CREATE INDEX messages_in_order ON messages (conversation_id, hlc_wall, hlc_counter, id);

-- Messages waiting for their recipient's acknowledgement (ADR 0011).
CREATE TABLE outbox (
    message_id    BLOB PRIMARY KEY REFERENCES messages (id),
    recipient_key BLOB NOT NULL CHECK (length(recipient_key) = 32),
    queued_at     INTEGER NOT NULL
) STRICT;
