-- When a waiting message was last sent over a connection: it is sent, not yet delivered (ADR 0011).
ALTER TABLE outbox ADD COLUMN transmitted_at INTEGER;
