# Hookrail P1 Slice E — k3s Deploy + Ops-Hardening — Implementation Plan

> **For agentic workers:** This plan is executed by the **Reasonix/DeepSeek bounded ralph loop**
> (one milestone per run), gated by Claude (Opus). It is NOT executed by Claude subagents. Steps use
> checkbox (`- [ ]`) syntax. Follow `docs/reasonix/hookrail-bridge.md` and the design doc
> `../../../docs/superpowers/specs/2026-06-19-hookrail-p1-sliceE-design.md` (rev2 FINAL).

**Goal:** Make Hookrail deployable to a single-node k3s cluster with a public URL (Cloudflare Tunnel),
verified on an ephemeral k3d cluster, and land four deferred ops-hardening fixes.

**Architecture:** Kustomize base + ephemeral/prod overlays for the 13-service stack; the loop authors +
verifies against ephemeral k3d (gate-only e2e) + CI; the live public cutover is attended. Go/SQL
hardening fixes ship first (M-E1).

**Tech Stack:** Go 1.26, pgx/v5 v5.10.0, golang-migrate/v4 v4.19.1 (pgx5 driver), Kustomize (kubectl
v1.33 built-in), k3d, kubeconform, Docker buildx (cross-compile), Cloudflare Tunnel, GHCR.

## Global Constraints

- Module path `github.com/mit112/hookrail`; never write the literal GH login into source.
- All gates **Opus** (Fable 5 gone). **Codex `gpt-5.5` read-only pre-gate on M-E1, M-E3, M-E4.**
- Single-task milestones use hyphenated `TASKS: N-N`. Green VERIFY is vacuous while code/manifests are
  stubs — confirm files exist; M-E3 gate must boot a real k3d cluster.
- Per-iteration VERIFY: `make build && make lint && make test && make itest` (Go tasks) and/or
  `kubectl kustomize deploy/k8s/overlays/<env> | kubeconform -strict -summary` (manifest tasks).
- Gate VERIFY adds the milestone-specific step (M-E3/M-E4 = `scripts/k8s-e2e.sh`, 0-leak teardown).
- Shell in scripts: use `/bin/ps`, `/usr/bin/sed`, etc. (zsh aliases shadow them).
- New integration tests MUST self-provision deps (testcontainers), matching CI's environment.
- The loop NEVER performs the live cutover and never holds a real secret / tunnel token.

---

# MILESTONE M-E1 — Ops-hardening (Tasks 1–5) · Codex pre-gate

VERIFY (per-iter + gate clean-worktree): `make build && make lint && make test && make itest`.

### Task 1: getEvent 404-masking fix (store boundary)

**Files:**
- Modify: `internal/store/status.go:28-33` (wrap first-row miss as `ErrNotFound`)
- Modify: `internal/api/server.go:128-136` (map `ErrNotFound`→404, else→500)
- Test: `internal/api/server_test.go` (or the existing api test file), `internal/store/status_test.go`

**Interfaces:**
- Consumes: `store.ErrNotFound` (`internal/store/store.go:45`), `pgx.ErrNoRows`.
- Produces: `GetEventStatus` returns `store.ErrNotFound` only for an absent event; `getEvent` returns
  404 only for `store.ErrNotFound`, 500 for any other error.

- [ ] **Step 1: Write failing store test** — in `internal/store/status_test.go` (integration, testcontainers):
```go
func TestGetEventStatus_MissingEvent_ReturnsErrNotFound(t *testing.T) {
	st := testStore(t) // existing testcontainers helper
	_, err := st.GetEventStatus(context.Background(), "01J000000000000000MISSING0")
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("want store.ErrNotFound, got %v", err)
	}
}
```

- [ ] **Step 2: Run, verify it fails** — `go test -tags integration ./internal/store -run TestGetEventStatus_MissingEvent -race` → FAIL (raw pgx.ErrNoRows, not ErrNotFound).

- [ ] **Step 3: Implement store wrap** — `internal/store/status.go`, the first QueryRow:
```go
	if err := s.Pool.QueryRow(ctx,
		`SELECT id, topic FROM events WHERE id = $1`, eventID).Scan(&st.EventID, &st.Topic); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return st, ErrNotFound
		}
		return st, err
	}
```
(Ensure `errors` and `github.com/jackc/pgx/v5` are imported.)

- [ ] **Step 4: Write failing api test (white-box `package api`)** — `Server` holds a concrete
  `*store.Store` (`internal/api/server.go:31-39`), NOT an interface, so a stub cannot be injected, and
  closing the pool would make `authProducer` (`internal/api/auth.go:22-25`) return 401 before `getEvent`
  runs. So test `getEvent` **directly** (white-box, same package), bypassing the router/auth, with a
  `Server` whose store's pool is closed → `GetEventStatus` returns a non-ErrNotFound error → expect 500;
  and an integration variant for the 404/200 paths:
```go
// internal/api/getevent_internal_test.go  (package api)
func TestGetEvent_DBError_Returns500(t *testing.T) {
	st := testStoreOpen(t)        // integration helper that opens a real *store.Store
	st.Pool.Close()              // force a non-ErrNotFound error from GetEventStatus
	s := &Server{store: st}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/v1/events/x", nil)
	req.SetPathValue("id", "x")
	s.getEvent(rr, req)
	if rr.Code != http.StatusInternalServerError { t.Fatalf("want 500, got %d", rr.Code) }
}
// 404 path (missing id) + 200 path (event with zero deliveries) via the normal integration harness.
```

