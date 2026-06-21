DROP INDEX deliveries_ordered_blocking_idx;
DROP INDEX deliveries_ordered_idx;
DROP TABLE ordered_key_state;
ALTER TABLE deliveries DROP COLUMN skipped_at, DROP COLUMN skip_reason, DROP COLUMN skipped_by, DROP COLUMN ordering_seq, DROP COLUMN ordering_key;
ALTER TABLE subscriptions DROP COLUMN ordered;
