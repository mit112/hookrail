# Hookrail — Design Specification

This is the authoritative design document for Hookrail. Source comments across the
codebase cite it by section (e.g. `§3.4`, `§8`, `§11`); those citations refer to the
sections below. It is a living document — where an implementation deviates from the
text, the deviation is noted at the call site.

Status: pre-1.0. No wire- or API-compatibility guarantees yet.

---

## 1. What and why

**Hookrail is a self-hostable webhook / event delivery service.** Producers POST
events; Hookrail guarantees delivery to subscribed HTTP endpoints with retries,
exponential backoff, idempotency, HMAC signing, dead-letter queues, per-endpoint
rate limiting, replay, and full observability.

Every SaaS that fires webhooks runs this machinery. Hookrail is an open,
self-hosted implementation of it with published reliability numbers: measured
throughput and latency under a defined load profile, and zero lost delivery
obligations under an enumerated chaos suite.

---

## 2. Non-goals

- **No exactly-once delivery.** At-least-once plus consumer-side dedup, documented
  honestly (§4). Exactly-once over HTTP to arbitrary endpoints is impossible.
- **No multi-region and no multi-tenant billing.**
- **No per-endpoint FIFO ordering by default.** Strict-FIFO per-key ordering is an
  opt-in subscription mode; the default path is explicitly unordered.
- Deployment targets a single logical cluster; datastore HA is process-level
  failover, not multi-region replication.

---

## 3. Architecture

```
                    ┌────────────────────────── control plane ──────────────────┐
 producers ──POST──►│ Ingress API (Go)                                          │
   (curl/SDK)       │  auth (hashed keys) · validate · idempotency check        │
                    │  ONE PG TXN: insert event + fan-out deliveries rows       │──202──►
                    │  then XADD delivery_ids (Redis) — best-effort             │
                    └───────┬──────────────────────────────┬────────────────────┘
                            ▼                              ▼
                    PostgreSQL (SOURCE OF TRUTH)   Redis Streams (lossy hot path)
                    events · deliveries (state,    single delivery stream
                      next_attempt_at, lease)      consumer group "deliverers"
                    subscriptions · endpoints      messages carry delivery_id ONLY
                    attempts (append-only) · DLQ   PEL + XAUTOCLAIM (fast reclaim)
                            ▲      ▲                       │
                            │      │ due/lost deliveries   │
                            │   ┌──┴────────────────────┐  │
                            │   │ PG scheduler/sweeper  │──┘ (republish due IDs;
                            │   └───────────────────────┘     duplicates are safe)
                            │   ┌──────────────────────────────────────────┐
                            └───┤ Delivery workers (Go, goroutine pools)   │
                                │  XREADGROUP → CAS-claim deliveries row   │
                                │  SSRF-guarded signed POST (§8)           │
                                │  classify → record attempt + transition  │
                                │  state IN ONE PG TXN → XACK              │
                                └──────────────────────────────────────────┘
 admin/dashboard ◄── Admin API (CRUD subscriptions, attempt timelines, DLQ replay)
```

**The core argument:** cross-store atomicity between Redis and Postgres is
impossible, so Postgres owns every delivery obligation and Redis is only a lossy
accelerator. Losing Redis *delays* deliveries (the next sweep republishes them); it
never loses them. Each component is independently testable.

### 3.1 Ingress API — the fast path

Authenticate (hashed producer keys, §4), validate, run the idempotency check (§4),
then in **one Postgres transaction** insert the event **and one `deliveries` row per
matching subscription** (topic matching is evaluated at ingest — the durable
fan-out). After commit, `XADD` each `delivery_id` to the Redis stream on a
best-effort basis: if Redis is down, the sweeper (§3.3) publishes them on its next
pass. Return `202` with the event ID and delivery IDs.

**The 202 durability boundary is the Postgres commit.** If Postgres is unavailable
the API returns `503` — never a silent accept. The best-effort `XADD` is explicitly
outside the durability boundary.

### 3.2 Queue layer — Redis Streams

