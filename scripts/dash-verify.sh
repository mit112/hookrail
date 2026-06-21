#!/usr/bin/env bash
# Verifies curated Grafana dashboards reference only declared+emitted metrics and real uids.
set -euo pipefail
cd "$(dirname "$0")/.."

python3 scripts/dash_verify_lib.py                       # real dashboards must pass

if python3 scripts/dash_verify_lib.py scripts/testdata >/dev/null 2>&1; then
  echo "dash-verify SELF-TEST FAILED: bad dashboard passed" >&2
  exit 1
fi
echo "dash-verify self-test OK (bad dashboard correctly rejected)"
