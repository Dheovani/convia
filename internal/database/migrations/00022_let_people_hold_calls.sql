-- +goose Up

-- A person signed in to Convia starts calls, and Convia itself ends them.
--
-- Until now a call was started and ended on an application's authority or an
-- operator's, and the actor said which. `M18-004` brings calls to Convia's own
-- product, where the product owner decided the shape:
--
--   * any member of a room starts a call in it, recorded as `person` without
--     saying which person, because who is in a call is what participants are
--     for;
--   * a call ends when its last participant leaves. When somebody asked to
--     leave, that is `person` too. When nobody asked -- the media plane reported
--     that the last connection went away, or the room was deleted -- it is
--     `system`, the actor docs/calls.md reserved for Convia acting on evidence
--     rather than on a request.
--
-- Nothing starts a call on its own, so `system` is an actor that only ends one.
--
-- Invariants encoded here:
--   * a call is started by an application, an operator or a person
--   * a call is ended by any of those, or by Convia itself
--   * one media session realizes at most one call, so a report from the media
--     plane names exactly one conversation

ALTER TABLE calls DROP CONSTRAINT calls_started_by_allowed;
ALTER TABLE calls DROP CONSTRAINT calls_ended_by_allowed;

ALTER TABLE calls ADD CONSTRAINT calls_started_by_allowed
    CHECK (started_by IN ('application', 'operator', 'person'));

ALTER TABLE calls ADD CONSTRAINT calls_ended_by_allowed
    CHECK (ended_by IS NULL OR ended_by IN ('application', 'operator', 'person', 'system'));

-- The media plane reports what happened by the session it realized, and knows
-- nothing else about a call. Finding the call from that report is the one read
-- that starts from the session, so it is the one that must not scan.
CREATE UNIQUE INDEX calls_media_session_key
    ON calls (media_session)
    WHERE media_session IS NOT NULL;

-- +goose Down

-- Restoring the narrower actors fails if a person or Convia ever started or
-- ended a call, which is correct: rewriting who ended a conversation would be
-- inventing history.
DROP INDEX calls_media_session_key;

ALTER TABLE calls DROP CONSTRAINT calls_ended_by_allowed;
ALTER TABLE calls DROP CONSTRAINT calls_started_by_allowed;

ALTER TABLE calls ADD CONSTRAINT calls_started_by_allowed
    CHECK (started_by IN ('application', 'operator'));

ALTER TABLE calls ADD CONSTRAINT calls_ended_by_allowed
    CHECK (ended_by IS NULL OR ended_by IN ('application', 'operator'));
