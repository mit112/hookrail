-- R3: per-producer-key topic authorization. A key may publish to topic T iff
-- some row's topic_pattern matches T (MatchTopic semantics). Zero rows = deny
-- (fail-closed, enforced in Go). Backfill '*' for every pre-R3 key so existing
-- keys are unchanged.
CREATE TABLE producer_key_scopes (
  producer_key_id TEXT NOT NULL REFERENCES producer_keys(id) ON DELETE CASCADE,
  topic_pattern   TEXT NOT NULL,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (producer_key_id, topic_pattern)
);

INSERT INTO producer_key_scopes (producer_key_id, topic_pattern)
  SELECT id, '*' FROM producer_keys;
