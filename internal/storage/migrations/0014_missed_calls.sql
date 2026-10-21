-- Calls this person missed (ADR 0044): shown in the conversation on this machine alone, never sent.
CREATE TABLE missed_calls (
    id          INTEGER PRIMARY KEY,
    contact_key BLOB NOT NULL CHECK (length(contact_key) = 32),
    at          INTEGER NOT NULL
) STRICT;
CREATE INDEX missed_calls_contact ON missed_calls (contact_key);
