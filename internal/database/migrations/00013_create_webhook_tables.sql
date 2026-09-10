-- +goose Up

-- Webhooks are the durable half of what M14 started. The event stream delivers
-- to whoever is connected right now and holds nothing; these two tables deliver
-- to whoever asked to be told, whether or not they were listening at the time.
--
-- The split is deliberate and each half is honest about what it is. A stream is
-- for a picture that must stay current and can be rebuilt by re-reading. A
-- webhook is for something a consumer must not miss, and that is why every
-- delivery is a row: it can be retried, counted, and explained afterwards.

-- An endpoint is one place an application asked to be notified, and the event
-- types it asked about.
--
-- The signing secret is stored in plaintext, which is the one place Convia does
-- that, and it is worth saying why. The three key families Convia issues —
-- cvk_, cvo_, cvi_ — are all *presented back* to Convia, so a digest is enough
-- to verify one and Convia never needs the original. A webhook secret is the
-- opposite: nobody presents it, Convia signs with it on every delivery, and a
-- digest cannot produce a signature. Storing it is therefore not a shortcut; it
-- is what an HMAC key is. What follows from that is operational rather than
-- cryptographic: the column is never returned by any read, it is excluded from
-- every projection, and rotation exists so a suspected exposure has a remedy.
CREATE TABLE webhook_endpoints (
    id TEXT PRIMARY KEY,
    application_id TEXT NOT NULL REFERENCES applications (id),
    name TEXT NOT NULL,
    url TEXT NOT NULL,
    secret TEXT NOT NULL,
    event_types TEXT[] NOT NULL,
    status TEXT NOT NULL,

    -- Consecutive terminal failures. It resets on any success, so an endpoint
    -- that fails occasionally is never disabled: what this counts is an
    -- endpoint that has stopped working, not one having a bad afternoon.
    consecutive_failures INTEGER NOT NULL DEFAULT 0,

    -- Why Convia stopped delivering, in Convia's own words. It is shown to the
    -- application so that a disabled endpoint explains itself rather than
    -- looking like a fault in Convia.
    disabled_reason TEXT,

    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,

    CONSTRAINT webhook_endpoints_id_format CHECK (id ~ '^whk_[A-Z2-7]{26}$'),

    CONSTRAINT webhook_endpoints_status_allowed CHECK (status IN ('enabled', 'disabled')),

    -- An endpoint subscribed to nothing would be a destination Convia never
    -- sends to, which is a registration that silently does not work.
    CONSTRAINT webhook_endpoints_subscribed CHECK (cardinality(event_types) > 0),

    -- Only a disabled endpoint has a reason, and every disabled one has it.
    CONSTRAINT webhook_endpoints_disabled_explained CHECK (
        (status = 'enabled' AND disabled_reason IS NULL)
        OR (status = 'disabled' AND disabled_reason IS NOT NULL)
    ),

    CONSTRAINT webhook_endpoints_failures_counted CHECK (consecutive_failures >= 0),
    CONSTRAINT webhook_endpoints_updated_at_order CHECK (updated_at >= created_at)
);

-- An application's own endpoints, newest first.
CREATE INDEX webhook_endpoints_application_created_at_id_idx
    ON webhook_endpoints (application_id, created_at DESC, id DESC);

-- The fan-out query the enqueue runs on every announced event: which of this
-- tenant's enabled endpoints asked about this type.
CREATE INDEX webhook_endpoints_application_enabled_idx
    ON webhook_endpoints (application_id)
    WHERE status = 'enabled';

-- A delivery is one event owed to one endpoint. It exists before the first
-- attempt and outlives the last one, which is what makes the whole thing
-- auditable: an application can ask what Convia tried to tell it and what
-- happened, without Convia having to have kept a log of it separately.
CREATE TABLE webhook_deliveries (
    id TEXT PRIMARY KEY,
    endpoint_id TEXT NOT NULL REFERENCES webhook_endpoints (id) ON DELETE CASCADE,

    -- Carried on the delivery rather than reached through the endpoint, so that
    -- listing a tenant's deliveries is one indexed read and cannot be answered
    -- with another tenant's rows through a mistaken join.
    application_id TEXT NOT NULL REFERENCES applications (id),

    event_id TEXT NOT NULL,
    event_type TEXT NOT NULL,

    -- The exact bytes that were signed, stored as text rather than JSONB.
    -- JSONB is a parsed representation: it reorders keys and drops
    -- insignificant whitespace, so a body round-tripped through it would no
    -- longer match its own signature. What a consumer verifies is bytes, so
    -- bytes are what is kept.
    payload TEXT NOT NULL,

    status TEXT NOT NULL,
    attempts INTEGER NOT NULL DEFAULT 0,

    -- When this delivery is next due. It is NULL once the delivery is over, one
    -- way or the other, which is also what keeps the worker's index small: it
    -- only ever holds work that is actually outstanding.
    next_attempt_at TIMESTAMPTZ,

    -- What the last attempt met. The status code is the destination's; the
    -- error is Convia's own description of a failure that produced no response,
    -- never the body a destination returned, which is text Convia did not write
    -- and has no reason to keep.
    last_status_code INTEGER,
    last_error TEXT,

    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    delivered_at TIMESTAMPTZ,

    CONSTRAINT webhook_deliveries_id_format CHECK (id ~ '^whd_[A-Z2-7]{26}$'),

    CONSTRAINT webhook_deliveries_status_allowed CHECK (status IN ('pending', 'delivered', 'failed')),

    -- Outstanding work is due at some point, and finished work is due at none.
    -- Without this a delivery could be pending forever with nothing to pick it
    -- up, which is the failure mode a queue must not be able to reach.
    CONSTRAINT webhook_deliveries_due_when_pending CHECK (
        (status = 'pending' AND next_attempt_at IS NOT NULL)
        OR (status <> 'pending' AND next_attempt_at IS NULL)
    ),

    -- A delivered row says when, and one that is not delivered says nothing.
    CONSTRAINT webhook_deliveries_delivery_timed CHECK (
        (status = 'delivered' AND delivered_at IS NOT NULL)
        OR (status <> 'delivered' AND delivered_at IS NULL)
    ),

    -- A finished delivery was attempted at least once. Reaching a terminal
    -- state without trying would mean Convia gave up on something it never sent.
    CONSTRAINT webhook_deliveries_attempted CHECK (
        status = 'pending' OR attempts > 0
    ),

    CONSTRAINT webhook_deliveries_attempts_counted CHECK (attempts >= 0),
    CONSTRAINT webhook_deliveries_updated_at_order CHECK (updated_at >= created_at)
);

-- The worker's only query: the oldest outstanding deliveries that are due.
-- Partial, so finished deliveries leave it entirely and the index stays the
-- size of the backlog rather than the size of the history.
CREATE INDEX webhook_deliveries_due_idx
    ON webhook_deliveries (next_attempt_at, id)
    WHERE status = 'pending';

-- An application reading what Convia tried to tell it, newest first.
CREATE INDEX webhook_deliveries_application_created_at_id_idx
    ON webhook_deliveries (application_id, created_at DESC, id DESC);

-- The same, narrowed to one endpoint, which is how a failing destination is
-- investigated.
CREATE INDEX webhook_deliveries_endpoint_created_at_id_idx
    ON webhook_deliveries (endpoint_id, created_at DESC, id DESC);

-- +goose Down

DROP TABLE webhook_deliveries;
DROP TABLE webhook_endpoints;
