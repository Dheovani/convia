-- +goose Up

-- The administrative listing pages applications newest first, keyed on
-- (created_at, id) so that a page stays stable while applications are created.
-- The index matches that ordering exactly, so paging never sorts the table.
CREATE INDEX applications_created_at_id_idx ON applications (created_at DESC, id DESC);

-- +goose Down

DROP INDEX applications_created_at_id_idx;
