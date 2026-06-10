#!/usr/bin/env bash
# Brings up compose, seeds three pipelines (happy/retry/dlq), runs e2e, tears down.
set -euo pipefail
cd "$(dirname "$0")/.."

COMPOSE="docker compose -f deploy/compose/docker-compose.yml"
export HOOKRAIL_MASTER_KEY="${HOOKRAIL_MASTER_KEY:-000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f}"

$COMPOSE up -d --build
trap '$COMPOSE down -v' EXIT

# wait for the API
for i in $(seq 1 60); do
  curl -fsS http://localhost:8080/readyz >/dev/null 2>&1 && break
  sleep 1
done

seed() { # seed <url> <topic> → prints producer_key (env comes from the api service)
  $COMPOSE run --rm api hookrail-ctl seed -url "$1" -topic "$2" \
    | awk -F= '/^producer_key=/{print $2}'
}

export E2E_PRODUCER_KEY=$(seed "http://test-receiver:9090/succeed"  "demo.*")
export E2E_RETRY_KEY=$(seed   "http://test-receiver:9090/fail/2"   "demo-retry.*")
export E2E_DLQ_KEY=$(seed     "http://test-receiver:9090/redirect" "demo-dlq.*")

go test -tags e2e ./test/e2e -v -count=1
