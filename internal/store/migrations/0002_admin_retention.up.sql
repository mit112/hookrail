-- Slice A admin + retention. Plain CREATE INDEX; Slice E adds CONCURRENTLY +
-- lock_timeout (design D-A5/F8). ALTER TYPE ADD VALUE is safe here: the value
-- is only used at runtime, never inside this migration's transaction.
ALTER TYPE delivery_state ADD VALUE IF NOT EXISTS 'cancelled';
ALTER TABLE endpoints     ADD COLUMN deleted_at TIMESTAMPTZ;
ALTER TABLE subscriptions ADD COLUMN deleted_at TIMESTAMPTZ;
ALTER TABLE deliveries    ADD COLUMN endpoint_id TEXT REFERENCES endpoints(id);
ALTER TABLE deliveries    ADD COLUMN attempts_truncated_at TIMESTAMPTZ;
ALTER TABLE dead_letters  ADD COLUMN endpoint_id TEXT REFERENCES endpoints(id);

UPDATE deliveries d SET endpoint_id = s.endpoint_id FROM subscriptions s
  WHERE s.id = d.subscription_id AND d.endpoint_id IS NULL;
UPDATE dead_letters dl SET endpoint_id = d.endpoint_id FROM deliveries d
  WHERE d.id = dl.delivery_id AND dl.endpoint_id IS NULL;

ALTER TABLE subscriptions ADD CONSTRAINT chk_rate         CHECK (rate_limit_rps IS NULL OR rate_limit_rps > 0);
ALTER TABLE subscriptions ADD CONSTRAINT chk_max_attempts CHECK (max_attempts BETWEEN 1 AND 100);

CREATE INDEX idx_deliveries_state_id     ON deliveries (state, id);
CREATE INDEX idx_deliveries_endpoint_id  ON deliveries (endpoint_id, id);
CREATE INDEX idx_deliveries_subscription ON deliveries (subscription_id);
CREATE INDEX idx_subscriptions_endpoint  ON subscriptions (endpoint_id);
CREATE INDEX idx_events_topic_id         ON events (topic, id);
CREATE INDEX idx_events_retain           ON events (created_at) WHERE payload_size > 0;
CREATE INDEX idx_attempts_completed      ON delivery_attempts (completed_at);
CREATE INDEX idx_idem_expires            ON idempotency_keys (expires_at);
CREATE INDEX idx_dead_letters_endpoint   ON dead_letters (endpoint_id, id);
