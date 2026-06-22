# Deploy Hookrail to k3s

This runbook deploys Hookrail to a single-node k3s cluster behind a Cloudflare
Tunnel, exposing the Ingest API and Dashboard on public hostnames. The admin API
is **not** exposed outside the cluster (defense-in-depth via NetworkPolicy; the
admin Service is `ClusterIP` only).

It is an **attended** procedure: you hold the secrets, you run each step, and the
cutover is deliberate. Nothing here is automated.

**Prerequisites:**

- k3s installed on the target host.
- `kubectl` v1.33+.
- The standalone `kustomize` binary (needed for `kustomize edit`; `kubectl
  kustomize` only renders).
- Docker (for building/pushing images).
- `hookrail-ctl` binary, or the Hookrail repo cloned.
- A Cloudflare account with a tunnel created (token available).
- Two DNS hostnames pointing to the tunnel (one for ingest, one for dashboard).

## 1. Create the namespace

```bash
kubectl create namespace hookrail
```

## 2. Create Secrets (attended — never committed)

Generate a 64-hex-char master key:

```bash
openssl rand -hex 32
```

Generate a session key (≥32 hex bytes):

```bash
openssl rand -hex 32
```

Choose strong passwords for the Postgres owner and the app role, and a dashboard
shared password (≥16 chars each). The admin token must be ≥16 chars.

```bash
# Postgres database secrets
kubectl -n hookrail create secret generic hookrail-db \
  --from-literal=owner_password='<pg-owner-pw>' \
  --from-literal=app_dsn='postgres://hookrail_app:<app-pw>@postgres:5432/hookrail?sslmode=disable' \
  --from-literal=app_pw='<app-pw>' \
  --from-literal=migrator_dsn='postgres://hookrail:<pg-owner-pw>@postgres:5432/hookrail?sslmode=disable'

# Master encryption key (64 hex chars)
kubectl -n hookrail create secret generic hookrail-app \
  --from-literal=master_key='<64-hex-chars>'

# Admin bearer token (≥16 chars)
kubectl -n hookrail create secret generic hookrail-admin \
  --from-literal=token='<admin-token>'

# Dashboard credentials
kubectl -n hookrail create secret generic hookrail-dashboard \
  --from-literal=password='<dashboard-pw>' \
  --from-literal=session_key='<64-hex-chars>'

# Cloudflare tunnel token
kubectl -n hookrail create secret generic cloudflared-token \
  --from-literal=token='<cloudflare-tunnel-token>'
```

## 3. Generate the dashboard producer key

```bash
hookrail-ctl create-producer-key -name dashboard
# prints: producer_key=hk_...
```

Copy the generated key (the `hk_...` value after `=`):

```bash
kubectl -n hookrail create secret generic hookrail-dashboard-producer-key \
  --from-literal=producer_key='hk_...'
```

## 4. Configure the Cloudflare Tunnel hostnames

Edit `deploy/k8s/overlays/prod/cloudflared.yaml` and replace the two
placeholders:

- `INGEST_HOSTNAME_PLACEHOLDER` → your ingest hostname (e.g. `ingest.example.com`)
- `DASHBOARD_HOSTNAME_PLACEHOLDER` → your dashboard hostname (e.g. `dashboard.example.com`)

## 5. Pin container images

Use the exact digest from GHCR (prevents accidental roll-forward):

```bash
# `kustomize edit` requires the standalone kustomize binary and operates on the
# kustomization in the current directory (kubectl kustomize only renders).
( cd deploy/k8s/overlays/prod && kustomize edit set image \
    hookrail=ghcr.io/mit112/hookrail@sha256:<digest> \
    hookrail-dashboard=ghcr.io/mit112/hookrail-dashboard@sha256:<digest> )
```

Replace `<digest>` with the `sha256:...` from the latest `:main` tag on GHCR.

## 6. Delete any stale migrate Job

```bash
kubectl -n hookrail delete job migrate --ignore-not-found
```

This is safe — the new apply will recreate it.

## 7. Apply the prod overlay

```bash
kubectl apply -k deploy/k8s/overlays/prod
```

## 8. Wait for the migrate Job

```bash
kubectl -n hookrail wait --for=condition=complete --timeout=180s job/migrate
```

## 9. Enable the app role login

