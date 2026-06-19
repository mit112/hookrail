#!/usr/bin/env bash
# Brings up compose with dashboard, runs Playwright e2e, tears down.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
COMPOSE="docker compose -f $ROOT/deploy/compose/docker-compose.yml"
export HOOKRAIL_MASTER_KEY="${HOOKRAIL_MASTER_KEY:-000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f}"

$COMPOSE up -d --build
trap '$COMPOSE down -v' EXIT

# wait for the dashboard health endpoint
for i in $(seq 1 90); do
  curl -fsS http://localhost:8085/healthz >/dev/null 2>&1 && break
  sleep 1
done

cd "$ROOT/clients/web"
npx playwright install --with-deps chromium
npm run e2e
# teardown via trap; verify 0 containers after
