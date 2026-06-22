# Changelog

All notable changes documented here. Honest history — early entries include design corrections.

## [Unreleased]

## [P2]

Opt-in strict-FIFO per-key ordering with pause-on-dead-letter, skip, backlog cap, and chaos-proven zero-reorder oracle.

- **Ordered subscriptions:** `"ordered": true` at creation time (immutable). Producers supply an `ordering_key` via `X-Hookrail-Ordering-Key` header or body field.
- **Per-key serialization:** `ordered_key_state` cursor table with row-level locking; at most one in-flight per key.
- **Pause on dead-letter:** a dead-lettered head blocks the entire key until replay or skip.
- **`POST /v1/deliveries/{id}/skip`:** admin action to advance past a blocked head (sets `skipped` state).
- **`GET /v1/ordered-keys?blocked=true`:** list blocked keys with backlog and successor age.
- **Backlog cap:** `HOOKRAIL_ORDERED_KEY_BACKLOG_MAX` (default 10000) per key; 429 + `Retry-After` when exceeded.
- **Chaos-proven:** zero-reorder oracle (E5) under worker kill — 3× deterministic passes.
- **App-tier HA:** scheduler leader election via PG advisory lock; worker graceful drain with reserve-before-claim tracker; per-replica rate-limit disclosure; 2-replica manifests with PDBs, anti-affinity, and preStop hooks; chaos-proven leader-failover oracle (E6).
- **Global rate limiting:** endpoints with an explicit `rate_limit_rps` override are enforced across all worker replicas via a shared Redis token bucket (atomic Lua, client-clock with clamp), on by default when Redis is configured (`HOOKRAIL_GLOBAL_RATELIMIT=0` disables). Cap-relaxing under failure — limiter-command errors fall back to the per-replica bucket (fail-open) and Redis state loss reconstructs full buckets. A changed override propagates within one successful limits refresh. Tunables: `HOOKRAIL_RL_TIMEOUT_MS`, `HOOKRAIL_RL_TTL_FLOOR_S`. Chaos-proven fail-open liveness + cap re-enforcement oracle (E_RL).
- Unordered delivery path is unchanged.

## [P1]

Backend product surface, a published SDK, an admin dashboard, and a deployable
target — built in slices on top of the P0 core.

- **Admin surface (Slice A):** `hookrail-admin` CRUD / query / DLQ-replay API
  (RFC 7807 errors) and a retention janitor in `hookrail-scheduler`.
- **Python SDK (Slice B):** [`hookrail`](https://pypi.org/project/hookrail/) 0.1.0
  on PyPI — typed producer client (sync + async), delivery-status reads, and a
  webhook signature-verification helper.
- **Admin dashboard (Slice C):** React/TypeScript SPA behind a Go BFF that keeps
  the admin token server-side; password login with an HMAC-signed session cookie
  and an allowlist proxy.
- **k3s deploy (Slice E):** Kustomize base + prod overlay, multi-arch GHCR images,
  a Cloudflare Tunnel for public TLS, default-deny NetworkPolicies, and an
  attended cutover runbook. Plus ops-hardening (least-privilege DB role, server
  timeouts, narrowed error mapping).
- **Docs v2 (Slice F):** README rewritten as a lean front door, deep operator
  detail split into `docs/`, a non-vacuous `scripts/docs-verify.sh` that asserts
  documented facts against the code, and corrected stale env names/defaults.

## [P0]

- Project bootstrap: module layout, lint, CI.
- P0 core: transactional ingest with idempotency (replay/409), durable
  deliveries state machine, Redis Streams dispatch with PG sweeper repair,
  CAS claims with lease takeover, SSRF-guarded HMAC-signed delivery,
  classification + full-jitter backoff with Retry-After, DLQ.
- Compose environment with Prometheus, Grafana, OTel Collector, Jaeger.
- k6 baseline protocol + report queries; e2e suite; GHCR image with SBOM.
