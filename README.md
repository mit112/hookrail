# Hookrail

**Self-hostable webhook delivery service.** Producers POST events; Hookrail
guarantees at-least-once delivery to subscribed HTTP endpoints with retries,
exponential backoff (full jitter + Retry-After), idempotency, HMAC signing,
dead-letter queues, per-endpoint rate limiting, and full observability.

> Status: P0. Local demo + measured baseline. Dashboard, Python SDK, and the
> public demo deployment land in P1 (see roadmap).

## Architecture

```
 producers ──POST──► Ingress API ──one PG txn: event + deliveries──► 202
                          │ best-effort XADD
                          ▼
   PostgreSQL (SOURCE OF TRUTH)        Redis Streams (lossy hot path)
   deliveries = durable obligations    delivery_ids only · consumer group
   state machine · leases · attempts   PEL + XAUTOCLAIM
          ▲         ▲                        │
          │     PG sweeper (30s) ────────────┘ republish due/stuck (dups safe)
          │
   Delivery workers: CAS-claim → SSRF-guarded signed POST → classify
   → attempt + transition in ONE txn → XACK
```

**The core argument:** cross-store atomicity between Redis and Postgres is
impossible, so Postgres owns every delivery obligation and Redis is just a
lossy accelerator. Losing Redis *delays* deliveries (next sweep republishes);
it never loses them. Workers claim via compare-and-swap with lease takeover,
so the two overlapping recovery layers (Redis PEL seconds-scale, PG sweeper
30s-scale) can both fire safely: duplicates possible, loss impossible.

## Honest semantics

At-least-once, not exactly-once — exactly-once over HTTP to arbitrary
endpoints is impossible. Consumers dedup on the `hookrail-delivery-id` header.
No FIFO ordering guarantee in P0 (documented; opt-in ordered keys is on the
roadmap). Single-node Postgres: zero-loss claims hold while PG storage is
intact (failure scope enumerated in the design doc).

## Quickstart (60 seconds)

```bash
git clone https://github.com/mit112/hookrail && cd hookrail
cp .env.example .env
make up && make seed     # prints your producer key + endpoint secret
curl -X POST localhost:8080/v1/events \
  -H "Authorization: Bearer <producer key>" -H "Content-Type: application/json" \
  -d '{"topic":"demo.hello","payload":{"msg":"hi"}}'
# watch it arrive: curl "localhost:9090/received?delivery_id=<id>"
# Grafana localhost:3000 · Jaeger localhost:16686 · Prometheus localhost:9091
```

## Security

Hookrail POSTs to user-supplied URLs — it is an SSRF machine without
defenses, so they're P0: HTTPS required in hosted mode; DNS resolved once,
all answers validated against blocked ranges (loopback, RFC1918, link-local
incl. cloud metadata, CGNAT, ULA), then the **validated IP is dialed
directly** (no re-resolution → no rebinding); redirects never followed;
response reads capped at 64KB; split timeout budget. Producer keys stored
hashed; endpoint secrets AES-256-GCM at rest. Signatures:
`hookrail-signature: t=<unix>,v1=HMAC_SHA256(secret, t.delivery_id.body)`,
±5min tolerance, dual-secret rotation.

## Measured baseline

See [docs/baseline/](docs/baseline/) — k6 protocol, hardware, and the
numbers table (published as measured, whatever they are).

## Development

`make test` (unit) · `make itest` (integration, needs Docker) ·
`make e2e` / `scripts/e2e.sh` (full stack) · `make lint`.
Conventional commits; merges require green CI.

License: Apache-2.0

## Admin & retention (P1 Slice A)

An internal admin surface (`hookrail-admin`, not exposed to producers) provides
CRUD, query, and DLQ replay. A retention janitor runs inside
`hookrail-scheduler`; the CLI exposes a one-shot variant.

### Auth & deployment

`hookrail-admin` listens on `:8082` and requires `HOOKRAIL_ADMIN_TOKEN` (a
bearer secret, refuses to boot without it). In production it sits behind a k3s
NetworkPolicy — defense-in-depth, not a replacement for one. Every `/v1/*`
route checks the bearer token; ops routes (`/healthz`, `/readyz`, `/metrics`)
are exempt.

### Endpoints