Redis Streams with a consumer group provide dispatch. Messages carry `delivery_id`
only; payloads are read from Postgres at attempt time. Redis is **not** durable
scheduling state — retries and lost messages are owned by Postgres
(`deliveries.next_attempt_at`). The stream is trimmed with approximate `MAXLEN`
(§5). AOF persistence narrows, but does not close, the loss gap; correctness does
not depend on it.

### 3.3 Scheduler / sweeper — the durability repair loop

Running inside the scheduler process, on startup and every 30s the sweeper publishes
to Redis every delivery that is **due** (`next_attempt_at <= now()`, state
`pending` / `retry_scheduled`) or **stuck** (`in_flight` with an expired lease).
Duplicate publication is safe — workers claim idempotently (§3.4). This loop is what
makes the best-effort `XADD` and Redis loss recoverable: whatever Postgres marks due
or stuck gets republished. Only one scheduler sweeps at a time (leader election via a
Postgres session-scoped advisory lock, ownership verified against `pg_locks`).

### 3.4 Delivery workers — the CAS claim

A goroutine pool per worker: `XREADGROUP` → **compare-and-swap claim** the
deliveries row → SSRF-guarded signed POST with a timeout budget (§8) → classify the
outcome (§7) → **record the attempt and apply the state transition in one Postgres
transaction** → `XACK`.

The claim is the concurrency-correctness core:

```sql
UPDATE deliveries
   SET state = 'in_flight', lease_until = now() + lease, claim_version = claim_version + 1
 WHERE id = $1
   AND (state IN ('pending','retry_scheduled')
        OR (state = 'in_flight' AND lease_until < now()))
```

Zero rows updated → someone else owns it → ack and drop. Exactly one claimant wins.
A live lease is respected; an expired lease is takeable. Because both recovery layers
(§10) funnel through this same CAS, overlapping recovery is collision-safe. A
`claim_version` fence lets a graceful drain release stragglers to `retry_scheduled`
without racing an in-progress claim. **Duplicates are possible; loss is not.**

---

## 4. Core decisions and rationale

