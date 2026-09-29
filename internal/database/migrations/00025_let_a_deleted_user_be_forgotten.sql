-- +goose Up

-- What a deleted user needs before anything can forget it.
--
-- Deletion has always retained the row: `docs/users.md` says the deletion stays
-- recoverable and the external subject stays taken until erasure frees it. What
-- did not exist was erasure -- there was no age at which Convia forgot anything,
-- and the job that was supposed to act at the end of the window was listed under
-- "Not Yet Implemented" rather than written. This is `M23-017`.
--
-- Two columns stood in the way.
--
-- `deleted_at` is new because the window has to start somewhere. It could have
-- been read from `updated_at`, since a deleted user can no longer be updated and
-- that timestamp therefore stops moving -- but that is a fact about two other
-- rules holding, and a retention window should not rest on an argument. The
-- backfill uses exactly that argument once, here, where it can be checked.
--
-- `external_subject` becomes nullable because freeing it is the point. An
-- application that deletes a person and later sees them again must be able to
-- resolve that subject into a new user; while the old row still holds it, the
-- unique index refuses. Nulling it is what "frees a deleted subject" means.
--
-- A tombstone value would have avoided the schema change, but nothing stops an
-- application from choosing that value as a real subject, and a collision there
-- would hand one person another person's user. NULL cannot be chosen by anybody.

ALTER TABLE users ADD COLUMN deleted_at TIMESTAMPTZ;

ALTER TABLE users ALTER COLUMN external_subject DROP NOT NULL;

-- Already-deleted users get the only moment the old schema recorded. A deleted
-- user cannot be updated or activated, so `updated_at` is when it was deleted.
UPDATE users SET deleted_at = updated_at WHERE status = 'deleted';

-- Deleted means dated, and dated means deleted. Without this a row could sit in
-- the window for ever by having no date, which is the failure that would look
-- exactly like the job working.
ALTER TABLE users ADD CONSTRAINT users_deleted_at_matches_status
    CHECK ((status = 'deleted') = (deleted_at IS NOT NULL));

ALTER TABLE users ADD CONSTRAINT users_deleted_at_order
    CHECK (deleted_at IS NULL OR deleted_at >= created_at);

-- An erased user has no subject, so it is not in this index and cannot block the
-- subject being resolved again. Everything else about the uniqueness is
-- unchanged: one subject resolves to one user for the life of the mapping,
-- including while the user is deleted and merely waiting.
DROP INDEX users_application_subject_key;

CREATE UNIQUE INDEX users_application_subject_key ON users (application_id, external_subject)
    WHERE external_subject IS NOT NULL;

-- The sweep reads by age within the deleted rows, which are a small part of the
-- table, so the index carries only those.
CREATE INDEX users_deleted_at_idx ON users (deleted_at) WHERE status = 'deleted';

-- +goose Down

-- The column stays nullable, deliberately.
--
-- Restoring NOT NULL would fail the moment anything had been erased, and there
-- is nothing to put back: the subject is gone because forgetting it was the
-- whole operation. A rollback that invented one would hand somebody another
-- person's user, which is worse than a column that is wider than it was.
--
-- The plain unique index is restored rather than the partial one. NULLs are
-- distinct to a unique index, so erased rows do not collide with each other and
-- the constraint means what it meant before.

DROP INDEX users_deleted_at_idx;

DROP INDEX users_application_subject_key;

CREATE UNIQUE INDEX users_application_subject_key ON users (application_id, external_subject);

ALTER TABLE users DROP CONSTRAINT users_deleted_at_order;

ALTER TABLE users DROP CONSTRAINT users_deleted_at_matches_status;

ALTER TABLE users DROP COLUMN deleted_at;
