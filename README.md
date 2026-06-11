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
