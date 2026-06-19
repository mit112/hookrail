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

