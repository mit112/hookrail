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

## 1b. Install the CloudNativePG operator (Datastore HA)

Postgres runs as a 3-instance CloudNativePG (CNPG) cluster (`Cluster/hookrail-pg`)
with synchronous quorum replication and automated failover. The operator install
manifest is vendored + pinned at `deploy/k8s/cnpg/operator-1.29.1.yaml` (see
`deploy/k8s/cnpg/README.md`). Install it **before** applying the overlay — the
`Cluster` CR needs the CRD to exist:

```bash
kubectl apply --server-side -f deploy/k8s/cnpg/operator-1.29.1.yaml
kubectl wait --for=condition=established --timeout=120s crd/clusters.postgresql.cnpg.io
kubectl -n cnpg-system rollout status deploy/cnpg-controller-manager --timeout=240s
```

> **Single-node honesty:** on a single Mac-mini k3s node this delivers
> **process-level** failover (survives a Postgres pod/process loss + automated
> standby promotion with zero RPO for accepted events). It does **not** survive
> node/disk loss until a second physical node is added — the manifests are
> correct for multi-node, but live fault-tolerance is bounded by the single node.

## 1c. Redis HA (Sentinel)

Redis runs as a **master + replica StatefulSet** (`redis-0`, `redis-1`) fronted by a
**3-node Sentinel quorum** (`redis-sentinel-0..2`), replacing the former single-node
Redis SPOF. On a master-pod loss, Sentinel automatically promotes the surviving
replica (RTO ~3–9s); the app's Redis clients are go-redis `FailoverClient`s that
discover the current master through the quorum, so no app restart is needed. These
ship in the base overlay — no extra install step beyond the overlay apply.

Configuration is driven by two ConfigMap keys (already set in
`deploy/k8s/base/configmap.yaml`); app pods run in Sentinel mode and `REDIS_ADDR` is
absent:

```yaml
REDIS_SENTINEL_ADDRS: "redis-sentinel-0.redis-sentinel:26379,redis-sentinel-1.redis-sentinel:26379,redis-sentinel-2.redis-sentinel:26379"
REDIS_MASTER_NAME: "hookrail"
```

Each redis pod runs a Sentinel-aware **role-selection entrypoint**: it asks the
Sentinel quorum (≥2/3 agreement) for the current master, falls back to a live-peer
`ROLE` probe, and only seeds `redis-0` as the genesis master on a **provably-fresh
PVC**. A restarted pod always rejoins the *current* master and never re-seizes
mastership after a real failover. Sentinels persist their `sentinel.conf` on a PVC so
a restarted Sentinel does not forget the current master.

> **Durability honesty (RPO):** application **RPO=0 is a Postgres + sweeper property,
> not a Redis property.** Redis carries `delivery_id` only; the stream is a hot-path
> hint. Sentinel replication is asynchronous, so a promotion may drop recently-written
> stream/consumer-group state — that loss is re-driven from Postgres by the sweeper
> (at-least-once), never a lost delivery. We therefore do **not** set
> `min-replicas-to-write` (it would make a freshly-promoted master reject writes until
> the killed pod rejoins, defeating availability for no durability gain).
>
> **Single-node honesty:** as with Postgres, on one k3s node this is process-level
> failover only; node/disk-loss HA needs a second physical node. The live Mac-mini
> cutover is always **attended** — never run by CI.

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
  --from-literal=app_dsn='postgres://hookrail_app:<app-pw>@hookrail-pg-rw:5432/hookrail?sslmode=disable' \
  --from-literal=app_pw='<app-pw>' \
  --from-literal=migrator_dsn='postgres://hookrail:<pg-owner-pw>@hookrail-pg-rw:5432/hookrail?sslmode=disable'

# CNPG owner credentials (basic-auth) — the operator sets the `hookrail` owner
# role's password from this at cluster bootstrap. Username MUST be `hookrail`;
# password MUST equal <pg-owner-pw> above so migrator_dsn authenticates.
kubectl -n hookrail create secret generic hookrail-db-owner \
  --type=kubernetes.io/basic-auth \
  --from-literal=username='hookrail' \
  --from-literal=password='<pg-owner-pw>'

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

> The dashboard producer key is generated **after** migrations apply (see step 8):
> `create-producer-key` writes the `producer_key_scopes` row created by migration
> 0010 in the same transaction, so it must run once the migrate Job has completed.

