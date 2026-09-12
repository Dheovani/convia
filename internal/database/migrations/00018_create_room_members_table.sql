-- +goose Up

-- Who belongs to a room.
--
-- **`00009` said Convia does not model this, and gave a reason M18 invalidated.**
-- The reason was that Convia held no credentials for an application's people, so
-- it could not enforce a policy about who belongs and the application already
-- knew. Since M18 a person signs in to Convia's own product and presents a
-- session, and at that moment Convia is the only party that can answer whether
-- they may open a room. So the concept arrives now, when something finally
-- depends on it, rather than having been guessed at earlier.
--
-- **It gates people, not applications.** An application's key already carries
-- full authority over its own rooms; requiring it to add itself as a member
-- before posting a deployment notice would be ceremony that protects nobody.
-- Membership is what the *session* surface checks, where somebody acts as
-- themselves. That is the line: the key says which application is calling,
-- membership says which of its people may be where.
--
-- Deriving it from participation was considered and rejected. A chat-first room
-- is one somebody joins and reads before any call happens in it, so a rule built
-- on `participants` would leave every silent room with no members at all.
--
-- **There is no role here**, unlike `participants`. A moderator exists there
-- because a call has something to moderate: removing somebody mid-conversation
-- is an act with a victim and a witness. A room member has no such power to
-- hold — the application manages membership through its own surface — and a
-- column nothing reads is a column that will acquire a meaning by accident.
--
-- **There is no lifecycle either.** A participation records `joined`, `left` and
-- `removed` because a call's roster is the record of one occasion. Membership is
-- current state: somebody is in the room or they are not. Leaving and coming
-- back leaves no trail here, and the audit log already records both acts for
-- anybody who needs the history.
--
-- A guest has no row. They have no Convia user, their stint is one call, and
-- they reach it with an invitation rather than by belonging anywhere.
--
-- Invariants encoded here:
--   * one row per person per room, enforced by the primary key
--   * the room and the person both exist, enforced by foreign keys
--   * nothing claims to have joined before the room existed, which the room's
--     own timestamps would contradict
CREATE TABLE room_members (
  application_id TEXT NOT NULL REFERENCES applications (id),
  room_id TEXT NOT NULL REFERENCES rooms (id),
  user_id TEXT NOT NULL REFERENCES users (id),
  created_at TIMESTAMPTZ NOT NULL,

  PRIMARY KEY (room_id, user_id)
);

-- "Which rooms am I in?" is the sidebar, and it is the one read that is not
-- scoped to a single room. The primary key serves the other direction -- a
-- room's member list, and the membership check itself -- so this is the only
-- extra index the table needs.
CREATE INDEX room_members_user_idx ON room_members (application_id, user_id, room_id);

-- +goose Down

DROP TABLE room_members;