- [ ] **Step 5: Implement api mapping** — `internal/api/server.go`:
```go
func (s *Server) getEvent(w http.ResponseWriter, r *http.Request) {
	status, err := s.store.GetEventStatus(r.Context(), r.PathValue("id"))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			problem(w, http.StatusNotFound, "event not found", "no event with that id")
			return
		}
		problem(w, http.StatusInternalServerError, "internal error", "failed to load event status")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(status)
}
```

- [ ] **Step 6: Run tests** — `make test && make itest` → PASS.
- [ ] **Step 7: Commit** — `fix(api): return 500 not 404 when getEvent hits a DB error (store.ErrNotFound boundary)`.

### Task 2: Operator-secret min-length (≥16 chars) + fixture updates

**Files:**
- Modify: `internal/dashboard/config.go` (password length check), `internal/admin` auth/config (token length check at startup)
- Modify: `deploy/compose/docker-compose.yml:93,136` (`dev-admin-token`→`dev-admin-token-001` ≥16), e2e/admin defaults if any
- Modify: `internal/dashboard/config_test.go:16` (`admintok`→a ≥16 value)
- Test: `internal/dashboard/config_test.go`, admin config test

**Interfaces:**
- Produces: both binaries refuse to boot when the admin token / dashboard password is < 16 chars.

