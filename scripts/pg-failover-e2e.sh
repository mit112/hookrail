#!/usr/bin/env bash
# CNPG Postgres-failover chaos e2e: brings up the full stack on k3d (CNPG 3-instance
# sync-quorum cluster), provisions a producer key, then runs the k8schaos failover
# oracle (test/chaos/k8s). Mirrors scripts/k8s-e2e.sh's deploy path. Ephemeral only —
# the live Mac-mini cutover is always attended.
set -euo pipefail
CLUSTER=hookrail-pgfo; NS=hookrail
KCFG=$(mktemp); export KUBECONFIG="$KCFG"
trap 'rm -f "$KCFG"; k3d cluster delete "$CLUSTER" >/dev/null 2>&1 || true' EXIT # armed BEFORE create
k3d cluster create "$CLUSTER" --wait --kubeconfig-switch-context=false
[ -s "$KCFG" ] || { echo "kubeconfig not populated"; exit 1; }
DOCKER_BUILDKIT=1 docker build -t hookrail:e2e .
DOCKER_BUILDKIT=1 docker build -t hookrail-dashboard:e2e --target dashboard .
k3d image import hookrail:e2e hookrail-dashboard:e2e -c "$CLUSTER"
for img in rancher/kubectl:v1.33.1 busybox:1.36 postgres:16-alpine redis:7-alpine \
  otel/opentelemetry-collector-contrib:0.104.0 prom/prometheus:v2.53.0 jaegertracing/all-in-one:1.58 \
  ghcr.io/cloudnative-pg/cloudnative-pg:1.29.1 ghcr.io/cloudnative-pg/postgresql:16.10; do
  docker pull "$img"
done
k3d image import \
  rancher/kubectl:v1.33.1 busybox:1.36 postgres:16-alpine redis:7-alpine \
  otel/opentelemetry-collector-contrib:0.104.0 prom/prometheus:v2.53.0 jaegertracing/all-in-one:1.58 \
  ghcr.io/cloudnative-pg/cloudnative-pg:1.29.1 ghcr.io/cloudnative-pg/postgresql:16.10 \
  -c "$CLUSTER"
for i in $(seq 1 30); do kubectl version --request-timeout=2s >/dev/null 2>&1 && break; sleep 2; done
kubectl version --request-timeout=2s >/dev/null 2>&1 || { echo "API server never ready"; exit 1; }
# CNPG operator (vendored pin) BEFORE the overlay (Cluster CR needs the CRD).
kubectl apply --server-side -f deploy/k8s/cnpg/operator-1.29.1.yaml
kubectl wait --for=condition=established --timeout=120s crd/clusters.postgresql.cnpg.io
kubectl -n cnpg-system rollout status deploy/cnpg-controller-manager --timeout=240s
kubectl apply -k deploy/k8s/overlays/ephemeral
kubectl -n "$NS" wait --for=condition=Ready --timeout=600s cluster/hookrail-pg
for i in $(seq 1 60); do
  RI=$(kubectl -n "$NS" get cluster hookrail-pg -o jsonpath='{.status.readyInstances}' 2>/dev/null || echo 0)
  [ "$RI" = "3" ] && break; echo "waiting 3 ready CNPG instances (have ${RI:-0}) $i/60"; sleep 5
done
[ "$(kubectl -n "$NS" get cluster hookrail-pg -o jsonpath='{.status.readyInstances}')" = "3" ] || { echo "FAIL: CNPG cluster not 3/3"; exit 1; }
kubectl -n "$NS" wait --for=condition=complete --timeout=300s job/migrate
kubectl -n "$NS" wait --for=condition=complete --timeout=120s job/db-bootstrap
kubectl -n "$NS" wait --for=condition=complete --timeout=120s job/dashboard-keygen
KEYLOG=$(kubectl -n "$NS" logs job/dashboard-keygen)
KEY=$(echo "$KEYLOG" | /usr/bin/sed -n 's/^producer_key=//p')
[ -n "$KEY" ] || { echo "no producer key from keygen job"; exit 1; }
for d in api worker scheduler admin; do kubectl -n "$NS" rollout status deploy/$d --timeout=120s; done
# Run the failover oracle. KUBECONFIG is inherited; pass the producer key + the
# break-glass admin token (matches dev-secret) + the CNPG owner password.
export HOOKRAIL_PRODUCER_KEY="$KEY"
export HOOKRAIL_ADMIN_TOKEN="dev-admin-token-001"
export HOOKRAIL_OWNER_PASSWORD="hookrail"
go test -tags k8schaos ./test/chaos/k8s -run TestExperimentPGFailover -count=1 -timeout 25m -v
echo "pg-failover-e2e OK"
