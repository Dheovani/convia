-- +goose Up

-- Every event that must not be lost, recorded by the transaction that made it happen.
--
-- Until now an event was handed to the live streams and queued for webhooks after
-- the change committed, so a crash in between lost it (`M15-016`), and a client
-- that reconnected could not ask for what it missed (`M14-006`). Writing the
-- event here, in the same transaction as the change, closes both: webhook
-- deliveries are queued in that transaction too, and streams read this table in
-- order and resume from any row still kept. See docs/adr/0017.
--
-- Order is the recording transaction, then the position. Positions are handed out
-- when rows are inserted, and transactions commit in any order, so a reader that
-- went by position alone could pass a row that was still to commit. A reader
-- takes only rows written by transactions older than every one still running,
-- and in that order nothing can appear behind it.
--
-- `transaction_id` is the one default in Convia's schema that is not a constant,
-- because it must be the transaction doing the insert, which only PostgreSQL knows.
--
-- The body is the event as delivered, without its cursor, which is these two
-- columns. It is text rather than JSONB so that a replay writes out exactly what
-- was recorded.
--
-- Rows are kept for a day, and the newest removed is remembered, so a cursor
-- from before it can be told it is too old rather than silently skip what was
-- removed.

CREATE TABLE event_journal (
    position BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    transaction_id xid8 NOT NULL DEFAULT pg_current_xact_id(),
    id TEXT NOT NULL,
    application_id TEXT NOT NULL,
    type TEXT NOT NULL,
    body TEXT NOT NULL,
    recorded_at TIMESTAMPTZ NOT NULL,

    CONSTRAINT event_journal_id_format CHECK (id ~ '^evt_[A-Z2-7]{26}$'),
    CONSTRAINT event_journal_body_present CHECK (char_length(body) > 0)
);

-- The order every reader follows.
CREATE INDEX event_journal_order_idx ON event_journal (transaction_id, position);

-- A replay reads one application's events in that order.
CREATE INDEX event_journal_application_order_idx ON event_journal (application_id, transaction_id, position);

-- Pruning removes by age.
CREATE INDEX event_journal_recorded_at_idx ON event_journal (recorded_at);

-- The newest event that has been removed. There is exactly one row.
CREATE TABLE event_journal_floor (
    singleton BOOLEAN PRIMARY KEY DEFAULT true,
    transaction_id xid8 NOT NULL,
    position BIGINT NOT NULL,

    CONSTRAINT event_journal_floor_singleton CHECK (singleton)
);

INSERT INTO event_journal_floor (transaction_id, position) VALUES ('0', 0);

-- +goose Down

DROP TABLE event_journal_floor;
DROP TABLE event_journal;
