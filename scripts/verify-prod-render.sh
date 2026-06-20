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

# 3. prod NetworkPolicy must exist (both cloudflared allows + default-deny)
for np in allow-cloudflared-to-api allow-cloudflared-to-dashboard default-deny-ingress; do
  if ! echo "$RENDERED" | grep -q "name: $np"; then
    echo "FAIL: prod render missing NetworkPolicy $np" >&2
    exit 1
  fi
done
echo "OK: prod networkpolicies present (cloudflared allows + default-deny)"

# 4. Exposure contract: cloudflared ingress must NOT route admin.
#    Match only the cloudflared ingress form (`service: http://admin...`); the
#    dashboard's in-cluster BFF env (HOOKRAIL_ADMIN_URL: http://admin:8082) is
#    legitimate and must not trip this guard.
if echo "$RENDERED" | grep -qE 'service:[[:space:]]*http://admin'; then
  echo "FAIL: cloudflared routes admin (admin must never be externally exposed)" >&2
  exit 1
fi
echo "OK: admin not routed by cloudflared"

# 5. Guard: NO inline Secret objects (all prod secrets are attended refs).
#    dev-secret.yaml renders Secrets named hookrail-{db,app,admin,dashboard}, so a
#    by-name grep can't catch it — assert there are zero Secret objects instead.
if echo "$RENDERED" | grep -qE '^kind: Secret$'; then
  echo "FAIL: prod render contains an inline Secret (dev-secret leak; prod secrets must be attended)" >&2
  exit 1
fi
echo "OK: no inline Secret objects"

# 6. Guard: NO dev-only workloads
for name in test-receiver dashboard-keygen db-bootstrap ephemeral-jobs-netpol test-receiver-netpol; do
  if echo "$RENDERED" | grep -q "name: $name"; then
    echo "FAIL: prod render contains $name (dev artifact leak)" >&2
    exit 1
  fi
done
echo "OK: no dev artifacts"

echo "PASS: prod render verification complete"
