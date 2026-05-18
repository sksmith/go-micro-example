-- DSN-017: dedupe table for at-least-once consumer transports.
-- The (event_id, consumer_group) unique constraint is what gives
-- us idempotency: the same event delivered twice to the same group
-- inserts once; second insert hits the unique violation and the
-- caller knows to skip the work. The dedupe insert lives in the
-- same transaction as the side-effecting writes so a crash mid-
-- handler rolls back the dedupe row alongside the work, leaving the
-- consumer free to retry.
CREATE TABLE IF NOT EXISTS processed_events (
    event_id       TEXT      NOT NULL,
    consumer_group TEXT      NOT NULL,
    processed_at   TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    PRIMARY KEY (event_id, consumer_group)
);

-- DSN-017's retention/cleanup job uses this index to delete rows
-- older than the configured window.
CREATE INDEX IF NOT EXISTS processed_events_processed_at_idx
    ON processed_events (processed_at);
