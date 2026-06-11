#!/usr/bin/env bash
# Baseline run per §11 protocol.
#   Local smoke (default): compose on this machine.
#   Published run: run THIS script on the GENERATOR machine with API_URL,
#     PG_DSN, RECEIVER_URL pointing at the target box and HOOKRAIL_MASTER_KEY
#     set to the TARGET's master key. Seeding goes straight to the target's
#     PG via PG_DSN — remote mode never touches a database on this machine.
# Usage: scripts/baseline/run.sh <fanout: 1|3> <rate>
# Requires on this machine: go, k6, psql, curl.
set -euo pipefail
cd "$(dirname "$0")/../.."

FANOUT="${1:?fanout (1 or 3)}"
RATE="${2:?events/s}"
API_URL="${API_URL:-http://localhost:8080}"
RECEIVER_URL="${RECEIVER_URL:-http://localhost:9090}"
# how the TARGET's workers reach the receiver (compose-internal by default;
# distinct from RECEIVER_URL, which is how THIS machine reaches /stats)
RECEIVER_INTERNAL_URL="${RECEIVER_INTERNAL_URL:-http://test-receiver:9090/succeed}"
PG_DSN="${PG_DSN:-postgres://hookrail:hookrail@localhost:5432/hookrail?sslmode=disable}"
SUSTAINED_SECONDS=600

# fresh run: deactivate load subscriptions from any previous run (fan-out
# contamination), then seed exactly FANOUT subscriptions — directly against
# PG_DSN, so remote targets are seeded remotely (ctl is just a PG client;
# it needs the target's master key to mint endpoint secrets)
psql "$PG_DSN" -c "UPDATE subscriptions SET active = false WHERE topic_pattern = 'load.*'"
KEY=""
for i in $(seq 1 "$FANOUT"); do
  OUT=$(DATABASE_URL="$PG_DSN" REDIS_ADDR="${REDIS_ADDR:-unused:0}" \
        HOOKRAIL_MASTER_KEY="${HOOKRAIL_MASTER_KEY:?set to the TARGET master key}" \
        HOOKRAIL_ALLOW_HTTP=true \
        go run ./cmd/hookrail-ctl seed -url "$RECEIVER_INTERNAL_URL" -topic "load.*")
  KEY=$(echo "$OUT" | awk -F= '/^producer_key=/{print $2}')
done

dup() { curl -fsS "$RECEIVER_URL/stats" | sed -E 's/.*"duplicates":([0-9]+).*/\1/'; }

# this run's epoch on PG's clock: every drain check scopes to it, so stale
# non-terminal load.k6 rows from an earlier aborted run can neither wedge
# nor skew this run
RUN_START=$(psql "$PG_DSN" -Atc "SELECT now()")

# warm-up: a SEPARATE k6 invocation, so k6 startup cost cannot leak warm-up
# traffic into the measurement window
API_URL="$API_URL" PRODUCER_KEY="$KEY" RATE="$RATE" DURATION=2m \
  k6 run deploy/k6/ingest.js

# drain warm-up traffic (healthy consumers: near-instant), THEN snapshot the
# duplicate ledger — the duplicate metric must cover the sustained window
# only, like every other published number. ENFORCED: undrained warm-up means
# late warm-up receipts would leak into sustained metrics, so timeout = abort.
LEFT=unknown
for i in $(seq 1 24); do
  LEFT=$(psql "$PG_DSN" -Atc "SELECT count(*) FROM deliveries d
    JOIN events e ON e.id = d.event_id
    WHERE e.topic = 'load.k6' AND e.created_at >= '$RUN_START'
      AND d.state NOT IN ('succeeded','dead_lettered')")
  [ "$LEFT" = "0" ] && break
  sleep 5
done
if [ "$LEFT" != "0" ]; then
  echo "WARM-UP DRAIN FAILED: $LEFT deliveries still non-terminal — run is INVALID" >&2
  exit 1
fi
DUP_BEFORE=$(dup)

# the sustained window opens NOW, on PG's clock — generator and target clocks
# are different domains and are never compared against each other (§11)
SINCE=$(psql "$PG_DSN" -Atc "SELECT now()")
echo "sustained window starts (PG clock): $SINCE"

API_URL="$API_URL" PRODUCER_KEY="$KEY" RATE="$RATE" DURATION="${SUSTAINED_SECONDS}s" \
  k6 run deploy/k6/ingest.js

# drain VERIFIER: poll until nothing non-terminal remains; FAIL on timeout —
# an undrained run is invalid and must never publish numbers
echo "waiting for queue drain..."
LEFT=unknown
for i in $(seq 1 120); do
  LEFT=$(psql "$PG_DSN" -Atc "SELECT count(*) FROM deliveries d
    JOIN events e ON e.id = d.event_id
    WHERE e.topic = 'load.k6' AND e.created_at >= '$RUN_START'
      AND d.state NOT IN ('succeeded','dead_lettered')")
  [ "$LEFT" = "0" ] && break
  sleep 5
done
if [ "$LEFT" != "0" ]; then
  echo "DRAIN FAILED: $LEFT deliveries still non-terminal — run is INVALID" >&2
  exit 1
fi

DUP_AFTER=$(dup)
echo "duplicate receipts during run (receiver ledger): $((DUP_AFTER - DUP_BEFORE))"

psql "$PG_DSN" -v since="$SINCE" -v sustained_seconds="$SUSTAINED_SECONDS" \
  -f scripts/baseline/report.sql