| Method | Path | Description |
|--------|------|-------------|
| POST | `/v1/endpoints` | Create endpoint (SSRF-validated) |
| GET | `/v1/endpoints` | List endpoints (keyset-paginated) |
| GET | `/v1/endpoints/{id}` | Get endpoint |
| PATCH | `/v1/endpoints/{id}` | Partial update |
| DELETE | `/v1/endpoints/{id}` | Soft-delete (cascades subscriptions) |
| POST | `/v1/endpoints/{id}/rotate-secret` | Rotate HMAC secret (one-time plaintext return) |
| POST | `/v1/subscriptions` | Create subscription |
| GET | `/v1/subscriptions` | List subscriptions (optionally filtered by `endpoint_id`) |
| GET | `/v1/subscriptions/{id}` | Get subscription |
| PATCH | `/v1/subscriptions/{id}` | Partial update |
| DELETE | `/v1/subscriptions/{id}` | Soft-delete (immutable after) |
| GET | `/v1/deliveries` | Browse deliveries (state/endpoint/topic/event filters) |
| GET | `/v1/deliveries/{id}` | Delivery timeline with attempt list |
| GET | `/v1/dlq` | Browse dead letters (endpoint-scoped) |
| POST | `/v1/dlq/{delivery_id}/replay` | Replay dead letter (atomic CAS, replay-safe with tombstone checks) |

All admin operations target `http://localhost:8082` (internal port) per the
OpenAPI contract. Error responses use RFC 7807 `application/problem+json`.

### Retention janitor

A background goroutine in `hookrail-scheduler` cycles through four passes:

1. **Purge idempotency keys** — delete keys past their TTL.
2. **Tombstone event payloads** — zero out payload bytes for events older than
   `RETENTION_EVENT_PAYLOAD_DAYS`, skipping any with unreplayed dead letters.
3. **Trim delivery attempts** — remove attempt rows older than
   `RETENTION_ATTEMPT_DAYS` and stamp `attempts_truncated` on the delivery.
4. **Purge idempotency keys again** — catch keys created during the tick.

| Env | Default | Description |
|-----|---------|-------------|
| `RETENTION_EVENT_PAYLOAD_DAYS` | `30` | Age at which event payloads are tombstoned |
| `RETENTION_ATTEMPT_DAYS` | `30` | Age at which attempt rows are trimmed |
| `RETENTION_INTERVAL` | `1h` | Janitor loop interval |
| `RETENTION_BATCH` | `500` | Batch size per pass |
| `RETENTION_TICK_BUDGET` | `30s` | Max wall-clock time per tick |
| `RETENTION_ENABLED` | `true` | Set to `false` to disable the janitor |

One-shot from the CLI:

```bash
hookrail-ctl retention --once
```

Exits non-zero if any pass fails (advisory-locked — only one janitor runs at a
time).

### Limits & honesty

These are documented residual risks (design §9); do not treat them as bugs.

- **`rate_limit_rps` is per-worker best-effort.** Each worker independently
  loads the MIN per-endpoint `rate_limit_rps` from every subscription and
  applies it to its own rate limiter. This is NOT a deployment-wide global
  cap — true global rate limiting is P2. If you have N workers, the effective
  rate can be up to N × `rate_limit_rps`. The burst is floored at 1, so even
  sub-0.5 rps values eventually allow a delivery.

- **Secret rotation & URL cutover is eventual.** After `rotate-secret`, the
  old secret remains valid until every in-flight HTTP attempt completes or
  times out (bounded by the worker attempt timeout). The new secret is returned
  once in the API response and is never stored in plaintext thereafter.

- **Pagination is best-effort.** Keyset pagination uses ULIDs, which are not
  strictly monotonic within the same millisecond. Items inserted at the same
  clock tick may occasionally be skipped or duplicated across pages.
  Pagination is suitable for 95th-percentile browsing, not for strict
  enumeration.

- **Ingest ↔ delete reconciliation is eventual.** An ingested event is
  delivered to the subscriptions that match at claim time. If a subscription is
  deleted shortly before or after the event arrives, the delivery may still be
  attempted (the worker's claim query excludes soft-deleted targets, but the
  delivery record was already created). Late attempts within a single ingest
  batch are possible, though the overall system converges to correct exclusion.

## Dashboard (P1 Slice C)

A browser-based admin dashboard (React/TypeScript SPA) served by a Go
backends-for-frontends (BFF) that holds the admin token server-side so it never
reaches the browser. The BFF authenticates humans with a shared password,
issues an HMAC-signed cookie, then proxies an allowlist of admin routes and one
native test-event route.

