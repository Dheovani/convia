-- Every call this person made or was called by (ADR 0045): kept on this machine alone, never sent. The calls
-- missed before it are kept in it too.
CREATE TABLE call_log (
    id          INTEGER PRIMARY KEY,
    contact_key BLOB NOT NULL CHECK (length(contact_key) = 32),
    incoming    INTEGER NOT NULL CHECK (incoming IN (0, 1)),
    video       INTEGER NOT NULL CHECK (video IN (0, 1)),
    outcome     TEXT NOT NULL,
    rang        INTEGER NOT NULL,
    answered    INTEGER NOT NULL DEFAULT 0,
    ended       INTEGER NOT NULL
) STRICT;
CREATE INDEX call_log_contact ON call_log (contact_key);

INSERT INTO call_log (contact_key, incoming, video, outcome, rang, ended)
    SELECT contact_key, 1, 0, 'missed', at, at FROM missed_calls;
DROP TABLE missed_calls;
