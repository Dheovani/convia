-- +goose Up

-- The media session that realizes a call.
--
-- A call is a Convia concept; the place where audio and video actually travel
-- is realized by a media plane and referred to by whatever identifier that
-- plane uses. Convia stores that reference so it can release the session when
-- the call ends, compares it to nothing, and never publishes it.
--
-- It lives on the call rather than in a table of its own because it is one
-- value belonging to one call for its whole life. A separate table would hold
-- exactly one row per call and be joined on every read to say what a column
-- already says.
--
-- The column is deliberately absent from the projection the domain reads. The
-- only way to obtain it is to ask the store for it explicitly, so no public
-- representation has a field that could carry it. See
-- docs/adr/0001-control-plane-media-plane-boundary.md.
--
-- The reference is **not** cleared when a call ends. Releasing a session is
-- best-effort against infrastructure that may be unreachable, so clearing it
-- would throw away the only handle a later attempt could use. It stays as the
-- record of which session realized this call.
ALTER TABLE calls ADD COLUMN media_session TEXT;

-- A reference is opaque, but it is not unbounded: a provider handing Convia
-- something enormous is a provider Convia should refuse rather than store.
ALTER TABLE calls ADD CONSTRAINT calls_media_session_length
    CHECK (media_session IS NULL OR char_length(media_session) BETWEEN 1 AND 400);

-- +goose Down

ALTER TABLE calls DROP CONSTRAINT calls_media_session_length;
ALTER TABLE calls DROP COLUMN media_session;
