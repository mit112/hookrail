#!/usr/bin/env bash
# Build artifacts must install cleanly into a fresh environment and import.
set -euo pipefail
cd "$(dirname "$0")/.."
WHEEL="$(ls -1 dist/*.whl | head -1)"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
uv venv "$TMP/venv"
VPY="$TMP/venv/bin/python"
"$TMP/venv/bin/python" -m ensurepip >/dev/null 2>&1 || true
uv pip install --python "$VPY" "$WHEEL"
"$VPY" -c "import hookrail; from hookrail import Hookrail, AsyncHookrail, verify_signature; print('smoke ok', hookrail.__version__)"
