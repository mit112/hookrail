ALTER TABLE subscriptions DROP CONSTRAINT IF EXISTS chk_rate_limit_rps_bounds;
ALTER TABLE subscriptions ADD CONSTRAINT chk_rate CHECK (rate_limit_rps IS NULL OR rate_limit_rps > 0);
