-- A conversation holds entries besides messages (ADR 0034, ADR 0035). target is the message an entry
-- is about, or the one a message answers; flag is whether a reaction is on.
ALTER TABLE messages ADD COLUMN kind TEXT NOT NULL DEFAULT 'text' CHECK (kind IN ('text', 'reaction', 'edit', 'deletion', 'file'));
ALTER TABLE messages ADD COLUMN target BLOB CHECK (target IS NULL OR length(target) = 16);
ALTER TABLE messages ADD COLUMN flag INTEGER NOT NULL DEFAULT 0 CHECK (flag IN (0, 1));
