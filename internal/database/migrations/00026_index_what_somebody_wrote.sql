-- +goose Up

-- The one cut through messages that is not a room.
--
-- Every other read of this table is scoped to a room, because that is how a
-- conversation is read, and the indexes follow that. Export asks the opposite
-- question -- everything one person wrote, across every room -- and `M23-017`
-- is the first thing to ask it. Without this, handing somebody their own data
-- reads the whole table, and the person it is slowest for is the one on the
-- installation with the most conversations in it.
--
-- The order is the order the export walks in: `(created_at, id)`, because a
-- sequence orders one room and says nothing across rooms, so paging by it
-- would interleave two rooms into an order that is not an order.
--
-- It is partial on the author being present. An erased message has none, and
-- those rows are exactly the ones no export will ever ask for.

CREATE INDEX messages_author_written_idx
    ON messages (application_id, author_user_id, created_at, id)
    WHERE author_user_id IS NOT NULL;

-- +goose Down

DROP INDEX messages_author_written_idx;
