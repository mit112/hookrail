-- Tighten rate_limit_rps bounds from > 0 to [0.01, 1_000_000]
-- so TTL math in the global Redis token bucket is safe.
ALTER TABLE subscriptions DROP CONSTRAINT IF EXISTS chk_rate;
ALTER TABLE subscriptions ADD CONSTRAINT chk_rate_limit_rps_bounds
  CHECK (rate_limit_rps IS NULL OR (rate_limit_rps >= 0.01 AND rate_limit_rps <= 1000000));
