-- wiped is an entry whose text is gone, edited or deleted since (ADR 0034); hidden is a message deleted on this machine only.
ALTER TABLE messages ADD COLUMN wiped INTEGER NOT NULL DEFAULT 0 CHECK (wiped IN (0, 1));
ALTER TABLE messages ADD COLUMN hidden INTEGER NOT NULL DEFAULT 0 CHECK (hidden IN (0, 1));