- [ ] **Step 1: Failing test (dashboard)** — short password rejected:
```go
func TestLoadConfig_RejectsShortPassword(t *testing.T) {
	t.Setenv("HOOKRAIL_DASHBOARD_PASSWORD", "short")
	// …set other required env to valid values…
	if _, err := LoadConfig(); err == nil {
		t.Fatal("want error for <16-char password")
	}
}
```
- [ ] **Step 2: Run, verify fail** — `go test ./internal/dashboard -run RejectsShortPassword` → FAIL.
- [ ] **Step 3: Implement** — `dashboard.LoadConfig()` returns `(Config, error)` (a VALUE, not a
  pointer — `internal/dashboard/config.go:25`), so return the zero `Config{}` on error, NOT `nil`:
  `if len(pw) < 16 { return Config{}, fmt.Errorf("HOOKRAIL_DASHBOARD_PASSWORD must be ≥16 chars") }`.
  Do the admin-token check **only in `cmd/hookrail-admin/main.go`** at startup — NOT in the shared
  `internal/config/config.Load()` (`config.go:36-58` is used by api/worker/scheduler/ctl too; validating
  the admin token there would break the non-admin binaries that don't set it).
- [ ] **Step 4: Update fixtures** — bump `dev-admin-token`→`dev-admin-token-001` in compose (`:93`,`:136`) and `internal/dashboard/config_test.go:16` to a ≥16 value; grep `git grep -n 'dev-admin-token\|admintok'` and fix every occurrence (incl. `scripts/`, `test/`).
- [ ] **Step 5: Run** — `make test && make itest && make e2e`-relevant unit paths → PASS.
- [ ] **Step 6: Commit** — `feat(security): enforce ≥16-char admin token + dashboard password; update dev fixtures`.

### Task 3: Login body cap + ListenAndServe timeouts

**Files:**
- Modify: `cmd/hookrail-dashboard/main.go:18-19` (real `http.Server` with timeouts; drop `//nolint:gosec`)
- Modify: `cmd/hookrail-admin/main.go:75`, `cmd/hookrail-api/main.go:61` (set timeouts on the existing `srv`)
- Modify: `cmd/hookrail-worker/main.go:78`, `cmd/hookrail-scheduler/main.go:61` (the partial `http.Server`s
  with only `ReadHeaderTimeout` → add the full policy; design §231-235 requires ALL FIVE servers)
- Modify: `internal/dashboard` login handler (MaxBytesReader on the body)
- Test: dashboard login handler test (oversized body → 413)

**Interfaces:**
- Produces: all three public-facing servers set Read/ReadHeader/Write/Idle timeouts; login rejects bodies
  over a fixed cap (e.g. 64 KiB) with 413.

- [ ] **Step 1: Failing test** — POST a >64 KiB login body, expect 413 (`http.MaxBytesReader` →
  `http.MaxBytesError`). Assert handler maps it to 413.
- [ ] **Step 2: Run, verify fail.**
- [ ] **Step 3: Implement login cap** — wrap `r.Body = http.MaxBytesReader(w, r.Body, 64<<10)` at the top
  of the login handler; on decode error that is a `*http.MaxBytesError`, write 413.
- [ ] **Step 4: Implement timeouts** — dashboard:
```go
srv := &http.Server{
	Addr:              cfg.Addr,
	Handler:           h.Handler(),
	ReadHeaderTimeout: 5 * time.Second,
	ReadTimeout:       15 * time.Second,
	WriteTimeout:      30 * time.Second,
	IdleTimeout:       60 * time.Second,
}
if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed { /* … */ }
```
  Set the same four timeouts on the admin (`:75`), api (`:61`), **worker (`:78`) and scheduler (`:61`)**
  `http.Server` structs (worker/scheduler keep `ReadHeaderTimeout` and gain Read/Write/Idle — all five
  servers per design §231-235). Remove the dashboard `//nolint:gosec // G114` comment. **Confirm no
  streaming handler** (`git grep -n 'http.Flusher\|Flush()\|text/event-stream'` → none) so WriteTimeout is safe.
- [ ] **Step 5: Run** — `make build && make lint && make test` → PASS (lint no longer needs the gosec suppression).
- [ ] **Step 6: Commit** — `feat(security): cap login body + set HTTP server timeouts on dashboard/admin/api`.

### Task 4: Least-priv `hookrail_app` role + remove scheduler self-migration

**Role model (folds #1,#2):** there is NO separate `hookrail_migrator` role. The Postgres **bootstrap
owner `hookrail`** (POSTGRES_USER, owns all objects from 0001/0002) IS the migrator — the migrate Job
connects with the owner DSN and can DDL everything (a `GRANT ALL` role can't own/ALTER objects it didn't
create, so a non-owner "migrator" is not viable). This migration creates only the least-priv runtime role
`hookrail_app` (DML + sequence usage, NO DDL), with NO password — the app role's **password is assigned
at deploy time** by an owner-run `ALTER ROLE hookrail_app LOGIN PASSWORD '<from Secret>'` step (documented
in the runbook Task 17; in the ephemeral overlay the dev-secret carries it and a tiny bootstrap Job sets
it). App Deployments use the `hookrail_app` DSN; the migrate Job uses the owner DSN.

**Files:**
- Create: `internal/store/migrations/0004_app_role.up.sql`, `0004_app_role.down.sql`
- Modify: `cmd/hookrail-scheduler/main.go:42-45` (delete the `s.Migrate()` block)
- Test: `internal/store/migration0004_test.go` (app role has DML+sequence usage, CANNOT DDL)

**Interfaces:**
- Produces: role `hookrail_app` (NOLOGIN until a deploy-time password ALTER; DML + `USAGE,SELECT` on
  sequences; no DDL). Migration idempotent (`DO $$ … IF NOT EXISTS`). Scheduler no longer self-migrates.

- [ ] **Step 1: Failing test (isolated container — fold #3,#4)** — the shared `testStore(t)`
  (`internal/store/testutil_test.go:28`) reuses ONE container across tests and creates per-test DBs;
  cluster-wide roles would leak between tests, so spin a **dedicated** testcontainers Postgres for this
  test only. Use the real helper signature (`testStore` exists; `startPG`/`connectAs` do NOT — write them
  or inline). Setup: open+`Migrate` as owner, then `ALTER ROLE hookrail_app LOGIN PASSWORD 'testpw'`
  (owner-run), then connect as `hookrail_app`:
```go
func TestAppRoleCanDMLNotDDL(t *testing.T) {
	ownerDSN := startDedicatedPG(t)               // dedicated testcontainers PG (NOT the shared testStore)
	st, _ := store.Open(context.Background(), ownerDSN); defer st.Close()
	mustNil(t, st.Migrate())
	_, _ = st.Pool.Exec(context.Background(), `ALTER ROLE hookrail_app LOGIN PASSWORD 'testpw'`)
	appPool := connectDSNAsRole(t, ownerDSN, "hookrail_app", "testpw")
	// INSERT into endpoints (BIGSERIAL id) must succeed:
	mustNil(t, appPool.Exec(ctx, `INSERT INTO endpoints (url) VALUES ('https://x.test')`))
	// CREATE TABLE must fail with permission denied:
	if _, err := appPool.Exec(ctx, `CREATE TABLE z(i int)`); err == nil { t.Fatal("app role must not DDL") }
}
```
- [ ] **Step 2: Run, verify fail** (`hookrail_app` doesn't exist yet).
- [ ] **Step 3: Write migration** — `0004_app_role.up.sql` (creates ONLY `hookrail_app`, NOLOGIN +
  passwordless; the deploy-time owner step grants LOGIN+PASSWORD). Run by the owner, so the grants take):
```sql
-- Slice E: least-privilege runtime role. NO password here (assigned at deploy time from a k8s Secret via
-- an owner-run ALTER ROLE). The bootstrap owner `hookrail` remains the migrator (this runs as owner).
DO $$ BEGIN
  IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname='hookrail_app') THEN
    CREATE ROLE hookrail_app NOLOGIN; END IF;
END $$;
GRANT USAGE ON SCHEMA public TO hookrail_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO hookrail_app;
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public TO hookrail_app;  -- BIGSERIAL PKs (0001:72,90)
ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO hookrail_app;
ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT USAGE, SELECT ON SEQUENCES TO hookrail_app;
```
  `0004_app_role.down.sql`: **do NOT `DROP ROLE`** (cluster-wide; would poison a shared container and can
  fail on dependent grants — fold #4). Only `REVOKE` the grants (guarded, idempotent); leave the role.
  (No advisory-lock grant needed — `pg_advisory_xact_lock` is callable by any role; confirmed
  `retention.go:26`.)
- [ ] **Step 4: Remove scheduler self-migration** — delete `cmd/hookrail-scheduler/main.go:42-45`
  (`if err := s.Migrate(); …`). The migrate Job is the sole migrator.
- [ ] **Step 5: Run** — `make build && make itest` → PASS (the app-role test green; scheduler still boots
  via the e2e/itest harness, which migrates separately as owner).
- [ ] **Step 6: Commit** — `feat(db): least-priv hookrail_app role (owner stays migrator); stop scheduler self-migration`.

### Task 5: CONCURRENTLY index migration (convention + real index)

**Files:**
- Create: `internal/store/migrations/0005_dead_letters_dead_at_concurrently.up.sql` + `.down.sql`
- Test: `internal/store/migration0005_test.go` (migration applies; index exists)

**Interfaces:**
- Produces: `idx_dead_letters_dead_at` created via `CREATE INDEX CONCURRENTLY` in its own single-statement
  migration — the production-safe convention for all future live-table indexes.

- [ ] **Step 1: Failing test** — after migrate, assert `idx_dead_letters_dead_at` exists in `pg_indexes`:
```go
func TestMigration0005CreatesDeadAtIndexConcurrently(t *testing.T) {
	dsn := startPG(t); must(store.Open+Migrate(dsn))
	// SELECT 1 FROM pg_indexes WHERE indexname='idx_dead_letters_dead_at' → expect 1 row
}
```
- [ ] **Step 2: Run, verify fail.**
- [ ] **Step 3: Write migration (single statement, NO transaction wrapping)** —
  `0005_dead_letters_dead_at_concurrently.up.sql` contains EXACTLY:
```sql
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_dead_letters_dead_at ON dead_letters (dead_at);
```
  `.down.sql`: `DROP INDEX CONCURRENTLY IF EXISTS idx_dead_letters_dead_at;`
  (The golang-migrate pgx5 driver's `Run` reads the file and calls `runStatement` directly — the
  migration STATEMENT is NOT wrapped in a transaction; only the version-bookkeeping uses its own separate
  tx — so a single `CREATE INDEX CONCURRENTLY` is legal. Source: `golang-migrate/migrate/v4@v4.19.1/
  database/pgx/v5/pgx.go:252-270` (runStatement) and `:339-368` (version tx). Do NOT add other statements
  to this file.)
- [ ] **Step 4: Run** — `make itest` → PASS (proves CONCURRENTLY runs through this migrate stack).
- [ ] **Step 5: Update the 0002 header comment** — change `internal/store/migrations/0002_*.up.sql:1` note
  to reference 0005 as the established convention.
- [ ] **Step 6: Commit** — `feat(db): production-safe CONCURRENTLY index on dead_letters.dead_at (convention)`.

**M-E1 gate:** Codex pre-gate (secrets/timeouts/error-handling/DB-privilege). Clean-worktree
`make build && make lint && make test && make itest` green. Confirm scheduler no longer migrates; app
role truly cannot DDL; CONCURRENTLY migration applies.

---

# MILESTONE M-E2 — Kustomize base (Tasks 6–10)

VERIFY (per-iter + gate): `kubectl kustomize deploy/k8s/base | kubeconform -strict -summary
-schema-location default` (kubeconform installed by preflight). No cluster needed.

### Task 6: Namespace, ConfigMap, base kustomization skeleton

**Files:** Create `deploy/k8s/base/namespace.yaml`, `configmap.yaml`, `kustomization.yaml`.

- [ ] **Step 1:** `namespace.yaml` → `apiVersion: v1 / kind: Namespace / metadata.name: hookrail`.
- [ ] **Step 2:** `configmap.yaml` — non-secret env mirroring the compose `hookrail-env` minus secrets:
```yaml
apiVersion: v1
kind: ConfigMap
metadata: { name: hookrail-config, namespace: hookrail }
data:
  REDIS_ADDR: "redis:6379"
  OTEL_EXPORTER_OTLP_ENDPOINT: "http://otel-collector:4318"
  HOOKRAIL_ADMIN_URL: "http://admin:8082"
  HOOKRAIL_INGRESS_URL: "http://api:8080"
```
- [ ] **Step 3:** `kustomization.yaml` listing all base resources (added across Tasks 6–10).
- [ ] **Step 4: Verify** — `kubectl kustomize deploy/k8s/base | kubeconform -strict -summary` → PASS (so far).
- [ ] **Step 5: Commit** — `feat(k8s): base namespace + config + kustomization skeleton`.

### Task 7: Postgres + Redis StatefulSets (+ PVCs, headless Service)

**Files:** Create `deploy/k8s/base/postgres.yaml`, `redis.yaml`; add to kustomization.

- [ ] **Step 1:** `postgres.yaml` — StatefulSet (1 replica, `postgres:16-alpine`), `volumeClaimTemplates`
  PVC, headless Service `postgres:5432`, readiness probe `pg_isready -U hookrail`. **Bootstrap env (must
  match compose `:13-16`):** `POSTGRES_DB=hookrail`, `POSTGRES_USER=hookrail` (from ConfigMap), and
  `POSTGRES_PASSWORD` from Secret `hookrail-db` key `owner_password`. The migrate Job + bootstrap connect
  as this owner (`hookrail`); the app DSN uses the `hookrail_app` role (Task 4). Resource req/limits (small).
- [ ] **Step 2:** `redis.yaml` — StatefulSet (`redis:7-alpine`, `--appendonly yes`), PVC, Service
  `redis:6379`, readiness `redis-cli ping`.
- [ ] **Step 3: Verify** kubeconform → PASS.
- [ ] **Step 4: Commit** — `feat(k8s): postgres + redis statefulsets with PVCs and probes`.

### Task 8: Migrate Job + initContainer wait pattern

**Files:** Create `deploy/k8s/base/migrate-job.yaml`; add to kustomization.

- [ ] **Step 1:** `migrate-job.yaml` — `kind: Job`, `backoffLimit: 3`, container runs
  `["hookrail-ctl","migrate"]` with `DATABASE_URL` from the migrator Secret, an **initContainer** that
  busy-waits on Postgres (`until pg_isready -h postgres; do sleep 1; done` via a postgres-client image or
  a `nc -z postgres 5432` loop). `restartPolicy: Never`.
- [ ] **Step 2: Define the wait contract + RBAC** — app Deployments (Task 9) use a shared initContainer
  `wait-for-migrate` (image `bitnami/kubectl:1.33`) that runs `kubectl wait --for=condition=complete
  --timeout=120s job/migrate`. Define here: ServiceAccount `wait-for-migrate`, a namespaced Role granting
  **`get`, `list`, `watch` on `jobs` in apiGroup `batch`** (`kubectl wait` does get+list+watch — get-only
  is insufficient), and a RoleBinding to that SA only. The migrate Job itself runs as the owner DSN
  (`hookrail`), NOT as a least-priv role.
- [ ] **Step 3: Verify** kubeconform → PASS.
- [ ] **Step 4: Commit** — `feat(k8s): migrate Job + wait-for-migrate init contract`.

### Task 9: App Deployments (api, worker, scheduler, admin, dashboard)

**Files:** Create `deploy/k8s/base/{api,worker,scheduler,admin,dashboard}.yaml`; add to kustomization.

**Interfaces:** each app Deployment = 1 replica, image `hookrail` (command per service), envFrom the
ConfigMap + Secret refs, the `wait-for-migrate` initContainer (Task 8), readiness/liveness probes,
resource requests/limits, `terminationGracePeriodSeconds: 30`.

- [ ] **Step 1: api.yaml (fully-worked template the others follow):**
```yaml
apiVersion: apps/v1
kind: Deployment
metadata: { name: api, namespace: hookrail }
spec:
  replicas: 1
  selector: { matchLabels: { app: api } }
  template:
    metadata: { labels: { app: api } }
    spec:
      serviceAccountName: wait-for-migrate
      initContainers:
        - name: wait-migrate
          image: bitnami/kubectl:1.33
          command: ["kubectl","wait","--for=condition=complete","--timeout=120s","job/migrate"]
      containers:
        - name: api
          image: hookrail
          command: ["hookrail-api"]
          ports: [{ containerPort: 8080 }]
          envFrom: [{ configMapRef: { name: hookrail-config } }]
          env:
            - { name: DATABASE_URL, valueFrom: { secretKeyRef: { name: hookrail-db, key: app_dsn } } }
            - { name: HOOKRAIL_MASTER_KEY, valueFrom: { secretKeyRef: { name: hookrail-app, key: master_key } } }
          readinessProbe: { httpGet: { path: /readyz, port: 8080 }, initialDelaySeconds: 3 }
          livenessProbe:  { httpGet: { path: /healthz, port: 8080 }, initialDelaySeconds: 10 }
          resources: { requests: { cpu: 50m, memory: 64Mi }, limits: { cpu: 500m, memory: 256Mi } }
      terminationGracePeriodSeconds: 30
---
apiVersion: v1
kind: Service
metadata: { name: api, namespace: hookrail }
spec: { selector: { app: api }, ports: [{ port: 8080, targetPort: 8080 }] }
```
- [ ] **Step 2: worker.yaml** — command `["hookrail-worker"]`, no Service, liveness on `:8081`, also waits
  on redis (add a `nc -z redis 6379` init or rely on app retry). Same env (app DSN + master key).
- [ ] **Step 3: scheduler.yaml** — command `["hookrail-scheduler"]`, Service `:8083` (metrics), liveness
  on `:8083`. (Scheduler no longer self-migrates — Task 4.)
- [ ] **Step 4: admin.yaml** — command `["hookrail-admin"]`, Service `:8082`, env adds
  `HOOKRAIL_ADMIN_TOKEN` from `secretKeyRef hookrail-admin/token`, probes on `:8082`.
- [ ] **Step 5: dashboard.yaml** — image `hookrail-dashboard`, command `["hookrail-dashboard"]`, Service
  `:8085`, env: `HOOKRAIL_ADMIN_TOKEN`, `HOOKRAIL_DASHBOARD_PASSWORD`, `HOOKRAIL_DASHBOARD_SESSION_KEY`
  (secretKeyRefs), `HOOKRAIL_PRODUCER_KEY_FILE=/run/secrets/producer_key` mounting Secret
  `hookrail-dashboard-producer-key`. **initContainer also waits for that Secret's key file** (overlay
  adds the wait in ephemeral; in base, mount the Secret). Probes on `:8085`.
- [ ] **Step 6: Verify** kubeconform → PASS (all five Deployments + Services valid).
- [ ] **Step 7: Commit** — `feat(k8s): api/worker/scheduler/admin/dashboard deployments`.

### Task 10: Observability (prometheus, otel-collector, jaeger) + base NetworkPolicy

**Files:** Create `deploy/k8s/base/{prometheus,otel-collector,jaeger}.yaml`, `networkpolicy.yaml`; kustomization.

- [ ] **Step 1:** prometheus Deployment+Service+ConfigMap (scrape `scheduler:8083` etc., port-forward only),
  otel-collector Deployment+Service (config via ConfigMap), jaeger all-in-one Deployment+Service.
- [ ] **Step 2:** `networkpolicy.yaml` — namespace **default-deny ingress** + allow rules:
  dashboard→admin:8082; app pods→postgres:5432/redis:6379; →otel-collector:4318. (cloudflared allow added
  in the prod overlay, Task 16.)
- [ ] **Step 3: Verify** kubeconform → PASS.
- [ ] **Step 4: Commit** — `feat(k8s): observability stack + default-deny base NetworkPolicy`.

**M-E2 gate:** `kubectl kustomize deploy/k8s/base | kubeconform -strict` green; every service + Job +
NetworkPolicy present (not stubs). No Codex pre-gate.

---

# MILESTONE M-E3 — Ephemeral overlay + e2e harness + CI (Tasks 11–14) · Codex pre-gate

VERIFY (per-iter): `kubectl kustomize deploy/k8s/overlays/ephemeral | kubeconform -strict`.
**Gate VERIFY:** `bash scripts/k8s-e2e.sh` exit 0 + **0 leftover k3d clusters**.

### Task 11: Ephemeral overlay (dev secret, NodePort, test-receiver, image tags)

**Files:** Create `deploy/k8s/overlays/ephemeral/{kustomization.yaml,dev-secret.yaml,test-receiver.yaml}` + patches.

- [ ] **Step 1:** `dev-secret.yaml` — throwaway dev values (admin token ≥16, dashboard pw ≥16, ≥32B
  session key, master key, db app/migrator DSNs pointing at the in-cluster postgres). Safe to commit
  (k3d-only). Add a header comment: "DEV ONLY — k3d throwaway".
- [ ] **Step 2:** `kustomization.yaml` — `resources: [../../base, dev-secret.yaml, test-receiver.yaml]`,
  `images:` mapping `hookrail`/`hookrail-dashboard` → local tags (`hookrail:e2e`/`hookrail-dashboard:e2e`),
  patches setting `imagePullPolicy: Never` (so k3d-imported images are used) and tiny resources. **NO
  NodePort** — the harness reaches api/dashboard via `kubectl port-forward` (fold: port-forward goes
  through the API server, not pod ingress, so it isn't blocked by the default-deny NetworkPolicy and needs
  no extra allow rule; NodePort would have required an ingress allow policy that the base default-deny omits).
- [ ] **Step 3:** `test-receiver.yaml` — Deployment+Service `:9090` (ephemeral only).
- [ ] **Step 4: Verify** kubeconform on ephemeral render → PASS.
- [ ] **Step 5: Commit** — `feat(k8s): ephemeral overlay (dev secret, NodePort, test-receiver)`.

### Task 12: Ephemeral key-gen Job (no in-cluster kubectl) + dashboard init-wait

**Why (fold #8):** the app image (`Dockerfile:24-27`) contains only the hookrail binaries + ca-certs — NO
`kubectl` — so a Job that runs `create-producer-key` AND `kubectl patch secret` is impossible. Split: an
in-cluster `dashboard-keygen` Job (hookrail image) only GENERATES the key and prints it to stdout; the
**host-side harness** (Task 13, host has kubectl) reads the Job logs and creates the Secret. No in-cluster
Secret mutation → no RBAC for it (also resolves design fold #5).

**Files:** Create `deploy/k8s/overlays/ephemeral/keygen-job.yaml`; patch dashboard to add the init-wait;
add `keygen-job.yaml` to the ephemeral `resources`.

- [ ] **Step 1:** `keygen-job.yaml` — Job `dashboard-keygen` (hookrail image, owner DSN), command
  `["hookrail-ctl","create-producer-key","-name","dashboard"]`, `restartPolicy: Never`,
  initContainer waits for `job/migrate` (reuse `wait-for-migrate` SA). It prints `producer_key=hk_...` to
  stdout (the harness greps it). It does NOT touch any Secret.
- [ ] **Step 2:** Patch the dashboard Deployment: add an initContainer that blocks until the mounted
  Secret file is non-empty (`until [ -s /run/secrets/producer_key ]; do sleep 2; done`), so the dashboard
  starts only after the harness has created `hookrail-dashboard-producer-key` (fold #6: no hot-reload, so
  the Secret must exist before the main container starts).
- [ ] **Step 3: Verify** kubeconform on ephemeral render → PASS.
- [ ] **Step 4: Commit** — `feat(k8s): ephemeral dashboard-keygen Job + dashboard secret init-wait`.

### Task 13: `scripts/k8s-e2e.sh` (gate-only)

**Files:** Create `scripts/k8s-e2e.sh` (executable); add `k8s-e2e` make target.

- [ ] **Step 1:** Script (teardown trap BEFORE create — M-C5 lesson):
```bash
#!/usr/bin/env bash
set -euo pipefail
CLUSTER=hookrail-e2e; NS=hookrail
trap 'k3d cluster delete "$CLUSTER" >/dev/null 2>&1 || true' EXIT   # armed BEFORE create (M-C5 lesson)
k3d cluster create "$CLUSTER" --wait
docker build -t hookrail:e2e .
docker build -t hookrail-dashboard:e2e --target dashboard .
k3d image import hookrail:e2e hookrail-dashboard:e2e -c "$CLUSTER"
kubectl apply -k deploy/k8s/overlays/ephemeral
kubectl -n "$NS" wait --for=condition=complete --timeout=180s job/migrate
# host-side producer-key provisioning (the app image has no kubectl — fold #8):
kubectl -n "$NS" wait --for=condition=complete --timeout=120s job/dashboard-keygen
KEY=$(kubectl -n "$NS" logs job/dashboard-keygen | /usr/bin/sed -n 's/^producer_key=//p')
[ -n "$KEY" ] || { echo "no producer key from keygen job"; exit 1; }
kubectl -n "$NS" create secret generic hookrail-dashboard-producer-key --from-literal=producer_key="$KEY"
for d in api worker scheduler admin dashboard; do kubectl -n "$NS" rollout status deploy/$d --timeout=120s; done
# e2e flow over port-forward (NOT NodePort — fold #10; port-forward bypasses the default-deny netpol):
kubectl -n "$NS" port-forward svc/api 18080:8080 >/dev/null 2>&1 & PF=$!; sleep 2
ADMIN_PF_PORT=18082; kubectl -n "$NS" port-forward svc/admin $ADMIN_PF_PORT:8082 >/dev/null 2>&1 & PFA=$!; sleep 2
trap 'kill $PF $PFA 2>/dev/null; k3d cluster delete "$CLUSTER" >/dev/null 2>&1 || true' EXIT
TOK=dev-admin-token-001   # matches dev-secret
# create endpoint (→ test-receiver) + subscription via admin:
EP=$(/usr/bin/curl -s -XPOST localhost:$ADMIN_PF_PORT/v1/endpoints -H "Authorization: Bearer $TOK" \
     -H 'Content-Type: application/json' -d '{"url":"http://test-receiver:9090/hook"}' | jq -r .id)
/usr/bin/curl -s -XPOST localhost:$ADMIN_PF_PORT/v1/subscriptions -H "Authorization: Bearer $TOK" \
     -H 'Content-Type: application/json' -d "{\"endpoint_id\":\"$EP\",\"topic_pattern\":\"e2e.*\"}" >/dev/null
# ingest via the public producer API with the provisioned key, then poll the event to succeeded:
EV=$(/usr/bin/curl -s -XPOST localhost:18080/v1/events -H "Authorization: Bearer $KEY" \
     -H 'Content-Type: application/json' -d '{"topic":"e2e.test","payload":{"hi":1}}' | jq -r .event_id)
for i in $(seq 1 30); do
  S=$(/usr/bin/curl -s localhost:18080/v1/events/"$EV" -H "Authorization: Bearer $KEY" | jq -r '.deliveries[0].state')
  [ "$S" = succeeded ] && { echo "k8s-e2e OK"; exit 0; }
  sleep 2
done
echo "delivery never reached succeeded (last=$S)"; exit 1
```
- [ ] **Step 2:** Add `k8s-e2e:` target to the Makefile (`bash scripts/k8s-e2e.sh`).
- [ ] **Step 3: Run locally** — `make k8s-e2e` → exit 0; then `k3d cluster list` shows **no**
  `hookrail-e2e` (0-leak).
- [ ] **Step 4: Commit** — `feat(k8s): gate-only ephemeral e2e harness (scripts/k8s-e2e.sh)`.

### Task 14: CI `k8s-smoke` job (main-only)

**Files:** Modify `.github/workflows/ci.yml` (add `k8s-smoke` job).

- [ ] **Step 1:** Add a `k8s-smoke` job (`if: github.ref=='refs/heads/main'`, `needs:[lint,test]`) that
  installs **both** k3d (`AbsaOSS/k3d-action` or install script) **and a version-pinned `kubectl`**
  (e.g. `azure/setup-kubectl@v4 with: version: v1.33.x`), then `command -v k3d kubectl docker` (fail
  early if any missing), then runs `bash scripts/k8s-e2e.sh` (amd64 local build). Bound with a job-level
  `timeout-minutes`.
- [ ] **Step 2: Verify** — workflow YAML lints (`actionlint` if available, else kubeconform N/A); push is
  the real test (gate watches the run).
- [ ] **Step 3: Commit** — `ci: add main-only k8s-smoke job running the ephemeral e2e harness`.

**M-E3 gate:** Codex pre-gate (exposure/e2e surface — provisioner RBAC, NetworkPolicy, NodePort scoping,
teardown). Gate runs `make k8s-e2e` live: exit 0, succeeded delivery, **0 leftover clusters**. Then push;
watch CI incl. the new `k8s-smoke` job.

---

# MILESTONE M-E4 — Prod overlay + multi-arch images + cutover runbook (Tasks 15–18) · Codex pre-gate

VERIFY (per-iter): `kubectl kustomize deploy/k8s/overlays/prod | kubeconform -strict` + cloudflared config
syntax check. **Gate VERIFY:** kubeconform on prod render + local multi-arch `docker buildx build` (no
push) succeeds. **No live cutover.**

### Task 15: Cross-compile multi-arch Dockerfile + CI both-image push

**Files:** Modify `Dockerfile` (build stages), `.github/workflows/ci.yml` (image job).

- [ ] **Step 1: Dockerfile cross-compile** —
```dockerfile
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build
ARG TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN GOARCH=$TARGETARCH CGO_ENABLED=0 go build -o /out/ ./cmd/...
```
  Apply `GOARCH=$TARGETARCH` to `dashboard-build` too. `web-assets` stage unchanged (arch-independent).
- [ ] **Step 2: Verify locally** — `docker buildx build --platform linux/amd64,linux/arm64 -t t .` and
  `--target dashboard` both succeed (no push).
- [ ] **Step 3: CI image job** — add `platforms: linux/amd64,linux/arm64`; build+push BOTH
  `ghcr.io/mit112/hookrail` and `ghcr.io/mit112/hookrail-dashboard`; tag each `:main` and
  `:sha-${{ github.sha }}`; output the digest.
- [ ] **Step 4: Commit** — `feat(build): cross-compile multi-arch images; push default + dashboard to GHCR`.

### Task 16: Prod overlay (cloudflared + tightened NetworkPolicy + Secret refs + digest pins)

**Files:** Create `deploy/k8s/overlays/prod/{kustomization.yaml,cloudflared.yaml}` + patches +
`prod-networkpolicy.yaml`.

- [ ] **Step 1:** `cloudflared.yaml` — Deployment running `cloudflare/cloudflared` with
  `tunnel --no-autoupdate run`, token from Secret `cloudflared-token` (created attended), a ConfigMap
  ingress mapping ONLY ingest hostname→`http://api:8080` and dashboard hostname→`http://dashboard:8085`.
- [ ] **Step 2:** `prod-networkpolicy.yaml` — allow cloudflared→api:8080 + dashboard:8085 ONLY; keep
  default-deny for everything else (admin reachable only from dashboard).
- [ ] **Step 3:** `kustomization.yaml` — `resources:[../../base, cloudflared.yaml, prod-networkpolicy.yaml]`,
  `images:` pinned to `ghcr.io/mit112/hookrail` + `-dashboard` (digest set at cutover via `kustomize edit
  set image`), prod resource limits, PVC sizes, **no** dev-secret/test-receiver/provisioner-Job (prod
  producer key is an attended Secret). Reference Secrets `hookrail-db`, `hookrail-app`, `hookrail-admin`,
  `hookrail-dashboard-producer-key`, `cloudflared-token` (all created attended).
- [ ] **Step 4: Verify** kubeconform on prod render → PASS; cloudflared ingress config syntax check.
- [ ] **Step 5: Commit** — `feat(k8s): prod overlay (cloudflared tunnel, tightened netpol, attended secrets)`.

### Task 17: Attended cutover runbook (README section)

**Files:** Modify `README.md` (add "Deploy to k3s (Slice E)" section).

- [ ] **Step 1:** Document, step by step: install k3s on the Mac mini; create the namespace; create each
  Secret attended (`kubectl create secret generic hookrail-db --from-literal=app_dsn=… migrator_dsn=…`;
  admin token; dashboard pw + session key; **producer key**: run `hookrail-ctl create-producer-key`,
  store the value; `cloudflared-token`); `kubectl delete job migrate --ignore-not-found` (fold #3);
  pin images (`kustomize edit set image hookrail=…@sha256:<digest> hookrail-dashboard=…@sha256:<digest>`);
  `kubectl apply -k deploy/k8s/overlays/prod`; wait migrate Job + rollouts.
- [ ] **Step 2: Post-cutover smoke (§3.4)** — curl the public ingest hostname (POST event → 202), the
  dashboard hostname (login page → 200), and assert the admin port is NOT reachable from outside.
- [ ] **Step 3: Residual risks** — copy §8 of the design (single-node, tunnel dep, plain Secrets, no
  key hot-reload, unbounded obs retention, no PodSecurity/backup).
- [ ] **Step 4: Commit** — `docs: attended k3s cutover runbook + post-cutover smoke + residual risks`.

### Task 18: Prod render verification test + slice exit

**Files:** Create `scripts/verify-prod-render.sh`; write `.agent/SLICEE_DONE` is the gate's job (not here).

- [ ] **Step 1:** `verify-prod-render.sh` — `kubectl kustomize deploy/k8s/overlays/prod | kubeconform
  -strict` AND assert the render contains cloudflared + the tightened NetworkPolicy + NO test-receiver +
  NO dev-secret (grep guards) — so prod can't accidentally ship dev artifacts.
- [ ] **Step 2: Run** — `bash scripts/verify-prod-render.sh` → exit 0.
- [ ] **Step 3: Commit** — `test(k8s): prod-render verification guards (no dev artifacts, netpol present)`.

**M-E4 gate:** Codex pre-gate (exposure/secrets/tunnel/image-pinning). kubeconform on prod + local
multi-arch build green. **No live cutover.** On green, the gate writes `.agent/SLICEE_DONE`. **Slice E
complete** → Slice F (README v2).

---

## Self-Review

- **Spec coverage:** §2 verification → Tasks 11–14, 18; §3.1 ordering/netpol → Tasks 8,9,10,16; §3.2
  provisioner → Tasks 9,12,16,17; §3.3 roles/migrate/CONCURRENTLY → Tasks 4,5,8; §3.4 smoke → Task 17;
  §4 images → Tasks 15,16,17; §5 four fixes → Tasks 1,2,3,4,5; §7 milestones → all; §8 residuals → Task 17.
- **Placeholder scan:** Task 13's e2e flow is now fully inlined (port-forward + curl create→ingest→poll);
  no `scripts/baseline/k8s-flow.sh` reference remains. No TBD/TODO.
- **Type consistency:** `store.ErrNotFound`, `GetEventStatus`, `getEvent`, role `hookrail_app` (owner
  `hookrail` is migrator), Secret names (`hookrail-db`/`hookrail-app`/`hookrail-admin`/
  `hookrail-dashboard-producer-key`/`cloudflared-token`), image names (`hookrail`/`hookrail-dashboard`),
  migration files `0004_app_role`/`0005_dead_letters_dead_at_concurrently` — used consistently.

---

## Appendix: Codex plan-review (rev1 → rev2) — 14 findings, all folded

8 BLOCKER, 5 MAJOR, 1 MINOR; all verified valid against the real repo, all folded:
1. [BLOCKER] migrator role can't auth before its own migration creates it → owner `hookrail` is the migrator; migrate Job uses owner DSN (Task 4, Task 7).
2. [BLOCKER] `GRANT ALL` ≠ ownership-level DDL → dropped the separate migrator role entirely (Task 4).
3. [BLOCKER] role test used nonexistent `startPG`/`connectAs` + passwordless roles → real `testStore`-style helper, dedicated container, deploy-time `ALTER ROLE … PASSWORD` (Task 4 Step 1).
4. [BLOCKER] down migration `DROP ROLE` poisons the shared testcontainer → down only REVOKEs, never drops; role test uses an isolated container (Task 4 Step 3).
5. [BLOCKER] api 500 test not constructible (concrete `*store.Store`, auth-first 401) → white-box `package api` test calling `getEvent` directly with a closed store (Task 1 Step 4).
6. [MAJOR] `LoadConfig` returns a value not pointer + shared `config.Load` → return `Config{}`; admin-token check in `cmd/hookrail-admin` only (Task 2 Step 3).
7. [MAJOR] design requires all 5 servers' timeouts → worker+scheduler added (Task 3).
8. [BLOCKER] provisioner Job has no kubectl in-image → in-cluster keygen Job prints key; host harness creates the Secret (Task 12, Task 13).
9. [BLOCKER] postgres manifest omitted POSTGRES_DB/USER → all three bootstrap envs set (Task 7).
10. [MAJOR] NodePort vs port-forward inconsistent + netpol blocks → port-forward only, NodePort dropped (Task 11, Task 13).
11. [BLOCKER] Task 13 called nonexistent `scripts/baseline/k8s-flow.sh` → full flow inlined (Task 13).
12. [MAJOR] CI k8s-smoke didn't install kubectl → install+pin kubectl + `command -v` preflight (Task 14).
13. [MAJOR] `kubectl wait` RBAC under-specified → get/list/watch on jobs in batch, SA-scoped (Task 8).
14. [MINOR] Task 5 CONCURRENTLY citation wrong → corrected to the pgx5 driver runStatement/version-tx evidence (Task 5).
