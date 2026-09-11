-- +goose Up

-- A message whose author has been erased.
--
-- `00015` said a message names a user or an invitation and exactly one of the
-- two. That is right for every message somebody wrote, and it has no room for
-- the one case M06 promised to handle: a person asks to be erased, and Convia
-- must stop holding what it holds about them.
--
-- The two obvious answers are both worse than this one:
--
--   * **Deleting their messages** takes a conversation away from the people
--     still in it. Every reply left behind answers something that is no longer
--     there, and a history full of holes is a worse record for everyone else
--     than one with an anonymous author. The participants table reached the
--     same conclusion in `00012`, where its Down refuses rather than inventing
--     users or deleting history.
--   * **Keeping the author link and clearing the name** erases nothing. The
--     name never lived here; the link to the person is the thing Convia holds.
--
-- So erasing redacts: the row keeps its place in the room's order, loses its
-- body, and loses its author. It becomes the tombstone a withdrawal already
-- produces, minus the attribution.
--
-- An erased message is therefore always a deleted one. The constraint says so,
-- which keeps "no author" from becoming a shape a live message could take
-- through some future bug.
ALTER TABLE messages DROP CONSTRAINT messages_author_identified;

ALTER TABLE messages ADD CONSTRAINT messages_author_identified CHECK (
  (author_user_id IS NOT NULL AND author_invitation_id IS NULL)
  OR (author_user_id IS NULL AND author_invitation_id IS NOT NULL)
  OR (author_user_id IS NULL AND author_invitation_id IS NULL AND deleted_at IS NOT NULL)
);

-- Erasing acts on everything one person wrote, across every room. Without this
-- it is a scan of the largest table Convia holds, which is why it arrives with
-- the operation that needs it rather than being guessed at in `00015`.
CREATE INDEX messages_author_user_idx
  ON messages (application_id, author_user_id)
  WHERE author_user_id IS NOT NULL;

-- +goose Down

DROP INDEX messages_author_user_idx;

-- Restoring the stricter constraint fails if anything has been erased, which is
-- correct: there is no way to un-erase somebody, and inventing an author for
-- their messages would be a lie the schema should refuse to tell.
ALTER TABLE messages DROP CONSTRAINT messages_author_identified;

ALTER TABLE messages ADD CONSTRAINT messages_author_identified CHECK (
  (author_user_id IS NOT NULL AND author_invitation_id IS NULL)
  OR (author_user_id IS NULL AND author_invitation_id IS NOT NULL)
);