| Decision | Choice | Why |
|---|---|---|
| Source of truth | **PostgreSQL owns delivery state** (`deliveries` table); Redis is a lossy hot path | Cross-store atomicity is impossible; one authoritative store + idempotent republication removes loss windows. Losing Redis delays deliveries, never loses them |
| Queue | **Redis Streams** | Consumer groups, PEL, and `XAUTOCLAIM` give real streaming semantics at low memory cost; Postgres, not Redis, holds retry state |
| Delivery semantics | **At-least-once** + consumer dedup on `delivery_id` | Exactly-once over HTTP is impossible. Duplicates share a `delivery_id` (`event_id` is ambiguous under overlapping subscriptions), so that is the dedup key. Both IDs ship in headers |
| Producer dedup | Dedicated **idempotency table** (§5), not a unique index | A unique index cannot expire or store a response; correct semantics need stored responses plus body-hash conflict detection (409) |
| Backoff | Exponential, **full jitter**, base 5s, cap 6h, default 8 attempts; `Retry-After` overrides when larger (capped at 6h) | Jitter prevents thundering herds; honoring `Retry-After` is correct HTTP citizenship |
| Signing | HMAC-SHA256, `hookrail-signature: t=<ts>,v1=<sig>`, headers carry `delivery_id` + `event_id`, ±5 min tolerance | Replay-resistant, rotation-friendly (§8) |
| Secrets / keys | Producer API keys **stored hashed** (SHA-256, constant-time compare); endpoint HMAC secrets **AES-256-GCM encrypted at rest** (master key from env/KMS) | Keys need only verification (hash); signing needs the plaintext secret back (so encrypt, don't hash) |
| Rate limiting (§4.3) | Token bucket per endpoint; per-replica local by default, Redis-backed **global** for endpoints with an explicit override | Teaches the algorithm and its distributed form; the global path is cap-relaxing under failure (fail-open), never throttling below the cap |
| Config | env + flags, 12-factor; no config service | Boring on purpose |

The exact backoff formula: `delay = uniform[0, min(cap, base · 2^(n-1))]`, with a
larger `Retry-After` winning (still capped at 6h). Backoff policy is per-delivery
(§4.3): a subscription may carry its own policy JSON; absent one, the worker default
applies.

---

## 5. Data model (Postgres)

- `events(id ulid pk, producer_key_id, topic, payload jsonb, payload_size, created_at)` — payload capped at **256KB** (413 above)
- `deliveries(id ulid pk, event_id fk, subscription_id fk, state enum, attempt_count, next_attempt_at, lease_until, claim_version, created_at, updated_at)` — the durable obligation "event X must reach subscription Y", created in the ingest transaction; the state machine (§6) lives here. `claim_version` is the drain/claim fence and never resets, even across replay
- `idempotency_keys(producer_key_id, idem_key, body_hash, event_id, response_snapshot jsonb, expires_at)` — pk `(producer_key_id, idem_key)`; replay returns the snapshot, same key + different `body_hash` → 409; expired rows purged
- `subscriptions(id, topic_pattern, endpoint_id, backoff_policy jsonb, max_attempts, rate_limit_rps, ordered, active)`
- `endpoints(id, url, secret_ciphertext, description, created_at)` — URL re-validated against SSRF policy (§8) at write **and** at every dial
- `delivery_attempts(id, delivery_id fk, attempt_no, status enum, http_status, latency_ms, error_class, requested_at, completed_at)` — append-only
- `dead_letters(id, delivery_id fk, final_error, dead_at, replayed_at nullable)`
- `producer_keys(id, key_hash, name, created_at, revoked_at)` — plus per-key topic scopes

IDs are ULIDs (sortable, URL-safe). Migrations use `golang-migrate` from commit one.
**Retention** (a janitor loop): idempotency rows purged after 24h; event payloads
tombstoned and attempt rows trimmed after 30 days (configurable); Redis stream
trimmed with approximate `MAXLEN ~ 100k`. Hot-query indexes exist for the due-scan
(`deliveries(state, next_attempt_at)`), per-endpoint timelines, and DLQ browse.

---

## 6. Delivery state machine

Lives in `deliveries`. Transitions:

```
pending        → in_flight       → succeeded
in_flight      → retry_scheduled → in_flight     (attempt < max; next_attempt_at set by backoff / Retry-After)
in_flight      → dead_lettered                    (attempt = max, or a permanent failure §7)
dead_lettered  → pending                           (manual replay; attempt_count resets, lineage kept in attempts log)
```

An ordered-mode head that dead-letters can also reach `skipped` (a terminal state)
by operator action; the key then advances. Every transition is written in the **same
Postgres transaction** as its attempt record. Recovery is two-layer (§10): Redis
PEL / `XAUTOCLAIM` (seconds-scale) and the PG sweeper (30s-scale). Both funnel through
the §3.4 CAS, so overlapping recovery is safe — duplicates possible, loss not.

Note: `attempt_count` resets on manual replay, but `claim_version` does not — the
fence keeps counting so a replay can never be captured by a stale in-flight claim.

---

## 7. HTTP outcome classification

| Outcome | Class | Action |
|---|---|---|
| 2xx | success | `succeeded` |
| 408, 425, 429 | retryable | retry; honor `Retry-After` if larger than backoff (cap 6h) |
| other 4xx | permanent | `dead_lettered` (error class recorded) |
| 5xx, timeout, connection error | retryable | retry per backoff |
| 3xx | permanent | **redirects are never followed** (SSRF + semantics); recorded as `redirect_rejected` |
| TLS / SSRF policy violation | permanent | `dead_lettered` with `error_class=policy` |
| worker panic (poison payload) | permanent | recovered per-message, quarantined to DLQ with `error_class=panic` |

---

## 8. Security

Hookrail POSTs to user-supplied URLs — it is an SSRF machine without defenses, so
these are baseline requirements, not polish.

### SSRF defenses

- HTTPS required in hosted mode (an `--allow-http` flag exists for local/self-host
  dev only).
- Resolve DNS **once**, then reject loopback, RFC1918, link-local (169.254.0.0/16,
  including cloud metadata), CGNAT, and ULA/IPv6-local ranges — and **dial the
  validated IP** via a custom `DialContext`, never re-resolving. This defeats DNS
  rebinding.
- Redirects disabled (§7). Response bodies read to a 64KB cap then discarded
  (slow-loris / huge-body defense).
- Split timeout budget: connect 3s / TLS 3s / response headers 10s / total 15s.
- URL policy is enforced **at endpoint registration and at every dial** — DNS can
  change between the two.
- Optional CIDR allowlist mode lets self-hosters deliver into internal networks
  they explicitly trust.

### Secrets & signing

Producer API keys are hashed (SHA-256, constant-time compare). Endpoint HMAC secrets
are AES-256-GCM encrypted at rest with a master key from env (KMS for a managed
path). The signature scheme:

```
hookrail-signature: t=<unix>, v1=HMAC_SHA256(secret, t || '.' || delivery_id || '.' || body)
```

Receivers verify `t` within ±5 min. Rotation uses a dual-secret window: after
`rotate-secret` the old secret stays valid until in-flight attempts drain, and the
new secret is returned exactly once and never stored in plaintext.

### Auth & RBAC

- **Admin API RBAC.** Every admin `/v1` route enforces a hierarchical role:
  `viewer < operator < admin`. Tokens are minted scoped to a role (`hkadm_…`,
  returned once), only their SHA-256 is stored, and a token below a route's minimum
  role gets `403` (missing/revoked → `401`). `GET /v1/whoami` returns the caller's
  role.
- **Dashboard users.** The dashboard BFF authenticates per-user accounts
  (`username:argon2id-hash:role`), issues a signed session cookie carrying only the
  username, and resolves the role live per request. It forwards a role-matched
  upstream token that it attests against the admin API and fails closed if
  attestation fails.
- **Producer-key topic scopes.** Each producer key carries a set of topic scopes;
  posting an event to an out-of-scope topic is rejected fail-closed (`403`).

---

## 9. API surface

**Ingest:** `POST /v1/events` (producer-key auth) · `GET /v1/events/{id}` (status +
per-delivery attempt timeline).

**Admin** (role-gated, §8): CRUD `/v1/endpoints`, `/v1/subscriptions`;
`GET /v1/deliveries` and `/v1/deliveries/{id}`; `GET /v1/dlq`;
`POST /v1/dlq/{id}/replay`; `POST /v1/deliveries/{id}/skip`;
`POST /v1/endpoints/{id}/rotate-secret`; `GET /v1/ordered-keys`; admin-token CRUD.

**Ops:** `/healthz`, `/readyz`, `/metrics` (not auth-gated).

Errors use **RFC 7807** `application/problem+json`. An OpenAPI spec is generated and
committed; per-operation response schemas are conformance-tested. The published SDK
follows the contract. Keyset pagination uses opaque ULID cursors (best-effort:
same-millisecond items may occasionally skip/duplicate across pages — suitable for
browsing, not strict enumeration).

---

## 10. Failure modes

| Failure | Behavior |
|---|---|
| Worker crash mid-delivery | Redis PEL reclaim (seconds) and/or PG lease expiry (≤ 30s + lease) → CAS re-claim → redeliver. Duplicate possible, loss impossible (the PG row persists) |
| Redis restart / data loss | Deliveries delayed, never lost: the sweeper republishes everything PG marks due/stuck. AOF narrows the gap |
| `XADD` fails after ingest commit | Sweeper publishes the pending row on its next pass (≤ 30s) |
| Postgres down | Ingress `503` (no silent accept — the 202 promise requires the commit); workers pause via a health gate |
| Consumer 5xx / timeout | Retry per backoff + `Retry-After` |
| Consumer 4xx (non-408/425/429) | Permanent → DLQ with error class |
| Poison payload (worker panic) | Recovered per-message, quarantined to DLQ with `error_class=panic` |
| Producer floods | `429` + per-key token bucket at ingress |
| Slow consumer | Per-endpoint rate limit + timeout budget keeps the pool healthy |
| Malicious endpoint (SSRF, redirect games, slow-loris) | §8 policy: blocked ranges, no redirects, capped body, hard timeouts |

**Zero loss is defined precisely:** after the queue drains, every `deliveries` row is
in a terminal state (`succeeded` / `dead_lettered` / `skipped`) or has a future
`next_attempt_at` — none stranded. This holds while Postgres storage is intact (the
enumerated failure scope). **Duplicate budget:** 0 in steady state, < 1% under chaos.
**Recovery:** stuck deliveries resume < 60s after a fault clears.

---

## 11. Testing & measurement

- **Unit:** state-machine transitions, backoff math (including the `Retry-After`
  interaction), signature generate/verify, token bucket, SSRF policy table (IP
  ranges, the rebinding case).
- **Integration** (testcontainers, Postgres + Redis): idempotency under concurrent
  duplicate POSTs (replay + 409 paths), CAS-claim races (two workers, one delivery),
  lease takeover, sweeper republication, retry scheduling.
- **End-to-end:** a docker-compose stack with a `test-receiver` (modes: succeed,
  fail-N-times, timeout, flap, redirect, slow-body) driving happy and unhappy paths.
- **Load (k6) — a measurement protocol, not vibes:**
  - Committed profiles: 1KB payload; fan-out 1 and 3 subscriptions; healthy
    consumers (instant 200).
  - The generator runs on a separate machine (network documented); 2 min warm-up,
    10 min sustained.
  - Published metrics: sustained ingest events/s; **end-to-end p50/p95/p99 = 202
    response → consumer 2xx receipt**; first-attempt dispatch latency; duplicate
    rate. The 202→receipt window and any internal timestamps are distinct domains and
    are never compared against each other.
  - Numbers are published as measured, whatever they are.
- **Chaos** (binary pass criteria, verifier-asserted, run in CI): `kill -9` workers
  under load; Redis restart / `FLUSHALL`; Postgres process restart (single node — no
  failover claimed at this tier); flaky-consumer load. The zero-loss and
  duplicate-budget definitions above are the oracle.

CI runs lint, vet, and unit + integration on every PR; e2e and the image build on
`main`; chaos nightly. **Merges are blocked on green CI.**

---

## 12. Deployment & observability

- **Local / dev:** Docker Compose (api, workers, scheduler, admin, dashboard,
  Postgres, Redis, Prometheus, Grafana, OTel Collector, Jaeger, test-receiver).
- **Live demo:** single-node k3s behind a Cloudflare Tunnel — the Ingest API and
  dashboard get public hostnames, the admin API stays `ClusterIP`-only (a
  NetworkPolicy allows only dashboard → admin), and public TLS terminates at the
  edge. Secrets are created attended; nothing is committed.
- **Datastore HA:** Postgres as a CloudNativePG 3-instance synchronous-quorum
  cluster (zero RPO for accepted events); Redis as a master+replica StatefulSet
  behind a 3-node Sentinel quorum with automatic promotion. On a single k3s node
  these are process-level failover only; multi-node node/disk-loss tolerance is
  deferred.
- **Observability:** the OTel SDK feeds an OTel Collector → Jaeger for traces;
  Prometheus scrapes api/worker/scheduler and Grafana serves curated boards
  (`overview`, `resilience`). Metrics include ordered-key blocked/backlog gauges
  re-derived from Postgres.
- **Images:** multi-arch (amd64/arm64) built in CI, with an SBOM step.

---

## 13. Roadmap

Delivered:

- **Core** — durable ingest, the delivery state machine, SSRF-guarded signed
  delivery, backoff, DLQ, and a measured baseline.
- **Product surface** — admin API + React/TypeScript dashboard, a Python SDK on
  PyPI, retention/trimming, the k3s deploy, the chaos suite, and curated Grafana
  boards.
- **Distinction** — opt-in strict-FIFO per-key ordering with pause-on-dead-letter,
  skip, a backlog cap, and chaos-proven zero-reorder; app-tier HA (scheduler leader
  election, worker graceful drain, global rate limiting); datastore HA (Postgres +
  Redis).

Deferred: multi-node k3s (node/disk-loss tolerance), in-cluster TLS/WAF independent
of the tunnel, and a `hookrail-ts` SDK.
