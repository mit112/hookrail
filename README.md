# Hookrail

[![CI](https://github.com/mit112/hookrail/actions/workflows/ci.yml/badge.svg)](https://github.com/mit112/hookrail/actions/workflows/ci.yml)
[![PyPI](https://img.shields.io/pypi/v/hookrail)](https://pypi.org/project/hookrail/)
[![License: Apache-2.0](https://img.shields.io/badge/license-Apache--2.0-blue)](LICENSE)

**Self-hostable webhook delivery service.** Producers POST events; Hookrail
guarantees at-least-once delivery to subscribed HTTP endpoints with retries,
exponential backoff (full jitter + Retry-After), idempotency, HMAC signing,
dead-letter queues, per-endpoint rate limiting, and full observability.

> **Status:** P0 core + P1 shipped — backend admin surface, Python SDK (on PyPI),
> admin dashboard, and a single-node k3s deploy. The chaos + curated-Grafana suite
> (Slice D) is in progress. The service is pre-1.0; no compatibility guarantees yet.

## Architecture

```text
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

Alongside the data path: **`hookrail-admin`** (`:8082`) is an internal CRUD /
query / DLQ-replay surface (never exposed to producers), and the **dashboard
BFF** (`:8085`) serves a browser admin UI while holding the admin token
server-side.

**The core argument:** cross-store atomicity between Redis and Postgres is
impossible, so Postgres owns every delivery obligation and Redis is just a lossy
accelerator. Losing Redis *delays* deliveries (next sweep republishes); it never
loses them. Workers claim via compare-and-swap with lease takeover, so the two
overlapping recovery layers (Redis PEL seconds-scale, PG sweeper 30s-scale) can
both fire safely: duplicates possible, loss impossible.

## Honest semantics

At-least-once, not exactly-once — exactly-once over HTTP to arbitrary endpoints is
impossible. Consumers dedup on the `hookrail-delivery-id` header. No FIFO ordering
guarantee (documented; opt-in ordered keys is on the roadmap). Single-node
Postgres: zero-loss claims hold while PG storage is intact (failure scope
enumerated in the design doc).

## Quickstart (60 seconds)

```bash
git clone https://github.com/mit112/hookrail && cd hookrail
cp .env.example .env
make up && make seed     # prints your producer key + endpoint secret
curl -X POST localhost:8080/v1/events \
  -H "Authorization: Bearer <producer key>" -H "Content-Type: application/json" \
  -d '{"topic":"demo.hello","payload":{"msg":"hi"}}'
# watch it arrive: curl "localhost:9090/received?delivery_id=<id>"
```

Observability ships with the stack: Grafana at `:3000` (datasource provisioned;
curated dashboards land in Slice D) · Jaeger at `:16686` · Prometheus at `:9091`.
See [docs/observability.md](docs/observability.md).

## Producer SDK (Python)

A typed Python client is published on PyPI as
[`hookrail`](https://pypi.org/project/hookrail/) (0.1.0). It covers the public
producer surface — send events, check delivery status, verify webhook signatures.

```bash
pip install hookrail
```

```python
from hookrail import Hookrail

with Hookrail(api_key="hk_...", base_url="https://hooks.example.com") as client:
    accepted = client.send_event("orders.created", {"order_id": "o_1", "amount": 4200})
    print("event:", accepted.event_id, "replayed:", accepted.replayed)
```

Full reference (async client, retries, signature verification): see
[clients/python/README.md](clients/python/README.md).

## Dashboard

A browser-based admin dashboard (React/TypeScript SPA) served by a Go
backends-for-frontends (BFF) that holds the admin token server-side so it never
reaches the browser. Humans log in with a shared password and get an HMAC-signed
session cookie; the BFF proxies an allowlist of admin routes plus one test-event
route.

```bash
make web-build
docker compose -f deploy/compose/docker-compose.yml up -d --build dashboard
# Open http://localhost:8085
```

Environment variables, auth model, and run details: see
[docs/dashboard.md](docs/dashboard.md).

## Admin API & retention

`hookrail-admin` listens on `:8082` and requires `HOOKRAIL_ADMIN_TOKEN` (a bearer
secret; it refuses to boot without one). It is not exposed to producers — in
production it sits behind a k3s NetworkPolicy. Every `/v1/*` route checks the
bearer token; ops routes (`/healthz`, `/readyz`, `/metrics`) are exempt. Errors
use RFC 7807 `application/problem+json`.

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
| POST | `/v1/dlq/{delivery_id}/replay` | Replay dead letter (atomic CAS, tombstone-checked) |

A retention janitor runs inside `hookrail-scheduler` (one-shot variant:
`hookrail-ctl retention --once`). It purges idempotency keys, tombstones old event
payloads, and trims old delivery attempts, advisory-locked so only one runs at a
time.

| Env | Default | Description |
|-----|---------|-------------|
| `RETENTION_EVENT_PAYLOAD_DAYS` | `30` | Age (days) at which event payloads are tombstoned |
| `RETENTION_ATTEMPT_DAYS` | `30` | Age (days) at which attempt rows are trimmed |
| `RETENTION_INTERVAL` | `1h` | Janitor loop interval |
| `RETENTION_BATCH` | `5000` | Batch size per pass |
| `RETENTION_TICK_BUDGET` | `60s` | Max wall-clock time per tick |
| `RETENTION_IDEM_HOURS` | `24h` | TTL for idempotency keys before purge |
| `RETENTION_STREAM_MAXLEN` | `100000` | Approx max Redis stream length (trim cap) |
| `RETENTION_ENABLED` | `true` | Set to `false` to disable the janitor |

## Deploy to k3s

A single-node k3s deploy behind a Cloudflare Tunnel exposes the Ingest API and
Dashboard on public hostnames; the admin API stays `ClusterIP`-only (NetworkPolicy
allows only dashboard→admin). Public TLS is terminated at the Cloudflare edge.
Secrets are created attended — nothing is committed.

Full runbook (12 attended steps + residual risks):
[docs/deploy/k3s.md](docs/deploy/k3s.md).

## Observability

Prometheus (`:9091`) scrapes the api/worker/scheduler; Grafana (`:3000`) has a
provisioned datasource; the OTel Collector forwards traces to Jaeger (`:16686`).
Curated dashboards land in Slice D. Details:
[docs/observability.md](docs/observability.md).

## Security

Hookrail POSTs to user-supplied URLs — it is an SSRF machine without defenses, so
they're P0: HTTPS required in hosted mode; DNS resolved once, all answers validated
against blocked ranges (loopback, RFC1918, link-local incl. cloud metadata, CGNAT,
ULA), then the **validated IP is dialed directly** (no re-resolution → no
rebinding); redirects never followed; response reads capped at 64KB; split timeout
budget. Producer keys stored hashed; endpoint secrets AES-256-GCM at rest.
Signatures: `hookrail-signature: t=<unix>,v1=HMAC_SHA256(secret, t.delivery_id.body)`,
±5min tolerance, dual-secret rotation.

## Honest limitations & residual risks

These are documented design trade-offs, not bugs.

### Delivery semantics

- **`rate_limit_rps` is per-worker best-effort.** Each worker independently applies
  the MIN per-endpoint `rate_limit_rps` to its own limiter. With N workers the
  effective rate can be up to N × `rate_limit_rps`; true global rate limiting is
  P2. The burst is floored at 1, so even sub-0.5 rps values eventually deliver.
- **Secret rotation & URL cutover is eventual.** After `rotate-secret`, the old
  secret stays valid until every in-flight attempt completes or times out. The new
  secret is returned once and never stored in plaintext.
- **Pagination is best-effort.** Keyset cursors use ULIDs, not strictly monotonic
  within a millisecond, so same-tick items may occasionally be skipped or
  duplicated across pages. Suitable for browsing, not strict enumeration.
- **Ingest ↔ delete reconciliation is eventual.** A delivery created for a
  subscription deleted shortly before/after ingest may still be attempted; the
  system converges to correct exclusion.

### Admin & dashboard

- **Single shared password, no RBAC.** Every authenticated dashboard user has full
  read/write access. Multi-user auth and scoped permissions are deferred.
- **Stateless cookie, no revocation.** The session cookie is a self-contained
  HMAC-signed token with a TTL; a compromised cookie can't be revoked before it
  expires. Logout only clears it client-side.
- **`next_cursor` is forgeable.** Keyset cursors are unsigned and tamperable. This
  reveals only data the user can already see (no privilege escalation), but cursor
  integrity is not guaranteed.
- **Admin-token blast radius.** The BFF holds the full `HOOKRAIL_ADMIN_TOKEN` in
  memory; compromising the BFF leaks full admin API access. Network policy and
  read-only proxy routes limit but don't eliminate this.
- **Test-event key exposure.** The test-event feature injects a provisioned
  producer key into the BFF's upstream calls; a BFF compromise leaks the ability
  to produce test events.

### k3s deploy

Single-node (node failure takes everything down); Cloudflare Tunnel dependency for
public access; plain Kubernetes Secrets (base64 in etcd, no SealedSecrets/SOPS/Vault);
no producer-key hot-reload; unbounded observability retention (emptyDir); no
PodSecurity admission or automated Postgres backup. Public TLS exists via the
tunnel — *direct* in-cluster TLS / WAF is still deferred. Full list:
[docs/deploy/k3s.md](docs/deploy/k3s.md).

## Measured baseline

See [docs/baseline/](docs/baseline/) — k6 protocol, hardware, and the numbers table
(published as measured, whatever they are).

## Roadmap & versioning

- **P0** ✓ — durable ingest, delivery state machine, SSRF-guarded signed delivery,
  backoff, DLQ, measured baseline.
- **P1** — Slice A (admin surface) ✓ · Slice B (Python SDK, PyPI) ✓ · Slice C
  (dashboard) ✓ · Slice E (k3s deploy + ops-hardening) ✓ · Slice F (docs v2) ✓.
- **Next** — Slice D: chaos suite + curated Grafana dashboards (independent of the
  A→C→E→F critical path).

Versioning: the Python SDK follows SemVer (`hookrail` 0.1.0). The service itself is
pre-1.0 with no compatibility guarantees yet.

## Development

```bash
make test          # unit
make itest         # integration (needs Docker)
make e2e           # full-stack end-to-end
make lint          # golangci-lint
make py-verify     # Python SDK lint + typecheck + test
make web-verify    # dashboard SPA typecheck + lint + test + build
```

Conventional commits; merges require green CI.

## License

Apache-2.0 — see [LICENSE](LICENSE).