### Quickstart

```bash
make web-build
docker compose -f deploy/compose/docker-compose.yml up -d --build dashboard
# Open http://localhost:8085, log in with password from dev config
```

Or in one command (builds the SPA, starts the full stack, and opens the
dashboard):

```bash
make web-build && docker compose -f deploy/compose/docker-compose.yml up -d --build dashboard && echo "http://localhost:8085"
```

### Environment variables

| Env | Default | Description |
|-----|---------|-------------|
| `HOOKRAIL_DASHBOARD_PASSWORD` | — | Shared password for human login (required) |
| `HOOKRAIL_DASHBOARD_SESSION_KEY` | — | HMAC signing key, 32 hex-encoded bytes (required) |
| `HOOKRAIL_DASHBOARD_SESSION_KEY_PREV` | — | Previous signing key for zero-downtime rotation |
| `HOOKRAIL_ADMIN_TOKEN` | — | Admin API bearer token for proxied admin calls (required) |
| `HOOKRAIL_PRODUCER_KEY_FILE` | — | Path to file containing the producer key (plaintext, written by the compose provisioner) |
| `HOOKRAIL_ADMIN_URL` | `http://admin:8082` | Internal admin API base URL |
| `HOOKRAIL_INGRESS_URL` | `http://api:8080` | Internal ingress API base URL |
| `HOOKRAIL_DASHBOARD_ADDR` | `:8085` | Listen address |
| `HOOKRAIL_DASHBOARD_SESSION_TTL` | `24h` | Session cookie TTL (Go duration) |
| `HOOKRAIL_DASHBOARD_INSECURE_COOKIE` | `false` | Allow cookies over plain HTTP (dev only) |

### Limits & honesty

These are documented residual risks (design §8); do not treat them as bugs.

- **Single shared password, no RBAC.** All dashboard users share one password.
  There is no role-based access control — every authenticated user has full
  read/write access to all admin operations. Multi-user auth and scoped
  permissions are deferred to a future slice.

- **Stateless cookie, no revocation.** The session cookie is a self-contained
  HMAC-signed token with a TTL. There is no server-side session store, so a
  compromised cookie cannot be revoked before its TTL expires. Logout clears
  the cookie client-side but the token remains valid until it expires.

- **`next_cursor` is forgeable.** Keyset pagination cursors are unsigned. A
  user can tamper with `next_cursor` to probe arbitrary ULID ranges. This
  reveals only the same data the user can already see through the UI (no
  privilege escalation), but it means cursor integrity is not guaranteed.

- **TLS / public exposure is Slice E.** The dashboard is designed for local or
  VPN-only deployment behind a TLS-terminating reverse proxy. It does not
  terminate TLS itself, and `HOOKRAIL_DASHBOARD_INSECURE_COOKIE` is `true` in
  dev. Production TLS, certificate management, and WAF integration are out of
  scope for this slice.

- **Admin-token blast radius.** The BFF holds the full `HOOKRAIL_ADMIN_TOKEN`
  in process memory. A compromise of the BFF process leaks full admin API
  access to the underlying admin surface. Defense-in-depth (network policy,
  read-only proxy routes) limits but does not eliminate this risk.

- **Test-event key exposure.** The test-event feature injects a provisioned
  producer key into the BFF's upstream calls. This key can create events on the
  ingress. It is scoped per the provisioner's `create-producer-key` naming and
  can be rotated independently, but a BFF compromise leaks the ability to
  produce test events.

## Deploy to k3s (Slice E)

This runbook deploys Hookrail to a single-node k3s cluster behind a Cloudflare
Tunnel, exposing the Ingest API and Dashboard on public hostnames. The admin
API is **not** exposed outside the cluster (defense-in-depth via NetworkPolicy).

**Prerequisites:** k3s installed on the target host, `kubectl` v1.33+,
`kustomize` (built into kubectl v1.33), Docker (for building/pushing images),
`hookrail-ctl` binary or the Hookrail repo cloned, a Cloudflare account with a
tunnel created (token available), and two DNS hostnames pointing to the tunnel
(one for ingest, one for dashboard).

### 1. Create the namespace

```bash
kubectl create namespace hookrail
```

### 2. Create Secrets (attended — never committed)

Generate a 64-hex-char master key:

