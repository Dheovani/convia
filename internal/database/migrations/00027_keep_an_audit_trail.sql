-- +goose Up

-- What was done to this installation, by whom, and on whose authority.
--
-- Until now an audit entry was a structured log line. `internal/applications`
-- said so in its own words -- "an operational record, not yet a queryable audit
-- trail; M21 introduces durable audit storage" -- and this is that. A log line
-- is good at being read while an incident is happening and bad at every
-- question asked afterwards: it is kept for as long as whoever ships the logs
-- decided, it cannot be searched by subject, and nothing in Convia can tell
-- whether it arrived at all.
--
-- The event journal is not this table and could not be. It is pruned after a
-- day, because its job is to let a disconnected reader catch up, and it records
-- `application_id` and no actor at all. A buffer that forgets is the right
-- shape for delivery and the wrong shape for accountability.
--
-- **There are no foreign keys here, deliberately.** An entry has to outlive what
-- it describes: the whole point of recording that an operator deleted a tenant
-- is that the record survives the tenant. A reference would leave two choices,
-- refusing the deletion or cascading the record away, and both destroy the
-- trail at exactly the moment it matters. So the identifiers are recorded as
-- the text they were, and an entry naming a row that no longer exists is a
-- correct entry rather than a dangling one.

CREATE TABLE audit_entries (
    id TEXT PRIMARY KEY,

    -- What happened, named exactly as the event vocabulary names it, so that an
    -- entry and the event announced for the same occurrence do not become two
    -- accounts of it. Entries exist for changes no event covers -- an operator
    -- reading a tenant's rooms announces nothing -- so the set here is wider.
    action TEXT NOT NULL,

    /*
        Who acted, in two parts, and both are needed.

        `actor_kind` is the authority Convia verified, and there is one for each
        kind of caller it verifies: an operator, an application's own key, a
        person signed in to Convia's product, the holder of an invitation,
        another installation acting for one of its own people, and Convia
        acting on evidence nobody asked it to act on. All six are here because
        a trail missing one would record those actions as the system's, which
        is the one claim about an action that must never be wrong.

        This table cannot stop there. "An operator suspended this tenant" with
        no operator in it is the exact failure an audit trail exists to prevent:
        it records that authority was used and loses which hands held it. So the
        identifier travels beside the kind -- an operator credential, an
        application credential, an account -- and is null only for the system,
        which has no credential because nobody presented one.
    */
    actor_kind TEXT NOT NULL,
    actor_id TEXT,

    -- The tenant the action touched, where it touched one. Null for an action
    -- about the installation itself, such as issuing an operator credential.
    application_id TEXT,

    -- What was acted on: its kind, and its public identifier. Both are recorded
    -- rather than derived from the action, because searching for everything
    -- that happened to one room is the question an incident actually asks.
    subject_kind TEXT NOT NULL,
    subject_id TEXT NOT NULL,

    /*
        Why, in the actor's own words, where Convia required an answer.

        This is `M21-009`: a high-impact operator action is refused without one.
        It is null on everything else rather than empty, so "no reason was
        required" and "a reason was required and is blank" cannot be confused,
        and the constraint below keeps a blank one out.
    */
    reason TEXT,

    /*
        The few facts about the change that cannot be reconstructed from the
        rest of the row, as flat text keyed by name.

        **It is deliberately not a copy of what changed.** There is no before
        and no after, because carrying the shape of every change would make
        this a second copy of every table, kept by code that cannot know when
        one of them changes meaning, and the first thing anybody would trust
        about it is the part most likely to be wrong.

        What it is for is the one thing that would otherwise be lost by moving
        from log lines to rows: the scopes a credential was minted with. An
        action name says a key was issued; it cannot say the key can suspend
        every tenant. Nothing a person wrote, no metadata an application
        composed, and no secret goes here -- an entry carrying key material
        would be a second copy of the thing the trail exists to protect.
    */
    details JSONB NOT NULL,

    -- The request that caused it, which is what joins an entry to the access
    -- log line and the trace for the same moment.
    request_id TEXT NOT NULL,

    recorded_at TIMESTAMPTZ NOT NULL,

    CONSTRAINT audit_entries_id_format CHECK (id ~ '^aud_[A-Z2-7]{26}$'),

    CONSTRAINT audit_entries_action_present CHECK (char_length(action) > 0),

    CONSTRAINT audit_entries_actor_kind_allowed CHECK (
        actor_kind IN ('operator', 'application', 'person', 'guest', 'peer', 'system')
    ),

    -- Only the system acts without a credential, and everything else has one.
    -- Written as both halves so neither direction can be satisfied by accident.
    CONSTRAINT audit_entries_actor_identified CHECK (
        (actor_kind = 'system' AND actor_id IS NULL)
        OR (actor_kind <> 'system' AND actor_id IS NOT NULL AND char_length(actor_id) > 0)
    ),

    CONSTRAINT audit_entries_subject_present CHECK (
        char_length(subject_kind) > 0 AND char_length(subject_id) > 0
    ),

    -- A reason is absent or it says something. A row carrying '' would record
    -- that Convia asked for a reason and accepted nothing.
    CONSTRAINT audit_entries_reason_said CHECK (reason IS NULL OR char_length(reason) > 0),

    -- An object, and a small one. The bound is what stops the trail becoming
    -- somewhere to put payloads, which is the one way this column could turn
    -- into the copy of every table it is written not to be.
    CONSTRAINT audit_entries_details_shaped CHECK (
        jsonb_typeof(details) = 'object' AND pg_column_size(details) <= 2048
    )
);

-- The trail read newest first, which is how an incident starts: what just
-- happened here. The identifier breaks ties: a timestamp is kept to the
-- microsecond, and a busy installation writes two entries in one.
CREATE INDEX audit_entries_recorded_at_id_idx ON audit_entries (recorded_at DESC, id DESC);

-- Everything that happened to one tenant, in that order.
CREATE INDEX audit_entries_application_recorded_at_id_idx
    ON audit_entries (application_id, recorded_at DESC, id DESC);

-- Everything one actor did, which is the question asked about a credential
-- suspected of being in the wrong hands.
CREATE INDEX audit_entries_actor_recorded_at_id_idx
    ON audit_entries (actor_kind, actor_id, recorded_at DESC, id DESC);

-- Everything that happened to one room, call, credential or account.
CREATE INDEX audit_entries_subject_recorded_at_id_idx
    ON audit_entries (subject_kind, subject_id, recorded_at DESC, id DESC);

-- +goose Down

DROP TABLE audit_entries;
