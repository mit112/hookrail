CREATE TYPE delivery_state AS ENUM
  ('pending','in_flight','retry_scheduled','succeeded','dead_lettered');
CREATE TYPE attempt_status AS ENUM
  ('success','retryable_failure','permanent_failure');

CREATE TABLE producer_keys (
  id         TEXT PRIMARY KEY,
  key_hash   BYTEA NOT NULL UNIQUE,           -- SHA-256 of the plaintext key (§4)
  name       TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  revoked_at TIMESTAMPTZ
);

CREATE TABLE endpoints (
  id                TEXT PRIMARY KEY,
  url               TEXT NOT NULL,             -- re-validated against SSRF policy at write AND dial (§5)
  secret_ciphertext BYTEA NOT NULL,            -- AES-256-GCM (§4)
  description       TEXT NOT NULL DEFAULT '',
  created_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE subscriptions (
  id             TEXT PRIMARY KEY,
  topic_pattern  TEXT NOT NULL,                -- exact topic or trailing-* prefix glob ("orders.*")
  endpoint_id    TEXT NOT NULL REFERENCES endpoints(id),
  backoff_policy JSONB,
  max_attempts   INT NOT NULL DEFAULT 8,
  rate_limit_rps DOUBLE PRECISION,
  active         BOOLEAN NOT NULL DEFAULT true
);

CREATE TABLE events (
  id              TEXT PRIMARY KEY,            -- ULID, generated in app code
  producer_key_id TEXT NOT NULL REFERENCES producer_keys(id),
  topic           TEXT NOT NULL,
  payload         JSONB NOT NULL,              -- capped at 256KB at ingress (§5)
  payload_size    INT NOT NULL,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- The durable obligation "event X must reach subscription Y" (§5).
-- Created in the ingest transaction; the state machine (§6) lives here.
CREATE TABLE deliveries (
  id              TEXT PRIMARY KEY,
  event_id        TEXT NOT NULL REFERENCES events(id),
  subscription_id TEXT NOT NULL REFERENCES subscriptions(id),
  state           delivery_state NOT NULL DEFAULT 'pending',
  attempt_count   INT NOT NULL DEFAULT 0,
  -- monotonic fence: bumped on EVERY claim, never decremented, never reset
  -- (manual replay resets attempt_count, §6 — the fence keeps counting)
  claim_version   BIGINT NOT NULL DEFAULT 0,
  next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  lease_until     TIMESTAMPTZ,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- the sweeper's due-scan (§5 "indexes for hot queries")
CREATE INDEX idx_deliveries_due ON deliveries (state, next_attempt_at);
CREATE INDEX idx_deliveries_event ON deliveries (event_id);

CREATE TABLE idempotency_keys (
  producer_key_id   TEXT NOT NULL REFERENCES producer_keys(id),
  idem_key          TEXT NOT NULL,
  body_hash         BYTEA NOT NULL,            -- same key + different hash → 409 (§6)
  event_id          TEXT NOT NULL REFERENCES events(id),
  response_snapshot JSONB NOT NULL,            -- replay returns this verbatim
  expires_at        TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (producer_key_id, idem_key)
);

CREATE TABLE delivery_attempts (
  id           BIGSERIAL PRIMARY KEY,
  delivery_id   TEXT NOT NULL REFERENCES deliveries(id),
  attempt_no    INT NOT NULL,
  claim_version BIGINT NOT NULL,
  status       attempt_status NOT NULL,
  http_status  INT,
  latency_ms   INT,
  error_class  TEXT,
  requested_at TIMESTAMPTZ NOT NULL,
  completed_at TIMESTAMPTZ NOT NULL
);
-- UNIQUE per claim_version, NOT per attempt_no: each claim produces at most
-- one completion, and claim_version never resets — so manual replay (§6:
-- attempt counter resets) can reuse attempt numbers without conflicting
CREATE UNIQUE INDEX idx_attempts_claim ON delivery_attempts (delivery_id, claim_version);
CREATE INDEX idx_attempts_delivery ON delivery_attempts (delivery_id, attempt_no);

CREATE TABLE dead_letters (
  id          BIGSERIAL PRIMARY KEY,
  delivery_id TEXT NOT NULL UNIQUE REFERENCES deliveries(id),
  final_error TEXT NOT NULL,
  dead_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
  replayed_at TIMESTAMPTZ
);