```bash
openssl rand -hex 32
```

Generate a session key (≥32 hex bytes):

```bash
openssl rand -hex 32
```

Choose strong passwords for the Postgres owner and the app role, and a
dashboard shared password (≥16 chars each). The admin token must be ≥16 chars.

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

### 3. Generate the dashboard producer key

```bash
hookrail-ctl create-producer-key -name dashboard
# prints: producer_key=hk_...
```

Copy the generated key (the `hk_...` value after `=`):

```bash
kubectl -n hookrail create secret generic hookrail-dashboard-producer-key \
  --from-literal=producer_key='hk_...'
```

### 4. Configure the Cloudflare Tunnel hostnames

Edit `deploy/k8s/overlays/prod/cloudflared.yaml` and replace the two
placeholders:

- `INGEST_HOSTNAME_PLACEHOLDER` → your ingest hostname (e.g. `ingest.example.com`)
- `DASHBOARD_HOSTNAME_PLACEHOLDER` → your dashboard hostname (e.g. `dashboard.example.com`)

### 5. Pin container images

Use the exact digest from GHCR (prevents accidental roll-forward):

```bash
kubectl kustomize edit set image \
  hookrail=ghcr.io/mit112/hookrail@sha256:<digest> \
  hookrail-dashboard=ghcr.io/mit112/hookrail-dashboard@sha256:<digest> \
  -k deploy/k8s/overlays/prod
```

Replace `<digest>` with the `sha256:...` from the latest `:main` tag on GHCR.

### 6. Delete any stale migrate Job

```bash
kubectl -n hookrail delete job migrate --ignore-not-found
```

This is safe — the new apply will recreate it.

### 7. Apply the prod overlay

```bash
kubectl apply -k deploy/k8s/overlays/prod
```

### 8. Wait for the migrate Job

```bash
kubectl -n hookrail wait --for=condition=complete --timeout=180s job/migrate
```

### 9. Enable the app role login

The `0004_app_role` migration creates `hookrail_app` as `NOLOGIN` (no
password). Connect as the owner and grant login with the password you chose in
step 2:

```bash
kubectl -n hookrail exec -it statefulset/postgres -- \
  psql -U hookrail -d hookrail \
  -c "ALTER ROLE hookrail_app LOGIN PASSWORD '<app-pw>'"
```

Use the same `<app-pw>` you set in `app_dsn` and `app_pw`.

### 10. Wait for all deployments

```bash
for d in api worker scheduler admin dashboard; do
  kubectl -n hookrail rollout status deploy/$d --timeout=120s
done
```

### 11. Post-cutover smoke

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

**Admin is NOT reachable from outside** — the admin Service is `ClusterIP`
only and the NetworkPolicy only allows dashboard→admin. From a machine outside
the cluster, the admin port must be unreachable:

```bash
curl -s --connect-timeout 5 http://<k3s-node-ip>:8082/healthz || echo "blocked (expected)"
```

### 12. Residual risks (Slice E — before future hardening)

- **Single-node.** The full stack (Postgres, Redis, all 5 app Deployments, OTel,
  Prometheus, Jaeger, cloudflared) runs on one k3s node. Node failure takes
  everything down. Multi-node and HA are deferred to a future slice.
- **Cloudflare Tunnel dependency.** Public access depends on the Cloudflare
  edge and the `cloudflared` sidecar. A Cloudflare outage or tunnel disruption
  makes the service unreachable from the internet. Direct ingress (e.g.
  node-local reverse proxy with TLS termination) is deferred.
- **Plain Kubernetes Secrets.** Secrets are stored as base64-encoded values in
  `etcd`. There is no encryption-at-rest wrapper (SealedSecrets, SOPS, Vault).
  Anyone with `kubectl` access to the `hookrail` namespace can read every
  secret in plaintext.
- **No producer-key hot-reload.** The dashboard reads its producer key from a
  mounted Secret at startup. Rotating the key requires editing the Secret and
  restarting the dashboard Deployment (kubectl rollout restart). Zero-downtime
  key rotation is deferred.
- **Unbounded observability retention.** Prometheus and Jaeger use emptyDir
  storage (no PVC). Data is lost on pod restart. Persistent, size-capped
  observability storage is deferred.
- **No PodSecurity / backup.** The namespace has no PodSecurity admission
  (restricted) and there is no automated Postgres backup. Both are deferred to
  a future slice.

