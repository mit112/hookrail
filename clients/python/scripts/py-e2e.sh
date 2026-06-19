#!/usr/bin/env bash
# Bring up the compose stack, seed a producer key, run the Python SDK e2e, tear down.
set -euo pipefail
cd "$(dirname "$0")/../../.."   # repo root
ROOT="$(pwd)"
COMPOSE="docker compose -f $ROOT/deploy/compose/docker-compose.yml"
export HOOKRAIL_MASTER_KEY="${HOOKRAIL_MASTER_KEY:-000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f}"

$COMPOSE up -d --build
trap '$COMPOSE down -v' EXIT
for _ in $(seq 1 60); do
  curl -fsS http://localhost:8080/readyz >/dev/null 2>&1 && break
  sleep 1
done
KEY="$($COMPOSE run --rm api hookrail-ctl seed -url http://test-receiver:9090/succeed -topic 'demo.python.*' \
       | awk -F= '/^producer_key=/{print $2}')"
export HOOKRAIL_API_KEY="$KEY"
export HOOKRAIL_BASE_URL="http://localhost:8080"
cd "$ROOT/clients/python" && uv run pytest -q -m e2e
