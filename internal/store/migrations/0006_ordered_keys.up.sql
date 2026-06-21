ALTER TABLE subscriptions ADD COLUMN ordered BOOLEAN NOT NULL DEFAULT false;

ALTER TABLE deliveries
  ADD COLUMN ordering_key TEXT NULL,
  ADD COLUMN ordering_seq BIGINT NULL,
  ADD COLUMN skipped_by   TEXT NULL,
  ADD COLUMN skip_reason  TEXT NULL,
  ADD COLUMN skipped_at   TIMESTAMPTZ NULL;

CREATE TABLE ordered_key_state (
  subscription_id  TEXT        NOT NULL,
  ordering_key     TEXT        NOT NULL,
  seq_counter      BIGINT      NOT NULL DEFAULT 0,
  cursor_seq       BIGINT      NOT NULL DEFAULT 1,
  head_delivery_id TEXT        NULL,
  blocked_reason   TEXT        NULL,
  blocked_since    TIMESTAMPTZ NULL,
  backlog_count    INTEGER     NOT NULL DEFAULT 0,
  updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (subscription_id, ordering_key)
);

CREATE INDEX deliveries_ordered_idx
  ON deliveries (subscription_id, ordering_key, ordering_seq)
  WHERE ordering_key IS NOT NULL;

CREATE INDEX deliveries_ordered_blocking_idx
  ON deliveries (subscription_id, ordering_key, ordering_seq)
  WHERE ordering_key IS NOT NULL
    AND state IN ('pending','in_flight','retry_scheduled','dead_lettered');
