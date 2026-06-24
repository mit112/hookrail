# Changelog

All notable changes documented here. Honest history — early entries include design corrections.

## [Unreleased]

### Redis HA — Sentinel + FailoverClient

- **Redis now runs HA** — a master+replica StatefulSet (`redis-0`, `redis-1`) behind a **3-node Sentinel quorum**, replacing the single-node Redis SPOF. On a master-pod loss Sentinel automatically promotes the surviving replica (RTO ~3–9s); both Redis consumers (the work-queue client used by api/admin/scheduler/worker, and the worker's rate-limiter client) are go-redis `FailoverClient`s that discover the current master through the quorum, so no app restart is needed across a failover.
- **Backward-compatible, opt-in.** A unified `internal/redisclient` constructor selects plain mode (bare `REDIS_ADDR`/`redis://` URL — unchanged for compose/dev/CI) or Sentinel mode (when `REDIS_SENTINEL_ADDRS` is set; `REDIS_MASTER_NAME` defaults to `hookrail`). `RedisConfigured` derivation makes `REDIS_ADDR` optional in Sentinel mode and flips `GlobalRateLimit` on by default under either. Compose and `-tags integration` stay plain-mode, byte-for-byte.
- **Application RPO=0 is a Postgres + sweeper property, not Redis.** Sentinel replication is asynchronous, so a promotion may drop recently-written stream/consumer-group state; that loss is re-driven from Postgres by the sweeper (at-least-once), never a lost delivery. The worker re-runs `EnsureGroup` on a `NOGROUP` error (a freshly-promoted master may be missing the group) instead of wedging, and both worker/scheduler bounded-retry `EnsureGroup` at startup so a not-yet-converged Sentinel quorum at boot doesn't crashloop the daemon. `min-replicas-to-write` is deliberately **not** set (it would make a freshly-promoted master reject writes until the killed pod rejoins, defeating availability for no durability gain).
- **k8s topology hardening:** each redis pod runs a Sentinel-aware role-selection entrypoint (quorum ≥2/3 → live-peer `ROLE` probe → genesis-only-`redis-0` on a provably-fresh PVC; a restart never re-seizes mastership), and Sentinels persist `sentinel.conf` on a PVC so a restart doesn't forget the current master.
- **Verified by a new main-only `redis-failover` CI job** that kills the master under continuous load and asserts a non-vacuous oracle: a **different-ordinal** pod is promoted by Sentinel (impossible without Sentinel + a live replica), the old pod rejoins as a replica, every accepted delivery converges to `succeeded` and was received end-to-end (per-`delivery_id` RPO=0 via PG+sweeper), and the worker recovers a destroyed consumer group **in place** (no pod restart). The duplicate count is logged-only — no tight numeric bound under async replication.
- **Single-node honesty:** on the live single Mac-mini k3s node this is process-level failover only; node/disk-loss HA needs a second node. The live cutover stays attended.

### Datastore HA — Postgres via CloudNativePG

- **Postgres now runs as a CloudNativePG (CNPG) 3-instance cluster** (`Cluster/hookrail-pg`) replacing the single-replica StatefulSet — the last app-tier-adjacent SPOF. Synchronous quorum replication (`method: any, number: 1`) with `dataDurability: required` and `failoverQuorum: true` guarantees **zero RPO for accepted (202-acked) events**: a commit is acked only once a standby holds it, and a primary loss only promotes a standby proven to hold that WAL (no stale promotion). The operator manifest is vendored + pinned (`deploy/k8s/cnpg/operator-1.29.1.yaml`).
- **Failover-resilient startup.** All DB-owning daemons (api/worker/scheduler/admin) now open the store via `OpenWithRetry` with a bounded `HOOKRAIL_DB_CONNECT_TIMEOUT` (default 30s) so a pod started mid-promotion waits for the new primary instead of crashlooping. The app connects to the CNPG `-rw` Service (current primary), reconnecting across a promotion.
- **Verified by a new main-only `pg-failover` CI job** that kills the primary under continuous load and asserts a non-vacuous oracle: failover actually happened (new primary UID, `-rw` flipped, old pod rejoins as replica), load crossed the no-primary window, every accepted delivery succeeds, receiver-distinct == DB succeeded (exact), duplicates within a mechanism-derived bound, and no app pod restarts.
- **Single-node honesty:** on the live single Mac-mini k3s node this is process-level failover only — node/disk-loss HA needs a second node. **Redis HA (Sentinel) is deferred** (Redis loss is already the at-least-once queue-loss case recovered by the sweeper).

### RBAC R3 — Producer-key topic scopes

- **Each `hk_` producer key is restricted to a set of allowed topic patterns** (`MatchTopic` semantics: exact, `foo.*` prefix, or `*`). `POST /v1/events` returns **403** for a topic outside the key's scope — a leaked or over-broad key can no longer publish arbitrary topics. The denial happens before the event is recorded and before the idempotency replay path (no event row, no replayable result).
- **Scopes are set at key creation and immutable:** `hookrail-ctl create-producer-key -name <n> -scope <pattern>` (repeatable, **≥1 required** — a scopeless key is refused). There is no admin API or dashboard surface for producer keys; rotate the key to change its scope. Deny-when-unscoped is **fail-closed** (a key with zero scope rows is denied everything).
- **Migration 0010** adds `producer_key_scopes` and backfills `'*'` for every pre-R3 key, so existing deployments are **non-breaking**. The dashboard test-event key is provisioned `'*'`-scoped at every site (compose, k8s keygen-job, k3s runbook). Scope enforcement becomes fully effective once all API replicas are on the R3 image (app-layer authz, same rolling-deploy model as R1/R2).
- **Python SDK:** new typed `ForbiddenError` (subclass of `HookrailAPIError`) for 403; it stays non-retryable.

### RBAC R2 — Dashboard users & role-aware UI

- **Per-user dashboard accounts** from a mounted secret file (`username:argon2id-hash:role`), replacing the single shared password. Login takes a username + password (uniform-failure verify); the `hookrail-dash-hash` helper mints file entries.
- **Role in the session, resolved live.** The cookie carries only a signed `sub`; the role is resolved per request from the live user file, so deleting/downgrading a user takes effect on their next request after a SIGHUP reload. **Breaking:** the legacy shared-password variable and the dashboard's `HOOKRAIL_ADMIN_TOKEN` are removed; provision `HOOKRAIL_DASHBOARD_USERS_FILE` + `HOOKRAIL_DASHBOARD_ROLE_TOKENS_FILE`.
- **Role-matched, attested upstream tokens.** The BFF forwards one of three role-scoped admin tokens chosen by the caller's live role, and **attests** each token against the new admin `GET /v1/whoami` (startup + periodic re-probe). A mislabeled/revoked token or unreachable admin API fails closed (`503`, `/readyz` not ready) and self-recovers. Per-route minimum roles mirror the R1 registry (drift-tested); `/api/test-event` requires operator.
- **Role-aware SPA:** username login; create/edit/delete/rotate gated to admin, replay + send-test-event to operator; privileged routes guarded against direct-URL access. UI gating is cosmetic — the BFF + admin API are the enforcement boundary.
- **`hookrail-ctl create-admin-token`** for bootstrap minting; compose + k8s provision the users + role-token secrets automatically.

### RBAC R1 — Admin API role-based access control

- **Three role-scoped admin tokens** ranked `viewer < operator < admin`, enforced per-route in the admin API middleware (`viewer` reads; `operator` adds replay/skip; `admin` adds config, secret rotation, and token management). A valid token below a route's minimum role gets `403`; missing/invalid/revoked gets `401`; a credential-store outage gets `503` (never fail-open).
- **`/v1/admin-tokens` management** (admin-only): `POST` mints a token (plaintext `hkadm_…` returned once, `Cache-Control: no-store`), `GET` lists metadata, `DELETE` revokes immediately. Tokens are stored only as SHA-256; active tokens are bounded to ~256 (best-effort anti-sprawl cap). Not proxied by the dashboard BFF in R1.
- **Break-glass:** `HOOKRAIL_ADMIN_TOKEN` remains a full-admin bootstrap credential via a constant-time compare, independent of the database. Fully backward compatible — existing deployments are unchanged until scoped tokens are minted.

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
