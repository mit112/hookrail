#!/usr/bin/env bash
set -euo pipefail
CLUSTER=hookrail-e2e; NS=hookrail
KCFG=$(mktemp); export KUBECONFIG="$KCFG"
trap 'rm -f "$KCFG"; k3d cluster delete "$CLUSTER" >/dev/null 2>&1 || true' EXIT   # armed BEFORE create (M-C5 lesson)
k3d cluster create "$CLUSTER" --wait --kubeconfig-switch-context=false
# k3d cluster create writes to $KUBECONFIG when set (avoids ~/.kube permissions)
[ -s "$KCFG" ] || { echo "kubeconfig not populated"; exit 1; }
DOCKER_BUILDKIT=0 docker build -t hookrail:e2e .
DOCKER_BUILDKIT=0 docker build -t hookrail-dashboard:e2e --target dashboard .
# Pre-pull + import all images into k3d (Deployments use imagePullPolicy: Never;
# Jobs use it for containers/0 too, so external images must be imported)
k3d image import hookrail:e2e hookrail-dashboard:e2e -c "$CLUSTER"
docker pull rancher/kubectl:v1.33.1
docker pull busybox:1.36
docker pull postgres:16-alpine
docker pull redis:7-alpine
docker pull otel/opentelemetry-collector-contrib:0.104.0
docker pull prom/prometheus:v2.53.0
docker pull jaegertracing/all-in-one:1.58
k3d image import \
  rancher/kubectl:v1.33.1 busybox:1.36 postgres:16-alpine redis:7-alpine \
  otel/opentelemetry-collector-contrib:0.104.0 prom/prometheus:v2.53.0 \
  jaegertracing/all-in-one:1.58 \
  -c "$CLUSTER"
# Wait for API server readiness
for i in $(seq 1 30); do kubectl version --request-timeout=2s >/dev/null 2>&1 && break; sleep 2; done
kubectl version --request-timeout=2s >/dev/null 2>&1 || { echo "API server never ready"; exit 1; }
kubectl apply -k deploy/k8s/overlays/ephemeral
# Wait for postgres StatefulSet to be ready before migrate (first-time init + image pull)
kubectl -n "$NS" wait --for=condition=ready pod -l app=postgres --timeout=300s
kubectl -n "$NS" wait --for=condition=complete --timeout=300s job/migrate
# Bootstrap hookrail_app password (0004 migration creates role NOLOGIN; set pw for app deployments)
kubectl -n "$NS" wait --for=condition=complete --timeout=120s job/db-bootstrap
# host-side producer-key provisioning (the app image has no kubectl — fold #8):
kubectl -n "$NS" wait --for=condition=complete --timeout=120s job/dashboard-keygen
KEY=$(kubectl -n "$NS" logs job/dashboard-keygen | /usr/bin/sed -n 's/^producer_key=//p')
[ -n "$KEY" ] || { echo "no producer key from keygen job"; exit 1; }
kubectl -n "$NS" create secret generic hookrail-dashboard-producer-key --from-literal=producer_key="$KEY"
for d in api worker scheduler admin dashboard; do kubectl -n "$NS" rollout status deploy/$d --timeout=120s; done
# e2e flow over port-forward (NOT NodePort — fold #10; port-forward bypasses the default-deny netpol):
kubectl -n "$NS" port-forward svc/api 18080:8080 >/dev/null 2>&1 & PF=$!; sleep 3
ADMIN_PF_PORT=18082; kubectl -n "$NS" port-forward svc/admin $ADMIN_PF_PORT:8082 >/dev/null 2>&1 & PFA=$!; sleep 3
# verify port-forwards are actually working
echo "DEBUG: checking port-forwards..." >&2
/usr/bin/curl -s --connect-timeout 5 --max-time 10 localhost:18080/healthz || echo "DEBUG: api healthz failed" >&2
/usr/bin/curl -s --connect-timeout 5 --max-time 10 localhost:$ADMIN_PF_PORT/healthz || echo "DEBUG: admin healthz failed" >&2
trap 'kill $PF $PFA 2>/dev/null; rm -f "$KCFG"; k3d cluster delete "$CLUSTER" >/dev/null 2>&1 || true' EXIT
TOK=dev-admin-token-001   # matches dev-secret
# create endpoint (→ test-receiver) + subscription via admin:
EP_RESP=$(/usr/bin/curl -s --connect-timeout 5 --max-time 30 -XPOST localhost:$ADMIN_PF_PORT/v1/endpoints -H "Authorization: Bearer $TOK" \
     -H 'Content-Type: application/json' -d '{"url":"http://test-receiver:9090/succeed"}')
echo "DEBUG: endpoint response: $EP_RESP" >&2
EP=$(echo "$EP_RESP" | jq -r .id)
echo "DEBUG: endpoint id: $EP" >&2
SUB_RESP=$(/usr/bin/curl -s --connect-timeout 5 --max-time 30 -XPOST localhost:$ADMIN_PF_PORT/v1/subscriptions -H "Authorization: Bearer $TOK" \
     -H 'Content-Type: application/json' -d "{\"endpoint_id\":\"$EP\",\"topic_pattern\":\"e2e.*\"}")
echo "DEBUG: subscription response: $SUB_RESP" >&2
# ingest via the public producer API with the provisioned key, then poll the event to succeeded:
INGEST_RESP=$(/usr/bin/curl -s --connect-timeout 5 --max-time 30 -XPOST localhost:18080/v1/events -H "Authorization: Bearer $KEY" \
     -H 'Content-Type: application/json' -d '{"topic":"e2e.test","payload":{"hi":1}}')
echo "DEBUG: ingest response: $INGEST_RESP" >&2
EV=$(echo "$INGEST_RESP" | jq -r .event_id)
echo "DEBUG: event_id: $EV" >&2
for i in $(seq 1 30); do
  STATUS_RESP=$(/usr/bin/curl -s --connect-timeout 5 --max-time 30 localhost:18080/v1/events/"$EV" -H "Authorization: Bearer $KEY")
  S=$(echo "$STATUS_RESP" | jq -r '.deliveries[0].state // "null"')
  echo "DEBUG: poll $i state=$S response=$STATUS_RESP" >&2
  [ "$S" = succeeded ] && { echo "k8s-e2e OK"; exit 0; }
  sleep 2
done
echo "delivery never reached succeeded (last=$S)"; exit 1
