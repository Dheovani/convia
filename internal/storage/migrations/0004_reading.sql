-- How far the person has read each conversation, on this machine only (ADR 0032).
ALTER TABLE conversations ADD COLUMN read_wall INTEGER NOT NULL DEFAULT 0;
ALTER TABLE conversations ADD COLUMN read_counter INTEGER NOT NULL DEFAULT 0;
