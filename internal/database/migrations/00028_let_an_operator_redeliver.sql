-- +goose Up

-- A delivery made again, on purpose, by somebody who was asked why.
--
-- Redelivering does not reopen the original. The original is the record of what
-- happened -- delivered, or given up on after its last attempt -- and changing
-- it would rewrite that. A redelivery is a new delivery of the same event, with
-- the same bytes, and this column is what says it is one: a tenant reading its
-- deliveries sees the second one and knows where it came from.
--
-- **It is a new delivery to the consumer too.** It arrives with its own
-- Convia-Delivery header, so a consumer that deduplicates on that header, as
-- docs/webhooks.md tells it to, handles it again -- which is the point when the
-- original failed. A consumer that must not handle one event twice has the
-- event's own identifier in the body, unchanged.
--
-- It cascades with the original for the reason every delivery cascades with
-- its endpoint: a redelivery is history about a destination, and deleting the
-- destination is deleting its history.

ALTER TABLE webhook_deliveries
    ADD COLUMN redelivery_of TEXT REFERENCES webhook_deliveries (id) ON DELETE CASCADE;

ALTER TABLE webhook_deliveries
    ADD CONSTRAINT webhook_deliveries_redelivery_elsewhere CHECK (redelivery_of IS NULL OR redelivery_of <> id);

-- +goose Down

ALTER TABLE webhook_deliveries DROP CONSTRAINT webhook_deliveries_redelivery_elsewhere;
ALTER TABLE webhook_deliveries DROP COLUMN redelivery_of;