## 3. Configure the Cloudflare Tunnel hostnames

Edit `deploy/k8s/overlays/prod/cloudflared.yaml` and replace the two
placeholders:

- `INGEST_HOSTNAME_PLACEHOLDER` → your ingest hostname (e.g. `ingest.example.com`)
- `DASHBOARD_HOSTNAME_PLACEHOLDER` → your dashboard hostname (e.g. `dashboard.example.com`)

## 4. Pin container images

Use the exact digest from GHCR (prevents accidental roll-forward):

```bash
# `kustomize edit` requires the standalone kustomize binary and operates on the
# kustomization in the current directory (kubectl kustomize only renders).
( cd deploy/k8s/overlays/prod && kustomize edit set image \
    hookrail=ghcr.io/mit112/hookrail@sha256:<digest> \
    hookrail-dashboard=ghcr.io/mit112/hookrail-dashboard@sha256:<digest> )
```

Replace `<digest>` with the `sha256:...` from the latest `:main` tag on GHCR.

## 5. Delete any stale migrate Job

```bash
kubectl -n hookrail delete job migrate --ignore-not-found
```

This is safe — the new apply will recreate it.

## 6. Apply the prod overlay

```bash
kubectl apply -k deploy/k8s/overlays/prod
```

## 7. Wait for the CNPG cluster, then the migrate Job

Wait for the 3-instance cluster to be Ready (the migrate Job's init container
also blocks on `hookrail-pg-rw`, but waiting here surfaces bootstrap problems):

```bash
kubectl -n hookrail wait --for=condition=Ready --timeout=600s cluster/hookrail-pg
kubectl -n hookrail wait --for=condition=complete --timeout=300s job/migrate
```

## 8. Generate the dashboard producer key

Run this **after** the migrate Job completes (step 7): migration 0010 creates the
`producer_key_scopes` table, and `create-producer-key` writes the key's scope row
in the same transaction. The dashboard publishes user-chosen topics via its test
event, so it is scoped to all topics (`-scope '*'`).

```bash
hookrail-ctl create-producer-key -name dashboard -scope '*'
# prints: producer_key=hk_...  key_id=...  scopes=*
```

Copy the generated key (the `hk_...` value after `=`):

```bash
kubectl -n hookrail create secret generic hookrail-dashboard-producer-key \
  --from-literal=producer_key='hk_...'
```

## 9. Enable the app role login

The `0004_app_role` migration creates `hookrail_app` as `NOLOGIN` (no password).
The CNPG owner `hookrail` is granted `CREATEROLE` at bootstrap (so it can ALTER
the role). Connect to the current primary and grant login with the password you
chose in step 2:

```bash
PRIMARY=$(kubectl -n hookrail get pod \
  -l cnpg.io/cluster=hookrail-pg,cnpg.io/instanceRole=primary \
  -o jsonpath='{.items[0].metadata.name}')
kubectl -n hookrail exec -it "$PRIMARY" -c postgres -- \
  psql -U hookrail -d hookrail -v pw="<app-pw>" \
  <<'SQL'
ALTER ROLE hookrail_app LOGIN PASSWORD :'pw';
SQL
```

Use the same `<app-pw>` you set in `app_dsn` and `app_pw`. (`:'pw'` is psql's
safe variable quoting — it works on stdin, not in a `-c` string.)

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

- **App-tier HA + Postgres HA, but bounded by a single node.** The relay app tier
  (api, worker, scheduler, admin, dashboard) runs at 2 replicas with scheduler
  leader election and worker graceful drain, and **Postgres now runs as a CNPG
  3-instance synchronous-quorum cluster with automated failover** (zero RPO for
  accepted events). On a *single* k3s node this is **process-level** failover
  only — surviving a Postgres pod/process loss + promotion — but **not node/disk
  loss**, since all three CNPG instances and the app pods share the one node.
  **Redis now runs HA too** — a master+replica StatefulSet behind a 3-node Sentinel
  quorum with automatic promotion (§1c). OTel, Prometheus, and Jaeger each still run
  as a single pod. **Multi-node k3s** is deferred; add a second node before claiming
  hardware-failure tolerance (every HA tier above is process-level on one node).
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
