-- Latency, published as "INGEST COMMIT → CONSUMER COMPLETION".
-- §11 defines "202 response → consumer 2xx receipt"; the commit strictly
-- precedes the 202 (the response is sent after commit), so this measurement
-- starts earlier and the published number errs larger, never smaller.
-- events.created_at is transaction time. Window: sustained phase only
-- (:since = sustained start captured on PG's clock).
\set ON_ERROR_STOP on

WITH e2e AS (
  SELECT da.completed_at - ev.created_at AS latency
  FROM deliveries d
  JOIN events ev ON ev.id = d.event_id
  JOIN delivery_attempts da ON da.delivery_id = d.id AND da.status = 'success'
  WHERE ev.topic = 'load.k6' AND ev.created_at >= :'since'::timestamptz
)
SELECT 'commit_to_completion_latency' AS metric,
  percentile_cont(0.50) WITHIN GROUP (ORDER BY latency) AS p50,
  percentile_cont(0.95) WITHIN GROUP (ORDER BY latency) AS p95,
  percentile_cont(0.99) WITHIN GROUP (ORDER BY latency) AS p99,
  count(*) AS n
FROM e2e;

-- First-attempt dispatch latency: ingest commit → first attempt started.
WITH first_dispatch AS (
  SELECT da.requested_at - ev.created_at AS latency
  FROM deliveries d
  JOIN events ev ON ev.id = d.event_id
  JOIN delivery_attempts da ON da.delivery_id = d.id AND da.attempt_no = 1
  WHERE ev.topic = 'load.k6' AND ev.created_at >= :'since'::timestamptz
)
SELECT 'first_dispatch' AS metric,
  percentile_cont(0.50) WITHIN GROUP (ORDER BY latency) AS p50,
  percentile_cont(0.95) WITHIN GROUP (ORDER BY latency) AS p95,
  percentile_cont(0.99) WITHIN GROUP (ORDER BY latency) AS p99,
  count(*) AS n
FROM first_dispatch;

-- PG-side duplicate PROXY only: with completion fencing, true duplicate HTTP
-- receipts usually still record a single success row here. The authoritative
-- duplicate number is the receiver's /stats ledger, captured by run.sh.
SELECT 'multi_success_deliveries' AS metric,
  count(*) FILTER (WHERE c > 1) AS duplicates,
  count(*) AS total,
  round(100.0 * count(*) FILTER (WHERE c > 1) / greatest(count(*),1), 4) AS pct
FROM (
  SELECT d.id, count(*) AS c
  FROM deliveries d
  JOIN delivery_attempts da ON da.delivery_id = d.id AND da.status = 'success'
  JOIN events ev ON ev.id = d.event_id
  WHERE ev.topic = 'load.k6' AND ev.created_at >= :'since'::timestamptz
  GROUP BY d.id
) x;

-- Drain check (§11 zero-loss definition): nothing stranded.
SELECT 'stranded' AS metric, count(*) AS n
FROM deliveries d JOIN events ev ON ev.id = d.event_id
WHERE ev.topic = 'load.k6' AND ev.created_at >= :'since'::timestamptz
  AND d.state NOT IN ('succeeded','dead_lettered')
  AND d.next_attempt_at <= now();

-- Achieved sustained ingest throughput: accepted events over the FIXED
-- sustained duration. (Dividing by max-min event span understates the window
-- and overstates the rate; the denominator is the protocol's 600s.)
SELECT 'sustained_ingest_rate' AS metric,
  round(count(*)::numeric / :sustained_seconds, 1) AS events_per_sec,
  count(*) AS n
FROM events
WHERE topic = 'load.k6' AND created_at >= :'since'::timestamptz;
