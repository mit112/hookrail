#!/usr/bin/env bash
set -euo pipefail
# verify-prod-render.sh — kubeconform the prod overlay + assert no dev artifacts leak through.

RENDERED=$(kubectl kustomize deploy/k8s/overlays/prod)

# 1. kubeconform
echo "$RENDERED" | kubeconform -strict -summary || {
  echo "FAIL: kubeconform found schema errors in prod render" >&2
  exit 1
}

# 2. cloudflared Deployment must exist
if ! echo "$RENDERED" | grep -q 'app: cloudflared'; then
  echo "FAIL: prod render missing cloudflared label" >&2
  exit 1
fi
echo "OK: cloudflared present"

# 3. prod NetworkPolicy must exist
if ! echo "$RENDERED" | grep -q 'name: allow-cloudflared-to-api'; then
  echo "FAIL: prod render missing allow-cloudflared-to-api NetworkPolicy" >&2
  exit 1
fi
echo "OK: prod networkpolicy present"

# 4. Guard: NO dev-only resources
for name in test-receiver dev-secret dashboard-keygen db-bootstrap ephemeral-jobs-netpol test-receiver-netpol; do
  if echo "$RENDERED" | grep -q "name: $name"; then
    echo "FAIL: prod render contains $name (dev artifact leak)" >&2
    exit 1
  fi
done
echo "OK: no dev artifacts"

echo "PASS: prod render verification complete"
