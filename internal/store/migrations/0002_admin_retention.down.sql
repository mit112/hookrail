-- 'cancelled' is NOT droppable in PostgreSQL — documented, left in place.
DROP INDEX IF EXISTS idx_dead_letters_endpoint;
DROP INDEX IF EXISTS idx_idem_expires;
DROP INDEX IF EXISTS idx_attempts_completed;
DROP INDEX IF EXISTS idx_events_retain;
DROP INDEX IF EXISTS idx_events_topic_id;
DROP INDEX IF EXISTS idx_subscriptions_endpoint;
DROP INDEX IF EXISTS idx_deliveries_subscription;
DROP INDEX IF EXISTS idx_deliveries_endpoint_id;
DROP INDEX IF EXISTS idx_deliveries_state_id;
ALTER TABLE subscriptions DROP CONSTRAINT IF EXISTS chk_max_attempts;
ALTER TABLE subscriptions DROP CONSTRAINT IF EXISTS chk_rate;
ALTER TABLE dead_letters  DROP COLUMN IF EXISTS endpoint_id;
ALTER TABLE deliveries    DROP COLUMN IF EXISTS attempts_truncated_at;
ALTER TABLE deliveries    DROP COLUMN IF EXISTS endpoint_id;
ALTER TABLE subscriptions DROP COLUMN IF EXISTS deleted_at;
ALTER TABLE endpoints     DROP COLUMN IF EXISTS deleted_at;
-- NOTE: 'cancelled' remains in delivery_state (PostgreSQL cannot remove an enum value).
