#!/usr/bin/env bash
# Non-vacuous docs verifier. Deterministic, no Docker, CI-safe. Portable (no mapfile / bash4).
# NOTE: deliberately NOT `set -e` — both stages must run and report even if the first fails.
set -uo pipefail
cd "$(dirname "$0")/.."

# Doc set assembled by the python lib (single source of truth, portable — no bash globbing of `**`).
DOCS="$(python3 scripts/docs_verify_lib.py --list)"
echo "==> docs set:"; printf '   %s\n' $DOCS

echo "==> markdownlint"
# shellcheck disable=SC2086  # paths have no spaces; intentional word-split
npx --yes markdownlint-cli2@0.14.0 $DOCS; lint=$?

echo "==> claims/link/leak"
python3 scripts/docs_verify_lib.py $DOCS; checks=$?

if [ "$lint" -ne 0 ] || [ "$checks" -ne 0 ]; then
  echo "docs-verify: FAIL (markdownlint=$lint claims/link/leak=$checks)"
  exit 1
fi
echo "docs-verify: ALL GREEN"