The `0004_app_role` migration creates `hookrail_app` as `NOLOGIN` (no password).
Connect as the owner and grant login with the password you chose in step 2:

```bash
kubectl -n hookrail exec -it statefulset/postgres -- \
  psql -U hookrail -d hookrail \
  -c "ALTER ROLE hookrail_app LOGIN PASSWORD '<app-pw>'"
```

Use the same `<app-pw>` you set in `app_dsn` and `app_pw`.

## 10. Wait for all deployments

```bash
for d in api worker scheduler admin dashboard; do
  kubectl -n hookrail rollout status deploy/$d --timeout=120s
done
```

## 11. Post-cutover smoke

**Ingest endpoint** (public hostname, returns 202 on success):

```bash
curl -s -o /dev/null -w "%{http_code}" \
  -X POST https://INGEST_HOSTNAME/v1/events \
  -H "Authorization: Bearer <dashboard-producer-key>" \
  -H "Content-Type: application/json" \
  -d '{"topic":"smoke.test","payload":{"msg":"hi"}}'
# Expected: 202
```

**Dashboard** (public hostname, login page returns 200):

```bash
curl -s -o /dev/null -w "%{http_code}" https://DASHBOARD_HOSTNAME/
# Expected: 200
```

**Admin is NOT reachable from outside** — the admin Service is `ClusterIP` only
and the NetworkPolicy only allows dashboard→admin. From a machine outside the
cluster, the admin port must be unreachable:

```bash
curl -s --connect-timeout 5 http://<k3s-node-ip>:8082/healthz || echo "blocked (expected)"
```

## 12. Residual risks (before future hardening)

- **App-tier HA, data-tier single-node.** The relay app tier (api, worker,
  scheduler, admin, dashboard) now runs at 2 replicas with scheduler leader
  election and worker graceful drain — see the README HA section. However,
  Postgres, Redis, OTel, Prometheus, and Jaeger each run as a single pod
  (`cloudflared` is bumped to 2 replicas in the prod overlay, but its live
  cutover stays attended). Node failure still takes the data tier down.
  Multi-node k3s and datastore HA are deferred to a future slice.
- **Cloudflare Tunnel dependency.** Public access depends on the Cloudflare edge
  and the `cloudflared` Deployment (the tunnel runs as its own standalone
  Deployment, not a sidecar). A Cloudflare outage or tunnel disruption makes
  the service unreachable from the internet. Direct ingress (e.g. node-local
  reverse proxy with TLS termination) is deferred. Note this means public TLS is
  terminated at the Cloudflare edge — *direct* in-cluster TLS / WAF is the part
  still deferred.
- **Plain Kubernetes Secrets.** Secrets are stored as base64-encoded values in
  `etcd`. There is no encryption-at-rest wrapper (SealedSecrets, SOPS, Vault).
  Anyone with `kubectl` access to the `hookrail` namespace can read every secret
  in plaintext.
- **No producer-key hot-reload.** The dashboard reads its producer key from a
  mounted Secret at startup. Rotating the key requires editing the Secret and
  restarting the dashboard Deployment (`kubectl rollout restart`). Zero-downtime
  key rotation is deferred.
- **Unbounded observability retention.** Prometheus and Jaeger use emptyDir
  storage (no PVC). Data is lost on pod restart. Persistent, size-capped
  observability storage is deferred.
- **PgBouncer / transaction-pooling incompatible.** The scheduler leader
  election holds a Postgres **session**-scoped advisory lock on a standalone
  `pgx.Conn`. PgBouncer in transaction-pooling mode assigns a different backend
  on each statement, breaking session-lock semantics. Use **direct PG
  connections or session-pooling only** — never transaction-pooling.
- **Rate limiting.** Endpoints with an explicit `rate_limit_rps` override are
  enforced **globally** across worker replicas via a shared Redis token bucket
  (on by default when Redis is configured; `HOOKRAIL_GLOBAL_RATELIMIT=0`
  disables), re-applied within one successful limits refresh. Endpoints without an
  override use each replica's local limiter, whose per-endpoint rate can reach
  N × `rate_limit_rps` with N replicas. The global path is cap-relaxing under
  failure (fail-open to the local bucket; Redis state loss rebuilds full buckets).
- **No PodSecurity / backup.** The namespace has no PodSecurity admission
  (restricted) and there is no automated Postgres backup. Both are deferred to a
  future slice.
