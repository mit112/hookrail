# Hookrail P1 · Slice A — Backend Product Surface — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add the internal Admin/Query API, retention/trimming janitor, and honest per-subscription delivery knobs to the hookrail Go core, so later P1 slices (SDK, dashboard, deploy, chaos, README) have a real backend to build on.

**Architecture:** A new internal `hookrail-admin` binary (`:8082`, bearer-token, defense-in-depth behind a k3s NetworkPolicy) exposes the full CRUD/query surface; a retention janitor runs as a second goroutine inside `hookrail-scheduler` (which gains a `:8083` metrics listener); high-risk edits to the P0 delivery hot path (atomic DLQ replay, a new `cancelled` terminal state, per-delivery `backoff_policy`, best-effort per-worker `rate_limit_rps`) are made TDD-first. Postgres stays the single source of truth; Redis Streams stays dumb.

**Tech Stack:** Go 1.26, pgx/v5 + pgxpool, golang-migrate (embedded), Redis Streams (go-redis/v9), testcontainers-go (PG + Redis, `-tags integration`), kin-openapi for schema conformance, Prometheus client + OTel, RFC-7807 problem+json.

## Global Constraints

- **Module path:** `github.com/mit112/hookrail`. Never emit a literal `<GH_LOGIN>`.
- **Go version floor:** `go 1.26.4` (matches `go.mod`).
- **Master spec:** `projectX/specs/2026-06-09-relay-design.md`. **Slice A design (authority for this plan):** `projectX/docs/superpowers/specs/2026-06-17-hookrail-p1-sliceA-design.md` (rev 3 FINAL). Where this plan and the design disagree, the design wins — raise it as a question, do not silently deviate.
- **Baseline:** branch off `main` @ `e02a955`. P0 is frozen; do not rewrite P0 code except the exact edits these tasks specify.
- **PG is the 202/durability boundary.** Redis loss is only ever a latency event repaired by the sweeper. No task may make Redis authoritative.
- **Fencing is sacred.** `claim_version` is monotonic, never reset, never decremented except `ReleaseClaim`/`DeadLetterExhausted`'s documented un-count. No task may reset or reuse it. Completion/transition CAS always carries `state + claim_version`.
- **No inert knobs, no over-promise.** `rate_limit_rps` is per-worker best-effort and must be documented as NOT a deployment-wide cap (true global cap is P2). `backoff_policy` is fully wired per-delivery.
- **VERIFY (run at every gate):** `make build && make lint && make test && make itest` — plus `make e2e` on M-A5. `make test` is `go test ./... -race -count=1`; `make itest` is `go test -tags integration ./... -race -count=1`.
- **TDD, DRY, YAGNI, frequent commits.** One logical change per commit, imperative mood. Tests must exercise real behavior — never weaken, skip, or delete an existing test to make a milestone pass.
- **Gate routing (design D-A6):** every milestone is gated by **Opus**. M-A4a and M-A4b additionally get a mandatory **Codex gpt-5.5-xhigh pre-gate review** before the Claude gate, because they edit the delivery hot path.

---

## Bridge milestone map (`.agent/MILESTONE` ranges)

This plan is executed through the Reasonix bridge (`projectX/docs/reasonix/hookrail-bridge.md`). Each milestone is a **contiguous, hyphenated** `TASKS: N-M` range — never a bare single number (the M6 lesson: `TASKS: 18` mis-parsed; always hyphenate, e.g. `TASKS: 13-13`). Set `.agent/MILESTONE` per milestone:

| Milestone | Tasks | `.agent/MILESTONE` contents | Gate |
|---|---|---|---|
| **M-A1** | 1–5  | `TASKS: 1-5`\n`VERIFY: make build && make lint && make test && make itest` | Opus |
| **M-A2** | 6–11 | `TASKS: 6-11`\n`VERIFY: make build && make lint && make test && make itest` | Opus |
| **M-A3** | 12–14 | `TASKS: 12-14`\n`VERIFY: make build && make lint && make test && make itest` | Opus |
| **M-A4a** | 15–18 | `TASKS: 15-18`\n`VERIFY: make build && make lint && make test && make itest` | Opus **+ Codex pre-gate** |
| **M-A4b** | 19–21 | `TASKS: 19-21`\n`VERIFY: make build && make lint && make test && make itest` | Opus **+ Codex pre-gate** |
| **M-A5** | 22–25 | `TASKS: 22-25`\n`VERIFY: make build && make lint && make test && make itest && make e2e` | Opus |

Kickoff (example, M-A1):
```bash
cd /Users/mitsheth/Documents/projectX/hookrail
printf 'TASKS: 1-5\nVERIFY: make build && make lint && make test && make itest\n' > .agent/MILESTONE
MAX_ITERS=10 MAX_STEPS=45 BUDGET=30 ./.agent/ralph.sh
```

---

## File structure (what gets created / modified)

**New packages / files**
- `internal/httpx/problem.go` — shared RFC-7807 `Problem`; cursor + limit helpers for keyset pagination.
- `internal/admin/{server.go,auth.go,endpoints.go,subscriptions.go,dlq.go,deliveries.go}` — the admin HTTP surface.
- `cmd/hookrail-admin/main.go` — internal admin binary; refuses boot without `HOOKRAIL_ADMIN_TOKEN`.
- `internal/store/{admin.go,dlq.go,replay.go,browse.go,retention.go}` — admin/query/retention store methods.
- `internal/scheduler/retention.go` — the §5 janitor (ticker, advisory-locked, bounded).
- `internal/backoff/policy_json.go` — `backoff_policy` JSONB validate/parse with panic-guard.
- `internal/worker/limits.go` — `EndpointLimits` loader that pushes per-endpoint MIN(rps) via `Registry.SetRate`.

**Edited P0 files**
- `internal/api/problem.go` → delegate to `httpx.Problem`.
- `internal/config/config.go` → admin token/listen + retention/idem/stream knobs + startup validation.
- `internal/queue/queue.go` → configurable `MaxLen`.
- `internal/api/server.go` + `cmd/hookrail-api/main.go` → wire `IdemTTL` from config.
- `internal/store/ingest.go` → write `endpoint_id`; exclude soft-deleted targets.
- `internal/store/claim.go` → return `backoff_policy`; exclude soft-deleted targets.
- `internal/store/status.go` → surface `attempts_truncated`.
- `internal/domain/state.go` → `cancelled` state + transitions.
- `internal/ratelimit/bucket.go` → `Registry.SetRate` / `Bucket.SetRate`.
- `internal/worker/worker.go` + `cmd/hookrail-worker/main.go` → per-delivery backoff; endpoint limits loader; dead-letter writes `endpoint_id`.
- `internal/scheduler/sweeper.go` is untouched; the janitor is a sibling.
- `cmd/hookrail-scheduler/main.go` → `:8083` metrics listener + janitor goroutine.
- `cmd/hookrail-ctl/main.go` → `retention --once` subcommand.
- `internal/store/migrations/0002_admin_retention.{up,down}.sql` — the schema migration; `internal/store/migrations/0003_deliveries_endpoint_id_notnull.{up,down}.sql` — the deferred NOT NULL.
- `api/openapi.yaml`, `deploy/compose/docker-compose.yml`, `deploy/compose/prometheus.yml`, `test/e2e/`, `README.md`.

---

## Milestone M-A1 — Foundations (Tasks 1–5)

Shared HTTP helper, config knobs wired to real producers, admin auth, the admin binary skeleton, and the scheduler metrics listener. No delivery-core edits yet.

### Task 1: `internal/httpx` shared RFC-7807 problem + pagination helpers

**Files:**
- Create: `internal/httpx/problem.go`
- Create: `internal/httpx/problem_test.go`
- Modify: `internal/api/problem.go` (delegate to `httpx.Problem`)

**Interfaces:**
- Produces: `httpx.Problem(w http.ResponseWriter, status int, title, detail string)`; `httpx.ClampLimit(raw string, def, max int) int`; `httpx.EncodeCursor(key string) string`; `httpx.DecodeCursor(raw string) (string, error)`.

- [ ] **Step 1: Write the failing test**

```go
// internal/httpx/problem_test.go
package httpx

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
)

func TestProblemShape(t *testing.T) {
	w := httptest.NewRecorder()
	Problem(w, 422, "ssrf rejected", "url resolves to a blocked range")
	if got := w.Header().Get("Content-Type"); got != "application/problem+json" {
		t.Fatalf("content-type = %q", got)
	}
	if w.Code != 422 {
		t.Fatalf("status = %d", w.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["title"] != "ssrf rejected" || body["status"].(float64) != 422 {
		t.Fatalf("body = %v", body)
	}
}

func TestClampLimit(t *testing.T) {
	cases := []struct{ in string; want int }{{"", 50}, {"10", 10}, {"500", 200}, {"-3", 50}, {"abc", 50}}
	for _, c := range cases {
		if got := ClampLimit(c.in, 50, 200); got != c.want {
			t.Fatalf("ClampLimit(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestCursorRoundTripAndReject(t *testing.T) {
	enc := EncodeCursor("01HZX...ulid")
	got, err := DecodeCursor(enc)
	if err != nil || got != "01HZX...ulid" {
		t.Fatalf("roundtrip got %q err %v", got, err)
	}
	if _, err := DecodeCursor("!!!not-base64!!!"); err == nil {
		t.Fatal("malformed cursor must error (handler maps to 400)")
	}
	if got, err := DecodeCursor(""); err != nil || got != "" {
		t.Fatalf("empty cursor must decode to empty start, got %q err %v", got, err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/httpx/ -run . -v`
Expected: FAIL — package/functions not defined.

- [ ] **Step 3: Write the implementation**

```go
// internal/httpx/problem.go

// Package httpx holds HTTP helpers shared by the api and admin servers:
// RFC-7807 problem responses and opaque keyset-pagination cursors (design §2.1).
package httpx

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
)

// Problem writes an RFC 7807 problem+json response (master spec §9).
func Problem(w http.ResponseWriter, status int, title, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"type":   "about:blank",
		"title":  title,
		"status": status,
		"detail": detail,
	})
}

// ClampLimit parses a ?limit= value, applying default and hard max (design §2.1).
// Empty, non-numeric, zero, or negative values fall back to def.
func ClampLimit(raw string, def, max int) int {
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return def
	}
	if n > max {
		return max
	}
	return n
}

// EncodeCursor renders an immutable sort key as an opaque, unsigned cursor.
func EncodeCursor(key string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(key))
}

// DecodeCursor reverses EncodeCursor. Empty input means "from the start".
// A non-decodable cursor is an error the caller maps to HTTP 400 (design §2.1).
func DecodeCursor(raw string) (string, error) {
	if raw == "" {
		return "", nil
	}
	b, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return "", errors.New("httpx: malformed cursor")
	}
	return string(b), nil
}
```

- [ ] **Step 4: Delegate the api helper (no behavior change)**

```go
// internal/api/problem.go
package api

import (
	"net/http"

	"github.com/mit112/hookrail/internal/httpx"
)

// problem writes an RFC 7807 problem+json response (§9). Thin alias over
// httpx.Problem so api and admin share one implementation (design §1).
func problem(w http.ResponseWriter, status int, title, detail string) {
	httpx.Problem(w, status, title, detail)
}
```

- [ ] **Step 5: Run tests to verify pass**

Run: `go test ./internal/httpx/ ./internal/api/ -v`
Expected: PASS (api package still compiles; existing api tests unaffected).

- [ ] **Step 6: Commit**

```bash
git add internal/httpx/ internal/api/problem.go
git commit -m "feat(httpx): shared RFC-7807 problem + opaque keyset cursor helpers"
```

### Task 2: Config knobs wired to real producers

**Files:**
- Modify: `internal/config/config.go`
- Create: `internal/config/config_test.go`
- Modify: `internal/queue/queue.go` (configurable `MaxLen`)
- Modify: `internal/api/server.go` + `cmd/hookrail-api/main.go` (wire `IdemTTL` + `q.MaxLen`)
- Modify: `cmd/hookrail-worker/main.go`, `cmd/hookrail-scheduler/main.go` (set `q.MaxLen` after `queue.New`; ctl owns no queue)

**Interfaces:**
- Produces: new `Config` fields `AdminToken string`, `AdminListen string`, `IdemTTL time.Duration`, `StreamMaxLen int64`, `EventPayloadRetention time.Duration`, `AttemptRetention time.Duration`, `RetentionInterval time.Duration`, `RetentionBatch int`, `RetentionTickBudget time.Duration`, `RetentionEnabled bool`; exported `Queue.MaxLen int64`; `api.New(s, q, limits, idemTTL)`.
- Consumes: `httpx` (none); used by Tasks 3–5, 19–21.

- [ ] **Step 1: Write the failing test**

```go
// internal/config/config_test.go
package config

import (
	"testing"
	"time"
)

func TestLoadDefaultsAndOverrides(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://x/y")
	t.Setenv("REDIS_ADDR", "localhost:6379")
	t.Setenv("HOOKRAIL_MASTER_KEY", "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.IdemTTL != 24*time.Hour {
		t.Fatalf("IdemTTL default = %v", c.IdemTTL)
	}
	if c.StreamMaxLen != 100_000 {
		t.Fatalf("StreamMaxLen default = %d", c.StreamMaxLen)
	}
	if !c.RetentionEnabled || c.RetentionInterval != time.Hour {
		t.Fatalf("retention defaults wrong: enabled=%v interval=%v", c.RetentionEnabled, c.RetentionInterval)
	}
}

func TestLoadRejectsNonPositiveRetention(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://x/y")
	t.Setenv("REDIS_ADDR", "localhost:6379")
	t.Setenv("HOOKRAIL_MASTER_KEY", "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff")
	t.Setenv("RETENTION_EVENT_PAYLOAD_DAYS", "0")
	if _, err := Load(); err == nil {
		t.Fatal("zero RETENTION_EVENT_PAYLOAD_DAYS must fail startup (design §3)")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config/ -v`
Expected: FAIL — fields/validation absent.

- [ ] **Step 3: Add the config fields, parsing, and validation**

Add to the `Config` struct in `internal/config/config.go`:

```go
	AdminToken            string        // HOOKRAIL_ADMIN_TOKEN (admin binary refuses boot if empty)
	AdminListen           string        // HOOKRAIL_ADMIN_LISTEN, default ":8082"
	IdemTTL               time.Duration // RETENTION_IDEM_HOURS, default 24h
	StreamMaxLen          int64         // RETENTION_STREAM_MAXLEN, default 100000
	EventPayloadRetention time.Duration // RETENTION_EVENT_PAYLOAD_DAYS, default 30d (job1 + replay-expiry)
	AttemptRetention      time.Duration // RETENTION_ATTEMPT_DAYS, default 30d (job2 + marker)
	RetentionInterval     time.Duration // RETENTION_INTERVAL, default 1h
	RetentionBatch        int           // RETENTION_BATCH, default 5000
	RetentionTickBudget   time.Duration // RETENTION_TICK_BUDGET, default 60s
	RetentionEnabled      bool          // RETENTION_ENABLED, default true
```

Add `import "time"` and `import "strconv"`. In `Load`, after the master-key block and before the `AllowCIDRs` block, insert:

```go
	c.AdminToken = os.Getenv("HOOKRAIL_ADMIN_TOKEN")
	c.AdminListen = envOr("HOOKRAIL_ADMIN_LISTEN", ":8082")

	var perr error
	posDur := func(env string, days bool, def time.Duration) time.Duration {
		raw := os.Getenv(env)
		if raw == "" {
			return def
		}
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 {
			perr = fmt.Errorf("%s must be a positive integer", env)
			return def
		}
		if days {
			return time.Duration(n) * 24 * time.Hour
		}
		return time.Duration(n) * time.Hour
	}
	c.EventPayloadRetention = posDur("RETENTION_EVENT_PAYLOAD_DAYS", true, 30*24*time.Hour)
	c.AttemptRetention = posDur("RETENTION_ATTEMPT_DAYS", true, 30*24*time.Hour)
	c.IdemTTL = posDur("RETENTION_IDEM_HOURS", false, 24*time.Hour)

	c.StreamMaxLen = 100_000
	if v := os.Getenv("RETENTION_STREAM_MAXLEN"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n <= 0 {
			perr = fmt.Errorf("RETENTION_STREAM_MAXLEN must be a positive integer")
		} else {
			c.StreamMaxLen = n
		}
	}
	c.RetentionInterval = time.Hour
	if v := os.Getenv("RETENTION_INTERVAL"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			perr = fmt.Errorf("RETENTION_INTERVAL must be a positive Go duration")
		} else {
			c.RetentionInterval = d
		}
	}
	c.RetentionTickBudget = 60 * time.Second
	if v := os.Getenv("RETENTION_TICK_BUDGET"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			perr = fmt.Errorf("RETENTION_TICK_BUDGET must be a positive Go duration")
		} else {
			c.RetentionTickBudget = d
		}
	}
	c.RetentionBatch = 5000
	if v := os.Getenv("RETENTION_BATCH"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			perr = fmt.Errorf("RETENTION_BATCH must be a positive integer")
		} else {
			c.RetentionBatch = n
		}
	}
	c.RetentionEnabled = envOr("RETENTION_ENABLED", "true") == "true"
	if perr != nil {
		return c, perr
	}
```

- [ ] **Step 4: Make the Redis stream MaxLen configurable**

In `internal/queue/queue.go`, add an exported field and default it in `New`, then use it in `Publish`:

```go
type Queue struct {
	rdb    *redis.Client
	stream string
	group  string
	MaxLen int64 // approximate XADD trim length (config RETENTION_STREAM_MAXLEN); default 100000
}
```
In `New`, set `MaxLen: 100_000` in the returned struct literal. In `Publish` replace `MaxLen: 100_000` with `MaxLen: q.MaxLen`.

- [ ] **Step 5: Wire IdemTTL into the ingest path**

In `internal/api/server.go`: add `idemTTL time.Duration` to `Server`, change `New`:
```go
func New(s *store.Store, q Publisher, limits *ratelimit.Registry, idemTTL time.Duration) *Server {
	return &Server{store: s, queue: q, limits: limits, idemTTL: idemTTL}
}
```
In `postEvent`, replace `IdemTTL: 24 * time.Hour` with `IdemTTL: s.idemTTL`.
In `cmd/hookrail-api/main.go`, change the handler line to wire `IdemTTL`, and set `q.MaxLen` right after `queue.New` (the API is the PRIMARY producer — its ingest XADDs must honor the configured trim length, not the hardcoded 100k):
```go
	q.MaxLen = cfg.StreamMaxLen   // add immediately after the queue.New(...) block
	...
		Handler: api.New(s, q, ratelimit.NewRegistry(500, 1000), cfg.IdemTTL).Handler(),
```
Update both `api.New(...)` call sites in `internal/api/server_test.go` to pass `24*time.Hour` as the final argument (mechanical; do not change any assertions).

- [ ] **Step 6: Set `q.MaxLen` in the remaining queue-owning mains**

`hookrail-ctl` does NOT open a queue — skip it. In `cmd/hookrail-worker/main.go` and `cmd/hookrail-scheduler/main.go`, immediately after the successful `queue.New(...)` assignment add:
```go
	q.MaxLen = cfg.StreamMaxLen
```
(`hookrail-admin` already sets this in Task 4.) Net result: every binary that owns a `*queue.Queue` — api, worker, scheduler, admin — honors `RETENTION_STREAM_MAXLEN`.

- [ ] **Step 7: Run tests + build to verify pass**

Run: `go test ./internal/config/ ./internal/api/ ./internal/queue/ -v && go build ./...`
Expected: PASS + clean build.

- [ ] **Step 8: Commit**

```bash
git add internal/config/ internal/queue/queue.go internal/api/ cmd/
git commit -m "feat(config): admin + retention + idem/stream knobs wired to producers with startup validation"
```

### Task 3: Admin bearer auth (constant-time digest)

**Files:**
- Create: `internal/admin/auth.go`
- Create: `internal/admin/server.go` (minimal `Server` skeleton holding the token digest)
- Create: `internal/admin/auth_test.go`

**Interfaces:**
- Produces: `admin.Server` struct; `(*Server).authAdmin(next http.HandlerFunc) http.HandlerFunc`; package-private `tokenDigest [32]byte`.
- Consumes: `httpx.Problem`.

- [ ] **Step 1: Write the failing test**

```go
// internal/admin/auth_test.go
package admin

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func newAuthOnly(token string) *Server {
	return &Server{tokenDigest: digest(token)}
}

func TestAuthAdmin(t *testing.T) {
	s := newAuthOnly("s3cret-token")
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })
	h := s.authAdmin(ok)

	cases := []struct {
		name, header string
		want         int
	}{
		{"valid", "Bearer s3cret-token", 200},
		{"missing", "", 401},
		{"empty bearer", "Bearer ", 401},
		{"wrong", "Bearer nope", 401},
		{"wrong length", "Bearer s3cret-token-longer", 401},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/v1/endpoints", nil)
			if c.header != "" {
				r.Header.Set("Authorization", c.header)
			}
			w := httptest.NewRecorder()
			h(w, r)
			if w.Code != c.want {
				t.Fatalf("status = %d, want %d", w.Code, c.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/admin/ -run TestAuthAdmin -v`
Expected: FAIL — package/`Server`/`authAdmin`/`digest` undefined.

- [ ] **Step 3: Write the auth implementation**

```go
// internal/admin/auth.go

// Package admin is the internal Admin/Query API (design §2): bearer-authed
// CRUD + query over the same Postgres truth the public ingress writes.
package admin

import (
	"crypto/sha256"
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/mit112/hookrail/internal/httpx"
)

// digest reduces a token to a fixed 32-byte SHA-256 so the constant-time
// compare leaks neither value nor length (design §1.1).
func digest(token string) [32]byte { return sha256.Sum256([]byte(token)) }

// authAdmin requires "Authorization: Bearer <HOOKRAIL_ADMIN_TOKEN>" on every
// /v1/* route. Ops routes (/healthz,/readyz,/metrics) are registered without
// this wrapper (design §1.1). Compares fixed digests in constant time.
func (s *Server) authAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		raw, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || raw == "" {
			httpx.Problem(w, http.StatusUnauthorized, "missing credentials", "Authorization: Bearer <admin token> required")
			return
		}
		got := digest(raw)
		if subtle.ConstantTimeCompare(got[:], s.tokenDigest[:]) != 1 {
			httpx.Problem(w, http.StatusUnauthorized, "invalid credentials", "admin token rejected")
			return
		}
		next(w, r)
	}
}
```

```go
// internal/admin/server.go
package admin

import (
	"github.com/mit112/hookrail/internal/ratelimit"
	"github.com/mit112/hookrail/internal/ssrf"
	"github.com/mit112/hookrail/internal/store"
)

// Publisher is the post-replay best-effort XADD (design §4.1).
type Publisher interface {
	Publish(ctx context.Context, deliveryID string) error
	Ping(ctx context.Context) error
}

// Server holds everything the admin handlers need. Routing is added in Task 4.
type Server struct {
	store       *store.Store
	queue       Publisher
	masterKey   [32]byte
	policy      ssrf.Policy
	limits      *ratelimit.Registry
	tokenDigest [32]byte
}
```
(Add `"context"` to the `server.go` imports.)

- [ ] **Step 4: Run test to verify pass**

Run: `go test ./internal/admin/ -run TestAuthAdmin -v`
Expected: PASS (all 5 sub-cases).

- [ ] **Step 5: Commit**

```bash
git add internal/admin/auth.go internal/admin/server.go internal/admin/auth_test.go
git commit -m "feat(admin): constant-time digest bearer auth middleware"
```

### Task 4: Admin server routing, ops-exempt health/metrics, and the `hookrail-admin` binary

**Files:**
- Modify: `internal/admin/server.go` (add `New`, `Handler`, `readyz`)
- Create: `cmd/hookrail-admin/main.go`
- Create: `internal/admin/server_test.go`

**Interfaces:**
- Produces: `admin.New(s *store.Store, q Publisher, masterKey [32]byte, pol ssrf.Policy, limits *ratelimit.Registry, token string) *Server`; `(*Server).Handler() http.Handler`. Handler wires `/v1/*` behind `authAdmin`; `/healthz`,`/readyz`,`/metrics` are exempt (design §1.1, F17). Handlers for individual routes are stubs returning 501 until Tasks 9–14 fill them.
- Consumes: `cfg.AdminToken`, `cfg.AdminListen` (Task 2).

- [ ] **Step 1: Write the failing test**

```go
// internal/admin/server_test.go
package admin

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mit112/hookrail/internal/ratelimit"
	"github.com/mit112/hookrail/internal/ssrf"
)

func TestOpsRoutesExemptAndV1Guarded(t *testing.T) {
	s := New(nil, nil, [32]byte{}, ssrf.Policy{}, ratelimit.NewRegistry(1, 1), "tok")
	h := s.Handler()

	// health/metrics must NOT require auth (design F17)
	for _, p := range []string{"/healthz", "/metrics"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", p, nil))
		if w.Code == http.StatusUnauthorized {
			t.Fatalf("%s should be exempt from auth, got 401", p)
		}
	}
	// an unauthenticated /v1 route must be 401
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/v1/endpoints", nil))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("/v1/endpoints unauth = %d, want 401", w.Code)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/admin/ -run TestOpsRoutes -v`
Expected: FAIL — `New`/`Handler` undefined.

- [ ] **Step 3: Implement `New`, `Handler`, `readyz`**

Append to `internal/admin/server.go`. After this task `server.go`'s import block must be exactly:
```go
import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/mit112/hookrail/internal/httpx"
	"github.com/mit112/hookrail/internal/ratelimit"
	"github.com/mit112/hookrail/internal/ssrf"
	"github.com/mit112/hookrail/internal/store"
)
```
(`encoding/json` + `httpx` are needed by `writeJSON`/`writePage`/`stub`; `net/http`/`promhttp` by `Handler`/`readyz`.) Then add:

```go
func New(s *store.Store, q Publisher, masterKey [32]byte, pol ssrf.Policy, limits *ratelimit.Registry, token string) *Server {
	return &Server{store: s, queue: q, masterKey: masterKey, policy: pol, limits: limits, tokenDigest: digest(token)}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	// Admin surface (design §2) — every /v1/* route is auth-guarded.
	mux.HandleFunc("POST /v1/endpoints", s.authAdmin(s.createEndpoint))
	mux.HandleFunc("GET /v1/endpoints", s.authAdmin(s.listEndpoints))
	mux.HandleFunc("GET /v1/endpoints/{id}", s.authAdmin(s.getEndpoint))
	mux.HandleFunc("PATCH /v1/endpoints/{id}", s.authAdmin(s.patchEndpoint))
	mux.HandleFunc("DELETE /v1/endpoints/{id}", s.authAdmin(s.deleteEndpoint))
	mux.HandleFunc("POST /v1/endpoints/{id}/rotate-secret", s.authAdmin(s.rotateSecret))
	mux.HandleFunc("POST /v1/subscriptions", s.authAdmin(s.createSubscription))
	mux.HandleFunc("GET /v1/subscriptions", s.authAdmin(s.listSubscriptions))
	mux.HandleFunc("GET /v1/subscriptions/{id}", s.authAdmin(s.getSubscription))
	mux.HandleFunc("PATCH /v1/subscriptions/{id}", s.authAdmin(s.patchSubscription))
	mux.HandleFunc("DELETE /v1/subscriptions/{id}", s.authAdmin(s.deleteSubscription))
	mux.HandleFunc("GET /v1/dlq", s.authAdmin(s.listDLQ))
	mux.HandleFunc("POST /v1/dlq/{delivery_id}/replay", s.authAdmin(s.replayDLQ))
	mux.HandleFunc("GET /v1/deliveries", s.authAdmin(s.listDeliveries))
	mux.HandleFunc("GET /v1/deliveries/{id}", s.authAdmin(s.getDelivery))
	// Ops routes — NOT auth-guarded (design §1.1, F17).
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("GET /readyz", s.readyz)
	mux.Handle("GET /metrics", promhttp.Handler())
	return mux
}

func (s *Server) readyz(w http.ResponseWriter, r *http.Request) {
	if err := s.store.Pool.Ping(r.Context()); err != nil {
		httpx.Problem(w, http.StatusServiceUnavailable, "not ready", "postgres unreachable")
		return
	}
	w.WriteHeader(http.StatusOK)
}
```

- [ ] **Step 4: Add stub handlers (replaced in M-A2/M-A3)**

Create `internal/admin/endpoints.go`, `subscriptions.go`, `dlq.go`, `deliveries.go`, each with the relevant handler methods as stubs that respond 501, e.g. in `endpoints.go`:

```go
package admin

import "net/http"

func (s *Server) createEndpoint(w http.ResponseWriter, r *http.Request)  { stub(w) }
func (s *Server) listEndpoints(w http.ResponseWriter, r *http.Request)   { stub(w) }
func (s *Server) getEndpoint(w http.ResponseWriter, r *http.Request)     { stub(w) }
func (s *Server) patchEndpoint(w http.ResponseWriter, r *http.Request)   { stub(w) }
func (s *Server) deleteEndpoint(w http.ResponseWriter, r *http.Request)  { stub(w) }
func (s *Server) rotateSecret(w http.ResponseWriter, r *http.Request)    { stub(w) }
```
Define `stub` AND the shared response helpers in `server.go` now (they use `encoding/json` + `httpx`, which is why those are in the import block above; Go does not flag unused functions, so defining them before Tasks 8–14 consume them is fine, but it keeps `encoding/json` from being an unused import in M-A1):
```go
func stub(w http.ResponseWriter) {
	httpx.Problem(w, http.StatusNotImplemented, "not implemented", "filled in a later Slice A task")
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writePage emits {"items": [...], "next_cursor": "..."} where next_cursor is
// the opaque keyset cursor for the next page (empty when the page is short).
func writePage(w http.ResponseWriter, items any, n, limit int, lastKey func() string) {
	next := ""
	if n == limit {
		next = httpx.EncodeCursor(lastKey())
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "next_cursor": next})
}
```
Put `createSubscription/listSubscriptions/getSubscription/patchSubscription/deleteSubscription` in `subscriptions.go`, `listDLQ/replayDLQ` in `dlq.go`, `listDeliveries/getDelivery` in `deliveries.go` — all calling `stub(w)` for now.

- [ ] **Step 5: Write the admin binary**

```go
// cmd/hookrail-admin/main.go
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/mit112/hookrail/internal/admin"
	"github.com/mit112/hookrail/internal/config"
	"github.com/mit112/hookrail/internal/obs"
	"github.com/mit112/hookrail/internal/queue"
	"github.com/mit112/hookrail/internal/ratelimit"
	"github.com/mit112/hookrail/internal/ssrf"
	"github.com/mit112/hookrail/internal/store"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cfg, err := config.Load()
	if err != nil {
		slog.Error("config", "err", err)
		os.Exit(1)
	}
	if cfg.AdminToken == "" {
		slog.Error("HOOKRAIL_ADMIN_TOKEN is required; refusing to boot an unauthenticated admin surface")
		os.Exit(1)
	}
	shutdown, err := obs.InitTracing(ctx, "hookrail-admin")
	if err != nil {
		slog.Error("tracing", "err", err)
		os.Exit(1)
	}
	defer func() { _ = shutdown(context.Background()) }()

	s, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		slog.Error("store", "err", err)
		os.Exit(1)
	}
	defer s.Close()
	q, err := queue.New(cfg.RedisAddr, cfg.Stream, cfg.Group)
	if err != nil {
		slog.Error("queue", "err", err)
		os.Exit(1)
	}
	defer q.Close()
	q.MaxLen = cfg.StreamMaxLen

	prefixes, err := cfg.AllowPrefixes()
	if err != nil {
		slog.Error("config", "err", err)
		os.Exit(1)
	}
	pol := ssrf.Policy{AllowHTTP: cfg.AllowHTTP, AllowCIDRs: prefixes}

	srv := &http.Server{
		Addr:              cfg.AdminListen,
		Handler:           admin.New(s, q, cfg.MasterKey, pol, ratelimit.NewRegistry(500, 1000), cfg.AdminToken).Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(shctx)
	}()
	slog.Info("hookrail-admin listening (INTERNAL)", "addr", cfg.AdminListen)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		slog.Error("serve", "err", err)
		os.Exit(1)
	}
}
```

- [ ] **Step 6: Run tests + build to verify pass**

Run: `go test ./internal/admin/ -v && go build ./...`
Expected: PASS + clean build (the binary compiles).

- [ ] **Step 7: Commit**

```bash
git add internal/admin/ cmd/hookrail-admin/
git commit -m "feat(admin): server routing + ops-exempt health/metrics + hookrail-admin binary (refuses boot without token)"
```

### Task 5: Scheduler `:8083` metrics listener

**Files:**
- Modify: `cmd/hookrail-scheduler/main.go`

**Interfaces:**
- Produces: scheduler exposes `/metrics` on `:8083` (design §1, F17) so the M-A4b retention metrics have a scrape target. The janitor goroutine itself lands in M-A4b; this task only adds the listener.

- [ ] **Step 1: Add the metrics listener**

In `cmd/hookrail-scheduler/main.go`, add imports `net/http` and `github.com/prometheus/client_golang/prometheus/promhttp`, then immediately before `sw := &scheduler.Sweeper{...}` insert:

```go
	go func() {
		mux := http.NewServeMux()
		mux.Handle("GET /metrics", promhttp.Handler())
		_ = (&http.Server{Addr: ":8083", Handler: mux, ReadHeaderTimeout: 5 * time.Second}).ListenAndServe()
	}()
```

- [ ] **Step 2: Verify build + existing scheduler tests pass**

Run: `go build ./... && go test ./internal/scheduler/ -v`
Expected: clean build, scheduler tests PASS (sweeper unchanged).

- [ ] **Step 3: Commit**

```bash
git add cmd/hookrail-scheduler/main.go
git commit -m "feat(scheduler): expose :8083 metrics listener for retention metrics"
```

> **M-A1 gate (Opus):** diff Tasks 1–5, run full VERIFY, confirm the admin binary refuses boot without `HOOKRAIL_ADMIN_TOKEN`, ops routes are auth-exempt, and no P0 behavior changed (api/queue tests still green). No `<GH_LOGIN>`.

---

## Milestone M-A2 — Schema migration + CRUD (Tasks 6–11)

The `0002_admin_retention` migration, ingest writing `endpoint_id`, and the endpoint/subscription CRUD + soft-delete + secret-rotation handlers. DELETE here is **soft-delete only**; the delivery-cancellation it triggers is wired in M-A4a (Task 15) once the `cancelled` wiring lands.

### Task 6: Migration `0002_admin_retention` (schema only, no NOT NULL yet)

**Files:**
- Create: `internal/store/migrations/0002_admin_retention.up.sql`
- Create: `internal/store/migrations/0002_admin_retention.down.sql`
- Create: `internal/store/migration0002_test.go`

**Interfaces:**
- Produces: enum value `'cancelled'`; columns `endpoints.deleted_at`, `subscriptions.deleted_at`, `deliveries.endpoint_id`, `deliveries.attempts_truncated_at`, `dead_letters.endpoint_id`; CHECK constraints `chk_rate`, `chk_max_attempts`; the design §5 indexes; backfills. **The `deliveries.endpoint_id SET NOT NULL` is intentionally NOT in this task** — it ships as a separate `0003` migration in Task 7, after ingest writes the column (design §5 sequencing note; golang-migrate never re-runs an applied version).

- [ ] **Step 1: Write the failing migration test**

```go
//go:build integration

package store_test

import (
	"context"
	"testing"
)

// TestMigration0002Roundtrip applies up (via testStore) then down then up,
// proving the literal down SQL is valid and the schema is reversible except
// the enum value (PG cannot drop it — documented).
func TestMigration0002Roundtrip(t *testing.T) {
	s := testStore(t) // already migrated up
	ctx := context.Background()

	// 'cancelled' must be a valid enum value
	if _, err := s.Pool.Exec(ctx, `SELECT 'cancelled'::delivery_state`); err != nil {
		t.Fatalf("cancelled enum value missing: %v", err)
	}
	// new columns exist
	for _, q := range []string{
		`SELECT deleted_at FROM endpoints WHERE false`,
		`SELECT deleted_at FROM subscriptions WHERE false`,
		`SELECT endpoint_id, attempts_truncated_at FROM deliveries WHERE false`,
		`SELECT endpoint_id FROM dead_letters WHERE false`,
	} {
		if _, err := s.Pool.Exec(ctx, q); err != nil {
			t.Fatalf("missing column for %q: %v", q, err)
		}
	}
	// CHECK constraints reject bad values
	if err := s.MigrateDown(); err != nil {
		t.Fatalf("down: %v", err)
	}
	if err := s.Migrate(); err != nil {
		t.Fatalf("re-up: %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -tags integration ./internal/store/ -run TestMigration0002 -v`
Expected: FAIL — enum value / columns absent.

- [ ] **Step 3: Write the up migration (NO `SET NOT NULL` yet)**

```sql
-- internal/store/migrations/0002_admin_retention.up.sql
-- Slice A admin + retention. Plain CREATE INDEX; Slice E adds CONCURRENTLY +
-- lock_timeout (design D-A5/F8). ALTER TYPE ADD VALUE is safe here: the value
-- is only used at runtime, never inside this migration's transaction.
ALTER TYPE delivery_state ADD VALUE IF NOT EXISTS 'cancelled';
ALTER TABLE endpoints     ADD COLUMN deleted_at TIMESTAMPTZ;
ALTER TABLE subscriptions ADD COLUMN deleted_at TIMESTAMPTZ;
ALTER TABLE deliveries    ADD COLUMN endpoint_id TEXT REFERENCES endpoints(id);
ALTER TABLE deliveries    ADD COLUMN attempts_truncated_at TIMESTAMPTZ;
ALTER TABLE dead_letters  ADD COLUMN endpoint_id TEXT REFERENCES endpoints(id);

UPDATE deliveries d SET endpoint_id = s.endpoint_id FROM subscriptions s
  WHERE s.id = d.subscription_id AND d.endpoint_id IS NULL;
UPDATE dead_letters dl SET endpoint_id = d.endpoint_id FROM deliveries d
  WHERE d.id = dl.delivery_id AND dl.endpoint_id IS NULL;

ALTER TABLE subscriptions ADD CONSTRAINT chk_rate         CHECK (rate_limit_rps IS NULL OR rate_limit_rps > 0);
ALTER TABLE subscriptions ADD CONSTRAINT chk_max_attempts CHECK (max_attempts BETWEEN 1 AND 100);

CREATE INDEX idx_deliveries_state_id     ON deliveries (state, id);
CREATE INDEX idx_deliveries_endpoint_id  ON deliveries (endpoint_id, id);
CREATE INDEX idx_deliveries_subscription ON deliveries (subscription_id);
CREATE INDEX idx_subscriptions_endpoint  ON subscriptions (endpoint_id);
CREATE INDEX idx_events_topic_id         ON events (topic, id);
CREATE INDEX idx_events_retain           ON events (created_at) WHERE payload_size > 0;
CREATE INDEX idx_attempts_completed      ON delivery_attempts (completed_at);
CREATE INDEX idx_idem_expires            ON idempotency_keys (expires_at);
CREATE INDEX idx_dead_letters_endpoint   ON dead_letters (endpoint_id, id);
```

- [ ] **Step 4: Write the down migration (literal SQL)**

```sql
-- internal/store/migrations/0002_admin_retention.down.sql
-- 'cancelled' is NOT droppable in PostgreSQL — documented, left in place.
DROP INDEX idx_dead_letters_endpoint;
DROP INDEX idx_idem_expires;
DROP INDEX idx_attempts_completed;
DROP INDEX idx_events_retain;
DROP INDEX idx_events_topic_id;
DROP INDEX idx_subscriptions_endpoint;
DROP INDEX idx_deliveries_subscription;
DROP INDEX idx_deliveries_endpoint_id;
DROP INDEX idx_deliveries_state_id;
ALTER TABLE subscriptions DROP CONSTRAINT chk_max_attempts;
ALTER TABLE subscriptions DROP CONSTRAINT chk_rate;
ALTER TABLE dead_letters  DROP COLUMN endpoint_id;
ALTER TABLE deliveries    DROP COLUMN attempts_truncated_at;
ALTER TABLE deliveries    DROP COLUMN endpoint_id;
ALTER TABLE subscriptions DROP COLUMN deleted_at;
ALTER TABLE endpoints     DROP COLUMN deleted_at;
-- NOTE: 'cancelled' remains in delivery_state (PostgreSQL cannot remove an enum value).
```

> **golang-migrate note:** `ALTER TYPE ... ADD VALUE` cannot run inside a transaction block in some PG versions. golang-migrate runs each migration in a transaction by default. If the up migration fails with *"ALTER TYPE ... ADD VALUE cannot run inside a transaction block"*, add the directive comment `-- +migrate NoTransaction` is **not** supported by golang-migrate's iofs source; instead split the enum addition into its own earlier migration file `0002_admin_retention.up.sql` is kept, and confirm against PG 16 (the test container). PG 12+ allows `ADD VALUE` in a transaction when the value is not used in the same tx, which is our case — verify the test passes; if it does, no split is needed.

- [ ] **Step 5: Run test to verify pass**

Run: `go test -tags integration ./internal/store/ -run TestMigration0002 -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/store/migrations/0002_admin_retention.up.sql internal/store/migrations/0002_admin_retention.down.sql internal/store/migration0002_test.go
git commit -m "feat(store): 0002 migration — cancelled enum, soft-delete + endpoint_id columns, CHECKs, indexes"
```

### Task 7: Ingest writes `endpoint_id`; exclude soft-deleted targets; enforce NOT NULL

**Files:**
- Modify: `internal/store/ingest.go`
- Create: `internal/store/migrations/0003_deliveries_endpoint_id_notnull.up.sql` + `.down.sql`
- Create/extend: `internal/store/ingest_endpointid_test.go`

**Interfaces:**
- Consumes: migration 0002 (`deliveries.endpoint_id`).
- Produces: every new `deliveries` row carries `endpoint_id`; ingest skips subscriptions whose subscription OR endpoint is soft-deleted (design §4.2, F3).

- [ ] **Step 1: Write the failing test**

```go
//go:build integration

package store_test

import (
	"context"
	"testing"

	"github.com/mit112/hookrail/internal/store"
)

func TestIngestPopulatesEndpointID(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	keyID := seedPipeline(t, s, "epid.*")
	res, err := s.IngestEvent(ctx, store.IngestParams{ProducerKeyID: keyID, Topic: "epid.x", Payload: []byte(`{}`)})
	if err != nil || len(res.DeliveryIDs) != 1 {
		t.Fatalf("ingest: %v (%d deliveries)", err, len(res.DeliveryIDs))
	}
	var epID *string
	if err := s.Pool.QueryRow(ctx, `SELECT endpoint_id FROM deliveries WHERE id=$1`, res.DeliveryIDs[0]).Scan(&epID); err != nil {
		t.Fatal(err)
	}
	if epID == nil || *epID == "" {
		t.Fatal("endpoint_id not populated on new delivery")
	}
}

func TestIngestSkipsSoftDeletedTargets(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	keyID := seedPipeline(t, s, "del.*")
	// soft-delete the only subscription
	if _, err := s.Pool.Exec(ctx, `UPDATE subscriptions SET deleted_at = now()`); err != nil {
		t.Fatal(err)
	}
	res, err := s.IngestEvent(ctx, store.IngestParams{ProducerKeyID: keyID, Topic: "del.x", Payload: []byte(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.DeliveryIDs) != 0 {
		t.Fatalf("ingest created %d deliveries for a soft-deleted target, want 0", len(res.DeliveryIDs))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -tags integration ./internal/store/ -run 'TestIngest(Populates|Skips)' -v`
Expected: FAIL — `endpoint_id` not written / soft-deleted rows still matched.

- [ ] **Step 3: Update the subscription select and the delivery insert**

In `internal/store/ingest.go`, change the subscription query to carry `endpoint_id` and exclude soft-deleted targets:

```go
	rows, err := tx.Query(ctx,
		`SELECT s.id, s.topic_pattern, s.endpoint_id
		 FROM subscriptions s
		 JOIN endpoints e ON e.id = s.endpoint_id
		 WHERE s.active AND s.deleted_at IS NULL AND e.deleted_at IS NULL`)
```
Change the local `sub` type and scan:
```go
	type sub struct{ id, pattern, endpointID string }
	var subs []sub
	for rows.Next() {
		var x sub
		if err := rows.Scan(&x.id, &x.pattern, &x.endpointID); err != nil {
			rows.Close()
			return IngestResult{}, err
		}
		subs = append(subs, x)
	}
	rows.Close()
```
Change the delivery insert to include `endpoint_id`:
```go
		did := NewID()
		if _, err := tx.Exec(ctx,
			`INSERT INTO deliveries (id, event_id, subscription_id, endpoint_id) VALUES ($1, $2, $3, $4)`,
			did, eventID, x.id, x.endpointID); err != nil {
			return IngestResult{}, fmt.Errorf("insert delivery: %w", err)
		}
```

- [ ] **Step 4: Add a SEPARATE `0003` migration for the NOT NULL constraint**

`Store.Migrate` (golang-migrate) records each version once and will NOT re-apply an edited, already-applied `0002` (`internal/store/store.go:43`). So the constraint must be its **own new migration**, sequenced after ingest writes `endpoint_id`. Create `internal/store/migrations/0003_deliveries_endpoint_id_notnull.up.sql`:
```sql
-- enforced only after ingest (Task 7) writes endpoint_id on every insert and
-- the 0002 backfill populated existing rows (design §5 sequencing).
ALTER TABLE deliveries ALTER COLUMN endpoint_id SET NOT NULL;
```
and `internal/store/migrations/0003_deliveries_endpoint_id_notnull.down.sql`:
```sql
ALTER TABLE deliveries ALTER COLUMN endpoint_id DROP NOT NULL;
```
Do NOT touch `0002` in this task. (The Task 6 roundtrip test still passes: `testStore` migrates the full set up, so 0003 applies on top of 0002.)

> **Safety basis (be honest — this is NOT a zero-downtime guarantee):** `0003` is safe in Slice A's deploy model because (a) the compose `migrate` one-shot runs to completion **before** any api/worker starts (`deploy/compose/docker-compose.yml`), so on a fresh stack there are no rows and `NOT NULL` is trivially satisfied; (b) for a pre-existing DB, `0002`'s backfill populated all existing rows and the ingest writer (this task) populates new ones. Slice A does **not** support a rolling upgrade with an OLD writer still running during migration — that (online migrations, `CONCURRENTLY`, `lock_timeout`) is explicitly **deferred to Slice E** (design D-A5/F8). If a future operator needs zero-downtime, ship `0002`+writer first and apply `0003` only after verifying `SELECT count(*) FROM deliveries WHERE endpoint_id IS NULL` = 0.

- [ ] **Step 5: Run tests to verify pass**

Run: `go test -tags integration ./internal/store/ -run 'TestIngest|TestMigration0002' -v`
Expected: PASS (ingest writes endpoint_id; NOT NULL holds; migration roundtrip still green).

- [ ] **Step 6: Commit**

```bash
git add internal/store/ingest.go internal/store/migrations/0003_deliveries_endpoint_id_notnull.up.sql internal/store/migrations/0003_deliveries_endpoint_id_notnull.down.sql internal/store/ingest_endpointid_test.go
git commit -m "feat(store): ingest writes endpoint_id, skips soft-deleted targets; 0003 enforces NOT NULL"
```

### Task 8: Endpoint CRUD store methods + admin handlers

**Files:**
- Create: `internal/store/admin.go` (endpoint methods)
- Create: `internal/admin/endpoints.go` (replace stubs with real handlers — list/get/create/patch/delete)
- Create: `internal/admin/testutil_test.go` (integration harness for admin)
- Create: `internal/admin/endpoints_test.go`

**Interfaces:**
- Produces (store): `CreateEndpoint` already exists (seed.go) returning `(id, secret, err)` — reuse it; add `GetEndpoint(ctx, id, includeDeleted bool) (EndpointRow, error)`, `ListEndpoints(ctx, afterID string, limit int, includeDeleted bool) ([]EndpointRow, error)`, `UpdateEndpoint(ctx, id string, url, description *string) error` (partial: nil = leave column unchanged), `SoftDeleteEndpoint(ctx, id string) error`. `EndpointRow{ID, URL, Description string; CreatedAt time.Time; DeletedAt *time.Time}`.
- Produces (handlers): `POST/GET/GET{id}/PATCH/DELETE /v1/endpoints` per design §2 (secret returned once on create; SSRF-validate on create + url PATCH; keyset list).

- [ ] **Step 1: Write the failing test (admin integration harness + endpoint CRUD)**

```go
//go:build integration

package admin_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/mit112/hookrail/internal/admin"
	"github.com/mit112/hookrail/internal/ratelimit"
	"github.com/mit112/hookrail/internal/ssrf"
	"github.com/mit112/hookrail/internal/store"
)

const testToken = "admin-test-token"

var (
	once     sync.Once
	adminDSN string
	dbSeq    atomic.Int64
)

// noQueue is a Publisher that records replay republish calls without Redis.
type noQueue struct{ published []string }

func (n *noQueue) Publish(_ context.Context, id string) error { n.published = append(n.published, id); return nil }
func (n *noQueue) Ping(context.Context) error                 { return nil }

func newServer(t *testing.T) (*admin.Server, *store.Store) {
	t.Helper()
	ctx := context.Background()
	once.Do(func() {
		pgc, err := tcpostgres.Run(ctx, "postgres:16-alpine",
			tcpostgres.WithDatabase("hookrail"), tcpostgres.WithUsername("hookrail"), tcpostgres.WithPassword("hookrail"),
			testcontainers.WithWaitStrategy(wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).WithStartupTimeout(60*time.Second)))
		if err != nil {
			t.Fatalf("pg container: %v", err)
		}
		adminDSN, err = pgc.ConnectionString(ctx, "sslmode=disable")
		if err != nil {
			t.Fatal(err)
		}
	})
	name := fmt.Sprintf("hookrail_t%d", dbSeq.Add(1))
	a, err := pgx.Connect(ctx, adminDSN)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	_ = a.Close(ctx)
	s, err := store.Open(ctx, strings.Replace(adminDSN, "/hookrail?", "/"+name+"?", 1))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	// AllowHTTP so the test receiver URL passes SSRF (dev policy).
	pol := ssrf.Policy{AllowHTTP: true}
	srv := admin.New(s, &noQueue{}, [32]byte{}, pol, ratelimit.NewRegistry(500, 1000), testToken)
	return srv, s
}

// do issues an authed request against the admin handler and returns the recorder.
func do(t *testing.T, srv *admin.Server, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var rdr *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	} else {
		rdr = bytes.NewReader(nil)
	}
	r := httptest.NewRequest(method, path, rdr)
	r.Header.Set("Authorization", "Bearer "+testToken)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)
	return w
}

func TestEndpointCreateGetListSoftDelete(t *testing.T) {
	srv, _ := newServer(t)

	// create — secret returned once, SSRF-validated
	w := do(t, srv, "POST", "/v1/endpoints", map[string]string{"url": "https://example.com/hook", "description": "d"})
	if w.Code != http.StatusCreated {
		t.Fatalf("create = %d body=%s", w.Code, w.Body.String())
	}
	var created struct{ ID, Secret string }
	_ = json.Unmarshal(w.Body.Bytes(), &created)
	if created.ID == "" || !strings.HasPrefix(created.Secret, "whsec_") {
		t.Fatalf("create body = %s", w.Body.String())
	}

	// get
	if g := do(t, srv, "GET", "/v1/endpoints/"+created.ID, nil); g.Code != http.StatusOK {
		t.Fatalf("get = %d", g.Code)
	}

	// soft-delete then excluded from list
	if d := do(t, srv, "DELETE", "/v1/endpoints/"+created.ID, nil); d.Code != http.StatusNoContent {
		t.Fatalf("delete = %d", d.Code)
	}
	l := do(t, srv, "GET", "/v1/endpoints", nil)
	if strings.Contains(l.Body.String(), created.ID) {
		t.Fatal("soft-deleted endpoint still listed without include_deleted")
	}
	li := do(t, srv, "GET", "/v1/endpoints?include_deleted=true", nil)
	if !strings.Contains(li.Body.String(), created.ID) {
		t.Fatal("include_deleted=true should surface the soft-deleted endpoint")
	}
}

func TestEndpointCreateRejectsSSRF(t *testing.T) {
	srv, _ := newServer(t)
	w := do(t, srv, "POST", "/v1/endpoints", map[string]string{"url": "http://169.254.169.254/latest"})
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("SSRF create = %d, want 422", w.Code)
	}
}

func TestEndpointPatchPartial(t *testing.T) {
	srv, _ := newServer(t)
	cw := do(t, srv, "POST", "/v1/endpoints", map[string]string{"url": "https://example.com/a", "description": "orig"})
	var ep struct{ ID string }
	_ = json.Unmarshal(cw.Body.Bytes(), &ep)

	// description-only PATCH must NOT require/clobber the URL
	if p := do(t, srv, "PATCH", "/v1/endpoints/"+ep.ID, map[string]string{"description": "updated"}); p.Code != http.StatusNoContent {
		t.Fatalf("description-only PATCH = %d, want 204", p.Code)
	}
	g := do(t, srv, "GET", "/v1/endpoints/"+ep.ID, nil)
	var got struct{ URL, Description string }
	_ = json.Unmarshal(g.Body.Bytes(), &got)
	if got.URL != "https://example.com/a" || got.Description != "updated" {
		t.Fatalf("after description PATCH: url=%q desc=%q (url must be unchanged)", got.URL, got.Description)
	}
	// url-only PATCH re-validates SSRF and updates only the url
	if p := do(t, srv, "PATCH", "/v1/endpoints/"+ep.ID, map[string]string{"url": "https://example.com/b"}); p.Code != http.StatusNoContent {
		t.Fatalf("url-only PATCH = %d, want 204", p.Code)
	}
	if bad := do(t, srv, "PATCH", "/v1/endpoints/"+ep.ID, map[string]string{"url": "http://169.254.169.254/x"}); bad.Code != http.StatusUnprocessableEntity {
		t.Fatalf("SSRF url PATCH = %d, want 422", bad.Code)
	}
}
```
`json` is already imported by the test file (used for create). The `do` helper marshals the map body.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -tags integration ./internal/admin/ -run TestEndpoint -v`
Expected: FAIL — stub handlers return 501.

- [ ] **Step 3: Write the endpoint store methods**

```go
// internal/store/admin.go
package store

import (
	"context"
	"time"
)

type EndpointRow struct {
	ID          string     `json:"id"`
	URL         string     `json:"url"`
	Description string     `json:"description"`
	CreatedAt   time.Time  `json:"created_at"`
	DeletedAt   *time.Time `json:"deleted_at,omitempty"`
}

func (s *Store) GetEndpoint(ctx context.Context, id string, includeDeleted bool) (EndpointRow, error) {
	q := `SELECT id, url, description, created_at, deleted_at FROM endpoints WHERE id=$1`
	if !includeDeleted {
		q += ` AND deleted_at IS NULL`
	}
	var e EndpointRow
	err := s.Pool.QueryRow(ctx, q, id).Scan(&e.ID, &e.URL, &e.Description, &e.CreatedAt, &e.DeletedAt)
	return e, err
}

// ListEndpoints is keyset-paginated on the immutable id (design §2.1): DESC,
// id < cursor. afterID == "" starts at the newest — expressed as
// `($1 = '' OR id < $1)` so it is collation-independent (no max-char sentinel).
func (s *Store) ListEndpoints(ctx context.Context, afterID string, limit int, includeDeleted bool) ([]EndpointRow, error) {
	q := `SELECT id, url, description, created_at, deleted_at FROM endpoints WHERE ($1 = '' OR id < $1)`
	if !includeDeleted {
		q += ` AND deleted_at IS NULL`
	}
	q += ` ORDER BY id DESC LIMIT $2`
	rows, err := s.Pool.Query(ctx, q, afterID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []EndpointRow
	for rows.Next() {
		var e EndpointRow
		if err := rows.Scan(&e.ID, &e.URL, &e.Description, &e.CreatedAt, &e.DeletedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// UpdateEndpoint applies a PARTIAL update: nil pointer = leave the column
// unchanged (COALESCE), so a description-only PATCH never clobbers the URL and
// vice versa.
func (s *Store) UpdateEndpoint(ctx context.Context, id string, url, description *string) error {
	ct, err := s.Pool.Exec(ctx,
		`UPDATE endpoints SET
		   url = COALESCE($2, url),
		   description = COALESCE($3, description)
		 WHERE id=$1 AND deleted_at IS NULL`, id, url, description)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// SoftDeleteEndpoint marks the endpoint and its subscriptions deleted in one tx.
// Delivery cancellation is added to this method in M-A4a (Task 15).
func (s *Store) SoftDeleteEndpoint(ctx context.Context, id string) error {
	tx, err := s.Pool.BeginTx(ctx, pgxTxRW())
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	ct, err := tx.Exec(ctx, `UPDATE endpoints SET deleted_at=now() WHERE id=$1 AND deleted_at IS NULL`, id)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	if _, err := tx.Exec(ctx,
		`UPDATE subscriptions SET deleted_at=now() WHERE endpoint_id=$1 AND deleted_at IS NULL`, id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
```
Add a shared sentinel and tx helper in `internal/store/store.go`:
```go
// ErrNotFound: admin lookups/updates that match no live row (handler → 404).
var ErrNotFound = errors.New("store: not found")

func pgxTxRW() pgx.TxOptions { return pgx.TxOptions{} }
```
(Add `"github.com/jackc/pgx/v5"` to `store.go` imports if not already present.)

- [ ] **Step 4: Write the endpoint handlers**

Replace the stub bodies in `internal/admin/endpoints.go`:

```go
package admin

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/mit112/hookrail/internal/httpx"
	"github.com/mit112/hookrail/internal/store"
)

type createEndpointReq struct {
	URL         string `json:"url"`
	Description string `json:"description"`
}

func (s *Server) createEndpoint(w http.ResponseWriter, r *http.Request) {
	var req createEndpointReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.URL == "" {
		httpx.Problem(w, http.StatusBadRequest, "invalid body", `expected {"url": string, "description": string}`)
		return
	}
	if err := s.policy.CheckURLResolved(r.Context(), req.URL); err != nil {
		httpx.Problem(w, http.StatusUnprocessableEntity, "url rejected", "url failed SSRF policy")
		return
	}
	id, secret, err := s.store.CreateEndpoint(r.Context(), s.masterKey, req.URL, req.Description)
	if err != nil {
		httpx.Problem(w, http.StatusServiceUnavailable, "create failed", "could not persist endpoint")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store") // secret in body (design §1.1)
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]string{"id": id, "url": req.URL, "secret": secret})
}

func (s *Server) getEndpoint(w http.ResponseWriter, r *http.Request) {
	e, err := s.store.GetEndpoint(r.Context(), r.PathValue("id"), r.URL.Query().Get("include_deleted") == "true")
	if err != nil {
		httpx.Problem(w, http.StatusNotFound, "not found", "no endpoint with that id")
		return
	}
	writeJSON(w, http.StatusOK, e)
}

func (s *Server) listEndpoints(w http.ResponseWriter, r *http.Request) {
	cursor, err := httpx.DecodeCursor(r.URL.Query().Get("cursor"))
	if err != nil {
		httpx.Problem(w, http.StatusBadRequest, "bad cursor", "cursor is not decodable")
		return
	}
	limit := httpx.ClampLimit(r.URL.Query().Get("limit"), 50, 200)
	rows, err := s.store.ListEndpoints(r.Context(), cursor, limit, r.URL.Query().Get("include_deleted") == "true")
	if err != nil {
		httpx.Problem(w, http.StatusServiceUnavailable, "list failed", "query error")
		return
	}
	writePage(w, rows, len(rows), limit, func() string {
		if len(rows) == 0 {
			return ""
		}
		return rows[len(rows)-1].ID
	})
}

type patchEndpointReq struct {
	URL         *string `json:"url"`
	Description *string `json:"description"`
}

func (s *Server) patchEndpoint(w http.ResponseWriter, r *http.Request) {
	var req patchEndpointReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Problem(w, http.StatusBadRequest, "invalid body", `expected {"url"?: string, "description"?: string}`)
		return
	}
	if req.URL != nil { // SSRF-validate ONLY when the URL is being changed
		if *req.URL == "" {
			httpx.Problem(w, http.StatusBadRequest, "invalid body", "url must be non-empty when present")
			return
		}
		if err := s.policy.CheckURLResolved(r.Context(), *req.URL); err != nil {
			httpx.Problem(w, http.StatusUnprocessableEntity, "url rejected", "url failed SSRF policy")
			return
		}
	}
	if err := s.store.UpdateEndpoint(r.Context(), r.PathValue("id"), req.URL, req.Description); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			httpx.Problem(w, http.StatusNotFound, "not found", "no live endpoint with that id")
			return
		}
		httpx.Problem(w, http.StatusServiceUnavailable, "update failed", "query error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) deleteEndpoint(w http.ResponseWriter, r *http.Request) {
	if err := s.store.SoftDeleteEndpoint(r.Context(), r.PathValue("id")); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			httpx.Problem(w, http.StatusNotFound, "not found", "no live endpoint with that id")
			return
		}
		httpx.Problem(w, http.StatusServiceUnavailable, "delete failed", "query error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
```
(`writeJSON` and `writePage` are already defined in `server.go` from Task 4 — reuse them; do not redefine.)

- [ ] **Step 5: Run tests to verify pass**

Run: `go test -tags integration ./internal/admin/ -run TestEndpoint -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/store/admin.go internal/store/store.go internal/admin/endpoints.go internal/admin/server.go internal/admin/testutil_test.go internal/admin/endpoints_test.go
git commit -m "feat(admin): endpoint CRUD + soft-delete + keyset list + SSRF validation"
```

### Task 9: Subscription CRUD store methods + admin handlers

**Files:**
- Modify: `internal/store/admin.go` (subscription methods)
- Modify: `internal/admin/subscriptions.go` (real handlers)
- Create: `internal/admin/subscriptions_test.go`

**Interfaces:**
- Produces (store): `CreateSubscriptionFull(ctx, p SubInput) (string, error)`, `GetSubscription`, `ListSubscriptions(ctx, endpointID, afterID string, limit int) ([]SubscriptionRow, error)`, `UpdateSubscription(ctx, id string, active *bool, maxAttempts *int, rps *float64, backoff []byte) error` (rejected if `deleted_at` set — F3), `SoftDeleteSubscription(ctx, id string) error`. `SubInput{TopicPattern, EndpointID string; MaxAttempts int; RateLimitRPS *float64; BackoffPolicy []byte}`.
- Consumes: `backoff.Validate` is NOT yet available (M-A4a Task 16). In this task, store `backoff_policy` as raw JSON **without** semantic validation; the CHECK constraints (`chk_rate`, `chk_max_attempts`) enforce numeric bounds and surface as 422. Backoff JSON validation is added in Task 16 when the handler gains `backoff.Validate`.

- [ ] **Step 1: Write the failing test**

```go
//go:build integration

package admin_test

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestSubscriptionLifecycle(t *testing.T) {
	srv, _ := newServer(t)
	// need an endpoint first
	ew := do(t, srv, "POST", "/v1/endpoints", map[string]string{"url": "https://example.com/h"})
	var ep struct{ ID string }
	_ = json.Unmarshal(ew.Body.Bytes(), &ep)

	// create subscription
	w := do(t, srv, "POST", "/v1/subscriptions", map[string]any{
		"topic_pattern": "orders.*", "endpoint_id": ep.ID, "max_attempts": 5,
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("create sub = %d body=%s", w.Code, w.Body.String())
	}
	var sub struct{ ID string }
	_ = json.Unmarshal(w.Body.Bytes(), &sub)

	// reject out-of-range max_attempts (CHECK → 422)
	bad := do(t, srv, "POST", "/v1/subscriptions", map[string]any{
		"topic_pattern": "x.*", "endpoint_id": ep.ID, "max_attempts": 999,
	})
	if bad.Code != http.StatusUnprocessableEntity {
		t.Fatalf("max_attempts=999 = %d, want 422", bad.Code)
	}

	// pause via PATCH active=false
	if p := do(t, srv, "PATCH", "/v1/subscriptions/"+sub.ID, map[string]any{"active": false}); p.Code != http.StatusNoContent {
		t.Fatalf("pause = %d", p.Code)
	}

	// soft-delete then PATCH must be rejected (F3: cannot resume a deleted sub)
	if d := do(t, srv, "DELETE", "/v1/subscriptions/"+sub.ID, nil); d.Code != http.StatusNoContent {
		t.Fatalf("delete = %d", d.Code)
	}
	if p := do(t, srv, "PATCH", "/v1/subscriptions/"+sub.ID, map[string]any{"active": true}); p.Code != http.StatusConflict {
		t.Fatalf("resume-deleted = %d, want 409", p.Code)
	}
}

func TestCreateSubAgainstDeletedEndpointRejected(t *testing.T) {
	srv, _ := newServer(t)
	ew := do(t, srv, "POST", "/v1/endpoints", map[string]string{"url": "https://example.com/h"})
	var ep struct{ ID string }
	_ = json.Unmarshal(ew.Body.Bytes(), &ep)
	_ = do(t, srv, "DELETE", "/v1/endpoints/"+ep.ID, nil)
	w := do(t, srv, "POST", "/v1/subscriptions", map[string]any{"topic_pattern": "a.*", "endpoint_id": ep.ID, "max_attempts": 3})
	if w.Code != http.StatusConflict && w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("create against deleted endpoint = %d, want 409/422", w.Code)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -tags integration ./internal/admin/ -run 'TestSubscription|TestCreateSub' -v`
Expected: FAIL — stub handlers.

- [ ] **Step 3: Write the subscription store methods**

Add `"encoding/json"` to `internal/store/admin.go`'s imports (for `json.RawMessage` on `SubscriptionRow`). Then append:

```go
// append to internal/store/admin.go
type SubInput struct {
	TopicPattern  string
	EndpointID    string
	MaxAttempts   int
	RateLimitRPS  *float64
	BackoffPolicy []byte // raw JSONB or nil
}

type SubscriptionRow struct {
	ID            string          `json:"id"`
	TopicPattern  string          `json:"topic_pattern"`
	EndpointID    string          `json:"endpoint_id"`
	MaxAttempts   int             `json:"max_attempts"`
	RateLimitRPS  *float64        `json:"rate_limit_rps,omitempty"`
	BackoffPolicy json.RawMessage `json:"backoff_policy,omitempty"` // raw JSON object, NOT base64 — pgx scans jsonb into RawMessage
	Active        bool            `json:"active"`
	DeletedAt     *time.Time      `json:"deleted_at,omitempty"`
}

// CreateSubscriptionFull inserts a subscription against a LIVE endpoint only.
// A deleted/absent endpoint yields ErrConflict (handler → 409). CHECK
// constraints reject bad max_attempts/rate (handler maps the PG error → 422).
func (s *Store) CreateSubscriptionFull(ctx context.Context, p SubInput) (string, error) {
	id := NewID()
	ct, err := s.Pool.Exec(ctx,
		`INSERT INTO subscriptions (id, topic_pattern, endpoint_id, max_attempts, rate_limit_rps, backoff_policy)
		 SELECT $1, $2, $3, $4, $5, $6
		 WHERE EXISTS (SELECT 1 FROM endpoints WHERE id=$3 AND deleted_at IS NULL)`,
		id, p.TopicPattern, p.EndpointID, p.MaxAttempts, p.RateLimitRPS, nullJSON(p.BackoffPolicy))
	if err != nil {
		return "", err
	}
	if ct.RowsAffected() == 0 {
		return "", ErrConflict
	}
	return id, nil
}

func (s *Store) GetSubscription(ctx context.Context, id string, includeDeleted bool) (SubscriptionRow, error) {
	q := `SELECT id, topic_pattern, endpoint_id, max_attempts, rate_limit_rps, backoff_policy, active, deleted_at
	      FROM subscriptions WHERE id=$1`
	if !includeDeleted {
		q += ` AND deleted_at IS NULL`
	}
	var r SubscriptionRow
	err := s.Pool.QueryRow(ctx, q, id).Scan(&r.ID, &r.TopicPattern, &r.EndpointID, &r.MaxAttempts,
		&r.RateLimitRPS, &r.BackoffPolicy, &r.Active, &r.DeletedAt)
	return r, err
}

func (s *Store) ListSubscriptions(ctx context.Context, endpointID, afterID string, limit int) ([]SubscriptionRow, error) {
	args := []any{afterID, limit}
	q := `SELECT id, topic_pattern, endpoint_id, max_attempts, rate_limit_rps, backoff_policy, active, deleted_at
	      FROM subscriptions WHERE ($1 = '' OR id < $1) AND deleted_at IS NULL`
	if endpointID != "" {
		q += ` AND endpoint_id = $3`
		args = append(args, endpointID)
	}
	q += ` ORDER BY id DESC LIMIT $2`
	rows, err := s.Pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SubscriptionRow
	for rows.Next() {
		var r SubscriptionRow
		if err := rows.Scan(&r.ID, &r.TopicPattern, &r.EndpointID, &r.MaxAttempts,
			&r.RateLimitRPS, &r.BackoffPolicy, &r.Active, &r.DeletedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// UpdateSubscription applies partial updates. A soft-deleted subscription is
// immutable (F3): 0 rows affected → ErrNotFound, which the handler maps to 409
// when the row exists-but-deleted, else 404.
func (s *Store) UpdateSubscription(ctx context.Context, id string, active *bool, maxAttempts *int, rps *float64, backoff []byte, setBackoff bool) error {
	ct, err := s.Pool.Exec(ctx,
		`UPDATE subscriptions SET
		   active = COALESCE($2, active),
		   max_attempts = COALESCE($3, max_attempts),
		   rate_limit_rps = CASE WHEN $4 THEN $5 ELSE rate_limit_rps END,
		   backoff_policy = CASE WHEN $6 THEN $7 ELSE backoff_policy END
		 WHERE id=$1 AND deleted_at IS NULL`,
		id, active, maxAttempts, rps != nil, rps, setBackoff, nullJSON(backoff))
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) SoftDeleteSubscription(ctx context.Context, id string) error {
	ct, err := s.Pool.Exec(ctx, `UPDATE subscriptions SET deleted_at=now() WHERE id=$1 AND deleted_at IS NULL`, id)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) subscriptionExists(ctx context.Context, id string) (bool, error) {
	var ok bool
	err := s.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM subscriptions WHERE id=$1)`, id).Scan(&ok)
	return ok, err
}

func nullJSON(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	return b
}
```
Add to `store.go`: `var ErrConflict = errors.New("store: conflicting state")`.

- [ ] **Step 4: Write the subscription handlers**

Replace stubs in `internal/admin/subscriptions.go`:

```go
package admin

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/mit112/hookrail/internal/httpx"
	"github.com/mit112/hookrail/internal/store"
)

type createSubReq struct {
	TopicPattern  string          `json:"topic_pattern"`
	EndpointID    string          `json:"endpoint_id"`
	MaxAttempts   int             `json:"max_attempts"`
	RateLimitRPS  *float64        `json:"rate_limit_rps"`
	BackoffPolicy json.RawMessage `json:"backoff_policy"`
}

// isCheckViolation reports a PG CHECK/constraint failure → HTTP 422.
func isCheckViolation(err error) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && (pg.Code == "23514" || pg.Code == "23502" || pg.Code == "23503")
}

func (s *Server) createSubscription(w http.ResponseWriter, r *http.Request) {
	var req createSubReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.TopicPattern == "" || req.EndpointID == "" {
		httpx.Problem(w, http.StatusBadRequest, "invalid body", "topic_pattern and endpoint_id are required")
		return
	}
	if req.MaxAttempts == 0 {
		req.MaxAttempts = 8
	}
	id, err := s.store.CreateSubscriptionFull(r.Context(), store.SubInput{
		TopicPattern: req.TopicPattern, EndpointID: req.EndpointID,
		MaxAttempts: req.MaxAttempts, RateLimitRPS: req.RateLimitRPS, BackoffPolicy: req.BackoffPolicy,
	})
	switch {
	case errors.Is(err, store.ErrConflict):
		httpx.Problem(w, http.StatusConflict, "endpoint not available", "endpoint is missing or soft-deleted")
		return
	case isCheckViolation(err):
		httpx.Problem(w, http.StatusUnprocessableEntity, "invalid subscription", "max_attempts must be 1..100 and rate_limit_rps > 0")
		return
	case err != nil:
		httpx.Problem(w, http.StatusServiceUnavailable, "create failed", "query error")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"id": id})
}

func (s *Server) getSubscription(w http.ResponseWriter, r *http.Request) {
	row, err := s.store.GetSubscription(r.Context(), r.PathValue("id"), r.URL.Query().Get("include_deleted") == "true")
	if err != nil {
		httpx.Problem(w, http.StatusNotFound, "not found", "no subscription with that id")
		return
	}
	writeJSON(w, http.StatusOK, row)
}

func (s *Server) listSubscriptions(w http.ResponseWriter, r *http.Request) {
	cursor, err := httpx.DecodeCursor(r.URL.Query().Get("cursor"))
	if err != nil {
		httpx.Problem(w, http.StatusBadRequest, "bad cursor", "cursor is not decodable")
		return
	}
	limit := httpx.ClampLimit(r.URL.Query().Get("limit"), 50, 200)
	rows, err := s.store.ListSubscriptions(r.Context(), r.URL.Query().Get("endpoint_id"), cursor, limit)
	if err != nil {
		httpx.Problem(w, http.StatusServiceUnavailable, "list failed", "query error")
		return
	}
	writePage(w, rows, len(rows), limit, func() string {
		if len(rows) == 0 {
			return ""
		}
		return rows[len(rows)-1].ID
	})
}

type patchSubReq struct {
	Active        *bool           `json:"active"`
	MaxAttempts   *int            `json:"max_attempts"`
	RateLimitRPS  *float64        `json:"rate_limit_rps"`
	BackoffPolicy json.RawMessage `json:"backoff_policy"`
}

func (s *Server) patchSubscription(w http.ResponseWriter, r *http.Request) {
	var req patchSubReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Problem(w, http.StatusBadRequest, "invalid body", "malformed patch")
		return
	}
	id := r.PathValue("id")
	err := s.store.UpdateSubscription(r.Context(), id, req.Active, req.MaxAttempts, req.RateLimitRPS,
		req.BackoffPolicy, len(req.BackoffPolicy) > 0)
	switch {
	case errors.Is(err, store.ErrNotFound):
		// distinguish 404 (no such id) from 409 (exists-but-deleted, F3)
		if ok, _ := s.store.SubscriptionExists(r.Context(), id); ok {
			httpx.Problem(w, http.StatusConflict, "subscription deleted", "cannot modify a soft-deleted subscription")
			return
		}
		httpx.Problem(w, http.StatusNotFound, "not found", "no subscription with that id")
		return
	case isCheckViolation(err):
		httpx.Problem(w, http.StatusUnprocessableEntity, "invalid subscription", "max_attempts must be 1..100 and rate_limit_rps > 0")
		return
	case err != nil:
		httpx.Problem(w, http.StatusServiceUnavailable, "update failed", "query error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
	_ = strings.TrimSpace("")
}

func (s *Server) deleteSubscription(w http.ResponseWriter, r *http.Request) {
	if err := s.store.SoftDeleteSubscription(r.Context(), r.PathValue("id")); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			httpx.Problem(w, http.StatusNotFound, "not found", "no live subscription with that id")
			return
		}
		httpx.Problem(w, http.StatusServiceUnavailable, "delete failed", "query error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
```
Export the existence helper on the store (rename `subscriptionExists` → `SubscriptionExists`).

- [ ] **Step 5: Run tests to verify pass**

Run: `go test -tags integration ./internal/admin/ -run 'TestSubscription|TestCreateSub' -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/store/admin.go internal/store/store.go internal/admin/subscriptions.go
git commit -m "feat(admin): subscription CRUD + soft-delete + deleted-sub immutability (F3)"
```

### Task 10: Secret rotation

**Files:**
- Modify: `internal/store/admin.go` (`RotateEndpointSecret`)
- Modify: `internal/admin/endpoints.go` (`rotateSecret` handler)
- Create: `internal/admin/rotate_test.go`

**Interfaces:**
- Produces: `RotateEndpointSecret(ctx, masterKey [32]byte, id string) (secret string, err error)` — generates a new `whsec_` secret, encrypts, updates the live endpoint, returns plaintext once; `ErrNotFound` if absent/deleted. Handler returns 200 + `{secret}` with `Cache-Control: no-store`. Cutover is eventual, bounded by the worker HTTP attempt timeout (design §4.3, F6) — documented in the OpenAPI note (M-A5).

- [ ] **Step 1: Write the failing test**

```go
//go:build integration

package admin_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestRotateSecret(t *testing.T) {
	srv, _ := newServer(t)
	ew := do(t, srv, "POST", "/v1/endpoints", map[string]string{"url": "https://example.com/h"})
	var ep struct{ ID, Secret string }
	_ = json.Unmarshal(ew.Body.Bytes(), &ep)

	w := do(t, srv, "POST", "/v1/endpoints/"+ep.ID+"/rotate-secret", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("rotate = %d body=%s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
	var out struct{ Secret string }
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if !strings.HasPrefix(out.Secret, "whsec_") || out.Secret == ep.Secret {
		t.Fatalf("rotated secret invalid or unchanged: %q", out.Secret)
	}
	// rotating an unknown endpoint → 404
	if nf := do(t, srv, "POST", "/v1/endpoints/nope/rotate-secret", nil); nf.Code != http.StatusNotFound {
		t.Fatalf("rotate unknown = %d, want 404", nf.Code)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -tags integration ./internal/admin/ -run TestRotateSecret -v`
Expected: FAIL — stub handler.

- [ ] **Step 3: Write the store rotation method**

Add these imports to `internal/store/admin.go` (mirroring `seed.go`): `"crypto/rand"`, `"encoding/hex"`, and `hcrypto "github.com/mit112/hookrail/internal/crypto"`. Then append:

```go
// append to internal/store/admin.go

func (s *Store) RotateEndpointSecret(ctx context.Context, masterKey [32]byte, id string) (string, error) {
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	secret := "whsec_" + hex.EncodeToString(raw)
	box, err := hcrypto.Encrypt(masterKey, []byte(secret))
	if err != nil {
		return "", err
	}
	ct, err := s.Pool.Exec(ctx,
		`UPDATE endpoints SET secret_ciphertext=$2 WHERE id=$1 AND deleted_at IS NULL`, id, box)
	if err != nil {
		return "", err
	}
	if ct.RowsAffected() == 0 {
		return "", ErrNotFound
	}
	return secret, nil
}
```

- [ ] **Step 4: Write the rotate handler**

Replace the `rotateSecret` stub in `internal/admin/endpoints.go`:
```go
func (s *Server) rotateSecret(w http.ResponseWriter, r *http.Request) {
	secret, err := s.store.RotateEndpointSecret(r.Context(), s.masterKey, r.PathValue("id"))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			httpx.Problem(w, http.StatusNotFound, "not found", "no live endpoint with that id")
			return
		}
		httpx.Problem(w, http.StatusServiceUnavailable, "rotate failed", "query error")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]string{"secret": secret})
}
```

- [ ] **Step 5: Run tests to verify pass**

Run: `go test -tags integration ./internal/admin/ -run TestRotateSecret -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/store/admin.go internal/admin/endpoints.go internal/admin/rotate_test.go
git commit -m "feat(admin): endpoint secret rotation (one-time secret, no-store, eventual cutover)"
```

### Task 11: Auth coverage + ops-reachability integration test

**Files:**
- Create: `internal/admin/auth_integration_test.go`

**Interfaces:**
- Verifies the milestone invariant: every `/v1/*` route requires the token; `/healthz`,`/readyz`,`/metrics` are reachable without it (design §1.1, F17). This is the M-A2 regression guard before the delivery-core milestones build on the surface.

- [ ] **Step 1: Write the test**

```go
//go:build integration

package admin_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestEveryV1RouteRequiresToken(t *testing.T) {
	srv, _ := newServer(t)
	h := srv.Handler()
	routes := []struct{ method, path string }{
		{"POST", "/v1/endpoints"}, {"GET", "/v1/endpoints"}, {"GET", "/v1/endpoints/x"},
		{"PATCH", "/v1/endpoints/x"}, {"DELETE", "/v1/endpoints/x"}, {"POST", "/v1/endpoints/x/rotate-secret"},
		{"POST", "/v1/subscriptions"}, {"GET", "/v1/subscriptions"}, {"GET", "/v1/subscriptions/x"},
		{"PATCH", "/v1/subscriptions/x"}, {"DELETE", "/v1/subscriptions/x"},
		{"GET", "/v1/dlq"}, {"POST", "/v1/dlq/x/replay"}, {"GET", "/v1/deliveries"}, {"GET", "/v1/deliveries/x"},
	}
	for _, rt := range routes {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(rt.method, rt.path, nil))
		if w.Code != http.StatusUnauthorized {
			t.Errorf("%s %s without token = %d, want 401", rt.method, rt.path, w.Code)
		}
	}
	for _, p := range []string{"/healthz", "/readyz", "/metrics"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", p, nil))
		if w.Code == http.StatusUnauthorized {
			t.Errorf("ops route %s required auth", p)
		}
	}
}
```

- [ ] **Step 2: Run test to verify pass**

Run: `go test -tags integration ./internal/admin/ -run TestEveryV1Route -v`
Expected: PASS (DLQ/deliveries routes are still stubs but auth fires before the stub).

- [ ] **Step 3: Commit**

```bash
git add internal/admin/auth_integration_test.go
git commit -m "test(admin): assert all /v1 routes auth-gated and ops routes exempt"
```

> **M-A2 gate (Opus):** full VERIFY incl. `make itest`; confirm migration 0002 roundtrips, `endpoint_id` NOT NULL holds and is populated on every new delivery, CRUD + soft-delete semantics (deleted ≠ paused), F3 deleted-sub immutability, secret-rotation one-time + no-store, and SSRF reject on create/PATCH. The NOT NULL constraint must be a **separate `0003` migration** in Task 7, never edited into the already-applied `0002`.

---

## Milestone M-A3 — DLQ + deliveries query surface (Tasks 12–14)

DLQ browse, the **atomic race-free** `ReplayDeadLetter` (the highest-correctness piece of this milestone), and the deliveries browse + timeline with the durable `attempts_truncated` marker.

### Task 12: DLQ browse

**Files:**
- Create: `internal/store/dlq.go` (`ListDLQ`)
- Modify: `internal/admin/dlq.go` (`listDLQ` handler)
- Create: `internal/admin/dlq_test.go`

**Interfaces:**
- Produces (store): `ListDLQ(ctx, f DLQFilter) ([]DLQRow, error)` keyset on `dead_letters.id DESC` (the bigserial — never `dead_at`, which re-dead-letter rewrites; design §2.1). `DLQFilter{AfterID int64; Limit int; EndpointID string; Replayed *bool; Since, Until *time.Time}`. `DLQRow{ID int64; DeliveryID, EndpointID, FinalError string; DeadAt time.Time; ReplayedAt *time.Time}`.
- Produces (handler): `GET /v1/dlq` with `?endpoint_id=`,`?replayed=`,`?since=`,`?until=`,`?limit=`,`?cursor=` (cursor encodes the int64 id as decimal text).

- [ ] **Step 1: Write the failing test**

```go
//go:build integration

package admin_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

func TestDLQBrowse(t *testing.T) {
	srv, st := newServer(t)
	ctx := context.Background()
	// seed a dead-lettered delivery directly (store-level, no worker needed)
	did := seedDeadLetter(t, st, "dlq.*")
	w := do(t, srv, "GET", "/v1/dlq", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("dlq = %d", w.Code)
	}
	var page struct {
		Items []struct{ DeliveryID string `json:"delivery_id"` } `json:"items"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &page)
	found := false
	for _, it := range page.Items {
		if it.DeliveryID == did {
			found = true
		}
	}
	if !found {
		t.Fatalf("dead-lettered delivery %s not in DLQ page: %s", did, w.Body.String())
	}
	_ = ctx
}

type dlqPage struct {
	Items      []struct{ DeliveryID string `json:"delivery_id"` } `json:"items"`
	NextCursor string                                              `json:"next_cursor"`
}

func TestDLQFilterAndCursor(t *testing.T) {
	srv, st := newServer(t)
	ctx := context.Background()
	// three dead-lettered deliveries on three distinct endpoints
	want := map[string]bool{}
	var oneDID string
	for _, p := range []string{"f1.*", "f2.*", "f3.*"} {
		did := seedDeadLetter(t, st, p)
		want[did] = true
		oneDID = did
	}
	// replayed=false returns exactly the three un-replayed rows
	var all dlqPage
	_ = json.Unmarshal(do(t, srv, "GET", "/v1/dlq?replayed=false", nil).Body.Bytes(), &all)
	if len(all.Items) != 3 {
		t.Fatalf("replayed=false returned %d, want 3", len(all.Items))
	}
	// replayed=true returns none (nothing replayed yet)
	var none dlqPage
	_ = json.Unmarshal(do(t, srv, "GET", "/v1/dlq?replayed=true", nil).Body.Bytes(), &none)
	if len(none.Items) != 0 {
		t.Fatalf("replayed=true returned %d, want 0", len(none.Items))
	}
	// endpoint filter: each seed used a distinct endpoint → filtering by one
	// endpoint returns exactly that one dead-letter
	var epID string
	_ = st.Pool.QueryRow(ctx, `SELECT endpoint_id FROM deliveries WHERE id=$1`, oneDID).Scan(&epID)
	var byEp dlqPage
	_ = json.Unmarshal(do(t, srv, "GET", "/v1/dlq?endpoint_id="+epID, nil).Body.Bytes(), &byEp)
	if len(byEp.Items) != 1 || byEp.Items[0].DeliveryID != oneDID {
		t.Fatalf("endpoint filter = %v, want exactly [%s]", byEp.Items, oneDID)
	}
	// keyset paging: limit=2 → page1 (2 items, cursor), page2 (exactly 1 item);
	// the union of both pages must equal all three seeded ids, no overlap.
	var page1 dlqPage
	_ = json.Unmarshal(do(t, srv, "GET", "/v1/dlq?limit=2", nil).Body.Bytes(), &page1)
	if len(page1.Items) != 2 || page1.NextCursor == "" {
		t.Fatalf("page1 items=%d cursor=%q, want 2 + non-empty cursor", len(page1.Items), page1.NextCursor)
	}
	var page2 dlqPage
	_ = json.Unmarshal(do(t, srv, "GET", "/v1/dlq?limit=2&cursor="+page1.NextCursor, nil).Body.Bytes(), &page2)
	if len(page2.Items) != 1 {
		t.Fatalf("page2 items=%d, want exactly 1 (the remaining row)", len(page2.Items))
	}
	union := map[string]bool{}
	for _, it := range append(append([]struct{ DeliveryID string `json:"delivery_id"` }{}, page1.Items...), page2.Items...) {
		if union[it.DeliveryID] {
			t.Fatalf("cursor page overlap: %s on both pages", it.DeliveryID)
		}
		union[it.DeliveryID] = true
	}
	for did := range want {
		if !union[did] {
			t.Fatalf("paged result missing seeded delivery %s", did)
		}
	}
	// malformed cursor → 400
	if bad := do(t, srv, "GET", "/v1/dlq?cursor=%%%not-base64", nil); bad.Code != http.StatusBadRequest {
		t.Fatalf("malformed cursor = %d, want 400", bad.Code)
	}
}
```
Add the `seedDeadLetter` helper to `internal/admin/testutil_test.go`:
```go
// seedDeadLetter ingests one event and drives its single delivery to
// dead_lettered via direct SQL (so DLQ/replay tests need no live worker).
func seedDeadLetter(t *testing.T, s *store.Store, pattern string) string {
	t.Helper()
	ctx := context.Background()
	keyID := seedPipeline(t, s, pattern)
	res, err := s.IngestEvent(ctx, store.IngestParams{ProducerKeyID: keyID, Topic: strings.Replace(pattern, "*", "x", 1), Payload: []byte(`{}`)})
	if err != nil || len(res.DeliveryIDs) != 1 {
		t.Fatalf("seed ingest: %v", err)
	}
	id := res.DeliveryIDs[0]
	if _, err := s.Pool.Exec(ctx,
		`UPDATE deliveries SET state='dead_lettered', lease_until=NULL WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Pool.Exec(ctx,
		`INSERT INTO dead_letters (delivery_id, final_error, endpoint_id)
		 SELECT id, 'seeded', endpoint_id FROM deliveries WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	return id
}
```
`seedPipeline` is defined in the store package's integration tests; the admin tests need their own. Add to `testutil_test.go`:
```go
// seedPipeline creates a producer key, endpoint, and one subscription matching
// pattern, returning the producer key id (mirrors the store test helper).
func seedPipeline(t *testing.T, s *store.Store, pattern string) string {
	t.Helper()
	ctx := context.Background()
	keyID, _, err := s.CreateProducerKey(ctx, "test")
	if err != nil {
		t.Fatal(err)
	}
	epID, _, err := s.CreateEndpoint(ctx, [32]byte{}, "https://example.com/h", "seed")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateSubscriptionFull(ctx, store.SubInput{TopicPattern: pattern, EndpointID: epID, MaxAttempts: 3}); err != nil {
		t.Fatal(err)
	}
	return keyID
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -tags integration ./internal/admin/ -run TestDLQBrowse -v`
Expected: FAIL — `listDLQ` is a stub.

- [ ] **Step 3: Write the DLQ store method**

```go
// internal/store/dlq.go
package store

import (
	"context"
	"strconv"
	"time"
)

type DLQFilter struct {
	AfterID    int64 // 0 = from newest
	Limit      int
	EndpointID string
	Replayed   *bool
	Since      *time.Time
	Until      *time.Time
}

type DLQRow struct {
	ID         int64      `json:"-"`
	DeliveryID string     `json:"delivery_id"`
	EndpointID *string    `json:"endpoint_id,omitempty"`
	FinalError string     `json:"final_error"`
	DeadAt     time.Time  `json:"dead_at"`
	ReplayedAt *time.Time `json:"replayed_at,omitempty"`
}

// ListDLQ pages dead_letters keyset on the bigserial id DESC (immutable;
// never dead_at — re-dead-letter rewrites it, design §2.1).
func (s *Store) ListDLQ(ctx context.Context, f DLQFilter) ([]DLQRow, error) {
	// AfterID == 0 means "from the newest"; the bigserial id is always > 0.
	q := `SELECT id, delivery_id, endpoint_id, final_error, dead_at, replayed_at
	      FROM dead_letters WHERE ($1 = 0 OR id < $1)`
	args := []any{f.AfterID, f.Limit}
	add := func(cond string, v any) { args = append(args, v); q += cond + strconv.Itoa(len(args)) }
	if f.EndpointID != "" {
		add(" AND endpoint_id = $", f.EndpointID)
	}
	if f.Replayed != nil {
		if *f.Replayed {
			q += " AND replayed_at IS NOT NULL"
		} else {
			q += " AND replayed_at IS NULL"
		}
	}
	if f.Since != nil {
		add(" AND dead_at >= $", *f.Since)
	}
	if f.Until != nil {
		add(" AND dead_at < $", *f.Until)
	}
	q += " ORDER BY id DESC LIMIT $2"
	rows, err := s.Pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DLQRow
	for rows.Next() {
		var d DLQRow
		if err := rows.Scan(&d.ID, &d.DeliveryID, &d.EndpointID, &d.FinalError, &d.DeadAt, &d.ReplayedAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
```
> Note: `$2` (limit) is referenced after appended args — keep limit at index 2 by passing it second in `args` and never reusing `$2`. The `add` helper appends from index 3 onward, so `$2` stays the limit. Verify the generated SQL in the test.

- [ ] **Step 4: Write the DLQ handler**

Replace the `listDLQ` stub in `internal/admin/dlq.go`:
```go
package admin

import (
	"net/http"
	"strconv"
	"time"

	"github.com/mit112/hookrail/internal/httpx"
	"github.com/mit112/hookrail/internal/store"
)

func (s *Server) listDLQ(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := store.DLQFilter{Limit: httpx.ClampLimit(q.Get("limit"), 50, 200), EndpointID: q.Get("endpoint_id")}
	if c, err := httpx.DecodeCursor(q.Get("cursor")); err != nil {
		httpx.Problem(w, http.StatusBadRequest, "bad cursor", "cursor is not decodable")
		return
	} else if c != "" {
		n, err := strconv.ParseInt(c, 10, 64)
		if err != nil {
			httpx.Problem(w, http.StatusBadRequest, "bad cursor", "cursor is not a valid id")
			return
		}
		f.AfterID = n
	}
	if v := q.Get("replayed"); v == "true" || v == "false" {
		b := v == "true"
		f.Replayed = &b
	}
	if t, ok := parseTime(q.Get("since")); ok {
		f.Since = &t
	}
	if t, ok := parseTime(q.Get("until")); ok {
		f.Until = &t
	}
	rows, err := s.store.ListDLQ(r.Context(), f)
	if err != nil {
		httpx.Problem(w, http.StatusServiceUnavailable, "dlq list failed", "query error")
		return
	}
	writePage(w, rows, len(rows), f.Limit, func() string {
		if len(rows) == 0 {
			return ""
		}
		return strconv.FormatInt(rows[len(rows)-1].ID, 10)
	})
}

func parseTime(v string) (time.Time, bool) {
	if v == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, v)
	return t, err == nil
}
```

- [ ] **Step 5: Run tests to verify pass**

Run: `go test -tags integration ./internal/admin/ -run TestDLQBrowse -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/store/dlq.go internal/admin/dlq.go internal/admin/testutil_test.go internal/admin/dlq_test.go
git commit -m "feat(admin): DLQ browse with id-keyset pagination + endpoint/replayed/time filters"
```

### Task 13: Atomic, race-free `ReplayDeadLetter` + replay handler

**Files:**
- Create: `internal/store/replay.go`
- Modify: `internal/admin/dlq.go` (`replayDLQ` handler)
- Create: `internal/store/replay_test.go` (store-level concurrency)
- Create: `internal/admin/replay_test.go` (handler status codes)

**Interfaces:**
- Produces (store): `ReplayDeadLetter(ctx, deliveryID string, replayAge time.Duration) (ReplayOutcome, error)` where `ReplayOutcome` ∈ {`ReplayOK`, `ReplayNotFound`, `ReplayConflict`, `ReplayGone`}. One tx: atomic replay-claim CAS → deleted-target guard → delivery reset; classification from a read-back (design §4.1, §2.2). `replayAge` = `cfg.EventPayloadRetention` (the SAME `$age` as tombstone/expiry, design §3).
- Produces (handler): `POST /v1/dlq/{delivery_id}/replay` → 200 / 404 / 409 / 410; on 200 best-effort `queue.Publish`.

- [ ] **Step 1: Write the failing store test (concurrency + classification)**

```go
//go:build integration

package store_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/mit112/hookrail/internal/store"
)

// seedDead drives one delivery to dead_lettered and inserts its DLQ row.
func seedDead(t *testing.T, s *store.Store, pattern string) string {
	t.Helper()
	ctx := context.Background()
	keyID := seedPipeline(t, s, pattern)
	res, _ := s.IngestEvent(ctx, store.IngestParams{ProducerKeyID: keyID, Topic: pattern[:len(pattern)-1] + "x", Payload: []byte(`{}`)})
	id := res.DeliveryIDs[0]
	_, _ = s.Pool.Exec(ctx, `UPDATE deliveries SET state='dead_lettered', lease_until=NULL WHERE id=$1`, id)
	_, _ = s.Pool.Exec(ctx, `INSERT INTO dead_letters (delivery_id, final_error, endpoint_id) SELECT id,'x',endpoint_id FROM deliveries WHERE id=$1`, id)
	return id
}

func TestReplayConcurrentExactlyOneWinner(t *testing.T) {
	s := testStore(t)
	id := seedDead(t, s, "rp1.*")
	const n = 8
	results := make([]store.ReplayOutcome, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			o, err := s.ReplayDeadLetter(context.Background(), id, time.Hour)
			if err != nil {
				t.Errorf("replay %d: %v", i, err)
			}
			results[i] = o
		}(i)
	}
	wg.Wait()
	ok := 0
	for _, o := range results {
		switch o {
		case store.ReplayOK:
			ok++
		case store.ReplayConflict: // every loser must be a clean 409, not 404/410
		default:
			t.Fatalf("loser outcome = %v, want ReplayConflict", o)
		}
	}
	if ok != 1 {
		t.Fatalf("%d winners, want exactly 1 (rest 409)", ok)
	}
	// the winner re-armed the delivery to pending; claim_version is UNCHANGED
	// by replay (design §4.1 step 4 — the fence keeps counting across replay)
	var state string
	var cv int64
	_ = s.Pool.QueryRow(context.Background(),
		`SELECT state::text, claim_version FROM deliveries WHERE id=$1`, id).Scan(&state, &cv)
	if state != "pending" {
		t.Fatalf("post-replay state = %q, want pending", state)
	}
	if cv != 0 {
		t.Fatalf("claim_version = %d after replay; replay must NOT reset/bump it (was 0 pre-claim)", cv)
	}
}

// TestReplayStepTwoRollback: the delivery is no longer dead_lettered when
// step-2 runs (it moved to in_flight). Step-1's CAS may claim the replay, but
// step-2's `WHERE state='dead_lettered'` affects 0 rows → the WHOLE tx rolls
// back → ReplayConflict, and dead_letters.replayed_at must NOT be left stamped.
// (ClaimDelivery cannot select a dead_lettered row — claim.go's predicate only
// accepts due pending/retry or expired in_flight — so we set in_flight directly
// to create the exact race the rollback guard exists for.)
func TestReplayStepTwoRollback(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	id := seedDead(t, s, "rvc.*")
	_, _ = s.Pool.Exec(ctx, `UPDATE deliveries SET state='in_flight', lease_until=now()+interval '30s' WHERE id=$1`, id)
	out, err := s.ReplayDeadLetter(ctx, id, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if out != store.ReplayConflict {
		t.Fatalf("step-2 0-rows = %v, want ReplayConflict (rollback)", out)
	}
	var replayedAt *time.Time
	_ = s.Pool.QueryRow(ctx, `SELECT replayed_at FROM dead_letters WHERE delivery_id=$1`, id).Scan(&replayedAt)
	if replayedAt != nil {
		t.Fatal("rolled-back replay must NOT leave replayed_at stamped (atomicity)")
	}
}

// TestReplayThenClaimBumpsFence proves the replay↔claim interplay: a successful
// replay re-arms to pending WITHOUT touching claim_version (design §4.1 step 4),
// and a subsequent worker claim then bumps the fence monotonically — so a stale
// pre-replay completion can never overwrite the re-armed delivery.
func TestReplayThenClaimBumpsFence(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	id := seedDead(t, s, "rtc.*")
	var cvBefore int64
	_ = s.Pool.QueryRow(ctx, `SELECT claim_version FROM deliveries WHERE id=$1`, id).Scan(&cvBefore)

	if out, err := s.ReplayDeadLetter(ctx, id, time.Hour); err != nil || out != store.ReplayOK {
		t.Fatalf("replay = %v %v, want ReplayOK", out, err)
	}
	var cvAfterReplay int64
	_ = s.Pool.QueryRow(ctx, `SELECT claim_version FROM deliveries WHERE id=$1`, id).Scan(&cvAfterReplay)
	if cvAfterReplay != cvBefore {
		t.Fatalf("claim_version changed by replay: %d -> %d (must be untouched)", cvBefore, cvAfterReplay)
	}
	ok, d, err := s.ClaimDelivery(ctx, id, 30*time.Second)
	if err != nil || !ok {
		t.Fatalf("claim after replay: ok=%v err=%v", ok, err)
	}
	if d.ClaimVersion <= cvBefore {
		t.Fatalf("claim_version = %d after claim, want > %d (fence keeps counting across replay)", d.ClaimVersion, cvBefore)
	}
}

func TestReplayClassification(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	// unknown id → NotFound
	if o, _ := s.ReplayDeadLetter(ctx, "does-not-exist", time.Hour); o != store.ReplayNotFound {
		t.Fatalf("unknown = %v, want NotFound", o)
	}
	// live (not dead) delivery → Conflict
	live := mkDelivery(t, s)
	if o, _ := s.ReplayDeadLetter(ctx, live, time.Hour); o != store.ReplayConflict {
		t.Fatalf("live = %v, want Conflict", o)
	}
	// dead but past expiry → Gone
	old := seedDead(t, s, "rp2.*")
	_, _ = s.Pool.Exec(ctx, `UPDATE dead_letters SET dead_at = now() - interval '48 hours' WHERE delivery_id=$1`, old)
	if o, _ := s.ReplayDeadLetter(ctx, old, time.Hour); o != store.ReplayGone {
		t.Fatalf("expired = %v, want Gone", o)
	}
	// deleted target → Conflict
	delTgt := seedDead(t, s, "rp3.*")
	_, _ = s.Pool.Exec(ctx, `UPDATE subscriptions SET deleted_at=now()`)
	if o, _ := s.ReplayDeadLetter(ctx, delTgt, time.Hour); o != store.ReplayConflict {
		t.Fatalf("deleted-target = %v, want Conflict", o)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -tags integration ./internal/store/ -run TestReplay -v`
Expected: FAIL — `ReplayDeadLetter`/`ReplayOutcome` undefined.

- [ ] **Step 3: Write the store implementation**

```go
// internal/store/replay.go
package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

type ReplayOutcome int

const (
	ReplayOK       ReplayOutcome = iota // 200
	ReplayNotFound                      // 404
	ReplayConflict                      // 409
	ReplayGone                          // 410
)

// ReplayDeadLetter atomically re-arms a dead-lettered delivery (design §4.1).
// One tx: (1) atomic replay-claim CAS — exactly one concurrent winner;
// (2) deleted-target guard; (3) fenced delivery reset (claim_version UNTOUCHED).
// On a CAS miss it classifies 404/409/410 from a read-back. Best-effort XADD
// is the caller's job after commit.
func (s *Store) ReplayDeadLetter(ctx context.Context, deliveryID string, replayAge time.Duration) (ReplayOutcome, error) {
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return ReplayConflict, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// (1) linearization point: claim the replay iff un-replayed AND within $age.
	ct, err := tx.Exec(ctx,
		`UPDATE dead_letters SET replayed_at = now()
		 WHERE delivery_id = $1 AND replayed_at IS NULL AND dead_at >= now() - $2::interval`,
		deliveryID, replayAge)
	if err != nil {
		return ReplayConflict, err
	}
	if ct.RowsAffected() == 0 {
		return s.classifyReplayMiss(ctx, deliveryID, replayAge) // read-only, own queries
	}

	// (3a) deleted-target guard: never resurrect a deleted destination.
	var depDeleted bool
	if err := tx.QueryRow(ctx,
		`SELECT (sub.deleted_at IS NOT NULL OR ep.deleted_at IS NOT NULL)
		 FROM deliveries d
		 JOIN subscriptions sub ON sub.id = d.subscription_id
		 JOIN endpoints ep ON ep.id = sub.endpoint_id
		 WHERE d.id = $1`, deliveryID).Scan(&depDeleted); err != nil {
		return ReplayConflict, err
	}
	if depDeleted {
		return ReplayConflict, nil // rollback via defer
	}

	// (3b) fenced reset: attempt_count→0, claim_version UNTOUCHED (design §4.1 step 4).
	ct2, err := tx.Exec(ctx,
		`UPDATE deliveries SET state='pending', attempt_count=0, next_attempt_at=now(),
		   lease_until=NULL, updated_at=now()
		 WHERE id=$1 AND state='dead_lettered'`, deliveryID)
	if err != nil {
		return ReplayConflict, err
	}
	if ct2.RowsAffected() == 0 {
		return ReplayConflict, nil // state changed under us → rollback
	}
	if err := tx.Commit(ctx); err != nil {
		return ReplayConflict, err
	}
	return ReplayOK, nil
}

func (s *Store) classifyReplayMiss(ctx context.Context, deliveryID string, replayAge time.Duration) (ReplayOutcome, error) {
	var alreadyReplayed, expired bool
	err := s.Pool.QueryRow(ctx,
		`SELECT replayed_at IS NOT NULL, dead_at < now() - $2::interval
		 FROM dead_letters WHERE delivery_id=$1`, deliveryID, replayAge).
		Scan(&alreadyReplayed, &expired)
	if errors.Is(err, pgx.ErrNoRows) {
		var exists bool
		if err := s.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM deliveries WHERE id=$1)`, deliveryID).Scan(&exists); err != nil {
			return ReplayConflict, err
		}
		if !exists {
			return ReplayNotFound, nil // unknown delivery
		}
		return ReplayConflict, nil // live delivery, not dead-lettered (design §2.2)
	}
	if err != nil {
		return ReplayConflict, err
	}
	if expired && !alreadyReplayed {
		return ReplayGone, nil
	}
	return ReplayConflict, nil // already replayed, or any other non-claimable state
}
```

- [ ] **Step 4: Write the replay handler**

Replace the `replayDLQ` stub in `internal/admin/dlq.go`:
```go
func (s *Server) replayDLQ(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("delivery_id")
	out, err := s.store.ReplayDeadLetter(r.Context(), id, s.replayAge)
	if err != nil {
		httpx.Problem(w, http.StatusServiceUnavailable, "replay failed", "query error")
		return
	}
	switch out {
	case store.ReplayOK:
		// best-effort re-publish; sweeper repairs on failure (design §4.1 step 5)
		if perr := s.queue.Publish(r.Context(), id); perr != nil {
			// non-fatal: row is pending; the sweeper will pick it up
			_ = perr
		}
		writeJSON(w, http.StatusOK, map[string]string{"delivery_id": id, "state": "pending"})
	case store.ReplayNotFound:
		httpx.Problem(w, http.StatusNotFound, "not found", "no delivery with that id")
	case store.ReplayGone:
		httpx.Problem(w, http.StatusGone, "expired", "dead-letter is past the replay window")
	default:
		httpx.Problem(w, http.StatusConflict, "not replayable", "delivery is live, already replayed, or its target is deleted")
	}
}
```
Add a `replayAge time.Duration` field to `admin.Server` and set it in `New`. This changes `admin.New`'s arity — update **every** call site in the same commit:
1. `internal/admin/server.go`: add `"time"` to the import block; add the field to `Server`; change the signature and body:
   ```go
   func New(s *store.Store, q Publisher, masterKey [32]byte, pol ssrf.Policy, limits *ratelimit.Registry, token string, replayAge time.Duration) *Server {
   	return &Server{store: s, queue: q, masterKey: masterKey, policy: pol, limits: limits, tokenDigest: digest(token), replayAge: replayAge}
   }
   ```
2. `cmd/hookrail-admin/main.go`: pass `cfg.EventPayloadRetention` as the final arg.
3. `internal/admin/testutil_test.go`: the `newServer` helper's `admin.New(...)` call — pass `time.Hour` (add `"time"` to that file's imports if not already present — it is, via the container wait).
4. `internal/admin/server_test.go`: the `New(nil, nil, [32]byte{}, ssrf.Policy{}, ratelimit.NewRegistry(1, 1), "tok")` call in `TestOpsRoutesExemptAndV1Guarded` — append `, time.Hour` and add `"time"` to that test file's imports.

- [ ] **Step 5: Write the handler status-code test**

```go
//go:build integration

package admin_test

import (
	"net/http"
	"testing"
)

func TestReplayHandlerStatusCodes(t *testing.T) {
	srv, st := newServer(t)
	// unknown → 404
	if w := do(t, srv, "POST", "/v1/dlq/nope/replay", nil); w.Code != http.StatusNotFound {
		t.Fatalf("unknown replay = %d, want 404", w.Code)
	}
	// dead-lettered → 200
	id := seedDeadLetter(t, st, "rh.*")
	if w := do(t, srv, "POST", "/v1/dlq/"+id+"/replay", nil); w.Code != http.StatusOK {
		t.Fatalf("replay = %d, want 200", w.Code)
	}
	// replaying again (now pending) → 409
	if w := do(t, srv, "POST", "/v1/dlq/"+id+"/replay", nil); w.Code != http.StatusConflict {
		t.Fatalf("double replay = %d, want 409", w.Code)
	}
}
```

- [ ] **Step 6: Run tests to verify pass**

Run: `go test -tags integration ./internal/store/ -run TestReplay -v && go test -tags integration ./internal/admin/ -run TestReplayHandler -v`
Expected: PASS (one winner; classification correct; handler 404/200/409).

- [ ] **Step 7: Commit**

```bash
git add internal/store/replay.go internal/store/replay_test.go internal/admin/dlq.go internal/admin/server.go cmd/hookrail-admin/main.go internal/admin/testutil_test.go internal/admin/replay_test.go
git commit -m "feat(store,admin): atomic race-free DLQ replay with 404/409/410 classification + deleted-target guard"
```

### Task 14: Deliveries browse + timeline + `attempts_truncated` marker

**Files:**
- Create: `internal/store/browse.go` (`ListDeliveries`, `GetDeliveryTimeline`)
- Modify: `internal/admin/deliveries.go` (handlers)
- Modify: `internal/store/status.go` (surface `attempts_truncated` in producer status too)
- Create: `internal/admin/deliveries_test.go`

**Interfaces:**
- Produces (store): `ListDeliveries(ctx, f DeliveryFilter) ([]DeliveryListRow, error)` keyset on `id DESC`, filters `?state=`,`?endpoint_id=`,`?topic=`,`?event_id=`; `GetDeliveryTimeline(ctx, id string) (DeliveryTimeline, error)` = state + attempts + durable `AttemptsTruncated bool`.
- Modifies: `store.DeliveryView` gains `AttemptsTruncated bool` so `GET /v1/events/{id}` (producer) surfaces it too (design §7, F12).

- [ ] **Step 1: Write the failing test**

```go
//go:build integration

package admin_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/mit112/hookrail/internal/store"
)

func TestDeliveriesBrowseAndTimeline(t *testing.T) {
	srv, st := newServer(t)
	ctx := context.Background()
	keyID := seedPipeline(t, st, "dv.*")
	res, _ := st.IngestEvent(ctx, store.IngestParams{ProducerKeyID: keyID, Topic: "dv.x", Payload: []byte(`{}`)})
	did := res.DeliveryIDs[0]

	// browse filtered by state=pending must return THIS delivery (not just 200)
	w := do(t, srv, "GET", "/v1/deliveries?state=pending", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("browse = %d", w.Code)
	}
	var page struct {
		Items []struct {
			ID    string `json:"id"`
			State string `json:"state"`
		} `json:"items"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &page)
	foundPending := false
	for _, it := range page.Items {
		if it.ID == did && it.State == "pending" {
			foundPending = true
		}
	}
	if !foundPending {
		t.Fatalf("state=pending filter did not return the pending delivery: %s", w.Body.String())
	}
	// negative filter: state=succeeded must NOT include it
	wn := do(t, srv, "GET", "/v1/deliveries?state=succeeded", nil)
	if strings.Contains(wn.Body.String(), did) {
		t.Fatal("state=succeeded filter wrongly returned a pending delivery")
	}

	// timeline shows the durable attempts_truncated marker (false here)
	tw := do(t, srv, "GET", "/v1/deliveries/"+did, nil)
	if tw.Code != http.StatusOK {
		t.Fatalf("timeline = %d", tw.Code)
	}
	var tl struct {
		State             string `json:"state"`
		AttemptsTruncated bool   `json:"attempts_truncated"`
	}
	_ = json.Unmarshal(tw.Body.Bytes(), &tl)
	if tl.State != "pending" || tl.AttemptsTruncated {
		t.Fatalf("timeline = %s", tw.Body.String())
	}

	// flip the durable marker and confirm it surfaces
	_, _ = st.Pool.Exec(ctx, `UPDATE deliveries SET attempts_truncated_at = now() WHERE id=$1`, did)
	tw2 := do(t, srv, "GET", "/v1/deliveries/"+did, nil)
	_ = json.Unmarshal(tw2.Body.Bytes(), &tl)
	if !tl.AttemptsTruncated {
		t.Fatal("attempts_truncated should be true after marker set")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -tags integration ./internal/admin/ -run TestDeliveriesBrowse -v`
Expected: FAIL — stubs.

- [ ] **Step 3: Write the browse + timeline store methods**

```go
// internal/store/browse.go
package store

import (
	"context"
	"strconv"
)

type DeliveryFilter struct {
	AfterID    string
	Limit      int
	State      string
	EndpointID string
	Topic      string
	EventID    string
}

type DeliveryListRow struct {
	ID         string `json:"id"`
	EventID    string `json:"event_id"`
	EndpointID string `json:"endpoint_id"`
	State      string `json:"state"`
}

func (s *Store) ListDeliveries(ctx context.Context, f DeliveryFilter) ([]DeliveryListRow, error) {
	q := `SELECT d.id, d.event_id, d.endpoint_id, d.state::text
	      FROM deliveries d JOIN events e ON e.id = d.event_id
	      WHERE ($1 = '' OR d.id < $1)`
	args := []any{f.AfterID, f.Limit}
	add := func(cond string, v any) { args = append(args, v); q += cond + strconv.Itoa(len(args)) }
	if f.State != "" {
		add(" AND d.state = $", f.State+"") // cast below
		q += "::delivery_state"
	}
	if f.EndpointID != "" {
		add(" AND d.endpoint_id = $", f.EndpointID)
	}
	if f.Topic != "" {
		add(" AND e.topic = $", f.Topic)
	}
	if f.EventID != "" {
		add(" AND d.event_id = $", f.EventID)
	}
	q += " ORDER BY d.id DESC LIMIT $2"
	rows, err := s.Pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DeliveryListRow
	for rows.Next() {
		var d DeliveryListRow
		if err := rows.Scan(&d.ID, &d.EventID, &d.EndpointID, &d.State); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

type DeliveryTimeline struct {
	DeliveryID        string        `json:"delivery_id"`
	State             string        `json:"state"`
	AttemptsTruncated bool          `json:"attempts_truncated"`
	Attempts          []AttemptView `json:"attempts"`
}

func (s *Store) GetDeliveryTimeline(ctx context.Context, id string) (DeliveryTimeline, error) {
	var tl DeliveryTimeline
	var truncatedAt *string
	if err := s.Pool.QueryRow(ctx,
		`SELECT id, state::text, (attempts_truncated_at IS NOT NULL)::text
		 FROM deliveries WHERE id=$1`, id).Scan(&tl.DeliveryID, &tl.State, &truncatedAt); err != nil {
		return tl, err
	}
	tl.AttemptsTruncated = truncatedAt != nil && *truncatedAt == "true"
	rows, err := s.Pool.Query(ctx,
		`SELECT attempt_no, claim_version, status::text, http_status, error_class, latency_ms
		 FROM delivery_attempts WHERE delivery_id=$1 ORDER BY claim_version`, id)
	if err != nil {
		return tl, err
	}
	defer rows.Close()
	tl.Attempts = []AttemptView{}
	for rows.Next() {
		var a AttemptView
		if err := rows.Scan(&a.AttemptNo, &a.ClaimVersion, &a.Status, &a.HTTPStatus, &a.ErrorClass, &a.LatencyMS); err != nil {
			return tl, err
		}
		tl.Attempts = append(tl.Attempts, a)
	}
	return tl, rows.Err()
}
```
> The `state` cast helper above appends `::delivery_state` directly after the placeholder; verify the generated SQL is `d.state = $N::delivery_state`. If the `add`+manual-append composition is fragile, inline the state branch without the `add` helper.

- [ ] **Step 4: Surface the marker in producer status too**

In `internal/store/status.go`, add `AttemptsTruncated bool` to `DeliveryView` (json `attempts_truncated`), and in `GetEventStatus` change the deliveries query + scan:
```go
	rows, err := s.Pool.Query(ctx,
		`SELECT id, state::text, (attempts_truncated_at IS NOT NULL) FROM deliveries WHERE event_id = $1 ORDER BY id`, eventID)
	...
		if err := rows.Scan(&d.DeliveryID, &d.State, &d.AttemptsTruncated); err != nil {
```

- [ ] **Step 5: Write the deliveries handlers**

Replace stubs in `internal/admin/deliveries.go`:
```go
package admin

import (
	"net/http"

	"github.com/mit112/hookrail/internal/httpx"
	"github.com/mit112/hookrail/internal/store"
)

func (s *Server) listDeliveries(w http.ResponseWriter, r *http.Request) {
	cursor, err := httpx.DecodeCursor(r.URL.Query().Get("cursor"))
	if err != nil {
		httpx.Problem(w, http.StatusBadRequest, "bad cursor", "cursor is not decodable")
		return
	}
	q := r.URL.Query()
	f := store.DeliveryFilter{
		AfterID: cursor, Limit: httpx.ClampLimit(q.Get("limit"), 50, 200),
		State: q.Get("state"), EndpointID: q.Get("endpoint_id"), Topic: q.Get("topic"), EventID: q.Get("event_id"),
	}
	rows, err := s.store.ListDeliveries(r.Context(), f)
	if err != nil {
		httpx.Problem(w, http.StatusServiceUnavailable, "list failed", "query error")
		return
	}
	writePage(w, rows, len(rows), f.Limit, func() string {
		if len(rows) == 0 {
			return ""
		}
		return rows[len(rows)-1].ID
	})
}

func (s *Server) getDelivery(w http.ResponseWriter, r *http.Request) {
	tl, err := s.store.GetDeliveryTimeline(r.Context(), r.PathValue("id"))
	if err != nil {
		httpx.Problem(w, http.StatusNotFound, "not found", "no delivery with that id")
		return
	}
	writeJSON(w, http.StatusOK, tl)
}
```

- [ ] **Step 6: Run tests to verify pass**

Run: `go test -tags integration ./internal/admin/ -run TestDeliveriesBrowse -v && go test -tags integration ./internal/store/ -run TestEventStatus -v`
Expected: PASS (timeline marker; existing status tests still green with the new field).

- [ ] **Step 7: Commit**

```bash
git add internal/store/browse.go internal/store/status.go internal/admin/deliveries.go internal/admin/deliveries_test.go
git commit -m "feat(admin): deliveries browse + timeline with durable attempts_truncated marker (also in producer status)"
```

> **M-A3 gate (Opus):** full VERIFY; pay special attention to `ReplayDeadLetter` — concurrent replay-vs-replay yields exactly one 200 and the rest 409; classification 404/409/410 matches §2.2 exactly (live delivery is 409, not 404); deleted-target → 409; `claim_version` is NOT reset by replay. DLQ keyset is on `dead_letters.id`, never `dead_at`.

---

## Milestone M-A4a — Delivery-core wiring (Tasks 15–18) · Codex pre-gate + Opus

**High-risk: edits the P0 delivery hot path.** Per design D-A6, this milestone gets a mandatory **Codex gpt-5.5-xhigh pre-gate review** (run Prompt C with the Codex reviewer, or a dedicated Codex pass) BEFORE the Opus gate. TDD-first throughout; never weaken the existing claim/attempt/worker tests.

### Task 15: `cancelled` terminal state + cancel-on-delete + reconciliation

**Files:**
- Modify: `internal/domain/state.go`
- Modify: `internal/store/admin.go` (`SoftDeleteEndpoint`, `SoftDeleteSubscription` gain cancellation; add `CancelOrphaned`)
- Create: `internal/domain/state_cancelled_test.go`
- Create: `internal/store/cancel_test.go`

**Interfaces:**
- Produces: `domain.StateCancelled State = "cancelled"`; transitions `pending→cancelled`, `retry_scheduled→cancelled`. `SoftDeleteEndpoint`/`SoftDeleteSubscription` now cancel non-terminal (`pending`/`retry_scheduled`) deliveries of the deleted target in the SAME tx; `in_flight` is left to finish its attempt. `CancelOrphaned(ctx, batch int) (int, error)` cancels `pending`/`retry_scheduled` deliveries whose subscription is soft-deleted (reconciliation; called by the janitor in M-A4b).
- Note: cancellation cannot overwrite `in_flight` (the completion CAS requires `state='in_flight'`), and the claim predicate never selects `cancelled` — so `cancelled` is safely terminal.

- [ ] **Step 1: Write the failing domain test**

```go
package domain

import "testing"

func TestCancelledTransitions(t *testing.T) {
	if !CanTransition(StatePending, StateCancelled) {
		t.Fatal("pending → cancelled must be allowed")
	}
	if !CanTransition(StateRetryScheduled, StateCancelled) {
		t.Fatal("retry_scheduled → cancelled must be allowed")
	}
	if CanTransition(StateInFlight, StateCancelled) {
		t.Fatal("in_flight → cancelled must NOT be allowed (let the attempt finish)")
	}
	if CanTransition(StateCancelled, StatePending) {
		t.Fatal("cancelled is terminal")
	}
	if !StateCancelled.IsTerminalForWorker() {
		t.Fatal("cancelled must be terminal for workers")
	}
	if !StateSucceeded.IsTerminalForWorker() {
		t.Fatal("succeeded must remain terminal for workers")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/domain/ -run TestCancelledTransitions -v`
Expected: FAIL — `StateCancelled` undefined.

- [ ] **Step 3: Add the domain state**

In `internal/domain/state.go`, add the constant and transitions:
```go
	StateCancelled      State = "cancelled"
```
Update `validNext`:
```go
	StatePending:        {StateInFlight: true, StateCancelled: true},
	StateRetryScheduled: {StateInFlight: true, StateCancelled: true},
	StateInFlight:       {StateSucceeded: true, StateRetryScheduled: true, StateDeadLettered: true},
	StateDeadLettered:   {StatePending: true},
	StateSucceeded:      {},
	StateCancelled:      {},
```
And update `IsTerminalForWorker` so `cancelled` is also terminal (workers must never touch it; the claim predicate already excludes it, but the helper must agree):
```go
func (s State) IsTerminalForWorker() bool {
	return s == StateSucceeded || s == StateCancelled
}
```

- [ ] **Step 4: Write the failing cancel store test**

```go
//go:build integration

package store_test

import (
	"context"
	"testing"

	"github.com/mit112/hookrail/internal/store"
)

func TestDeleteEndpointCancelsPendingAndRetry(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	keyID := seedPipeline(t, s, "cx.*")
	res, _ := s.IngestEvent(ctx, store.IngestParams{ProducerKeyID: keyID, Topic: "cx.x", Payload: []byte(`{}`)})
	did := res.DeliveryIDs[0]
	var epID string
	_ = s.Pool.QueryRow(ctx, `SELECT endpoint_id FROM deliveries WHERE id=$1`, did).Scan(&epID)

	if err := s.SoftDeleteEndpoint(ctx, epID); err != nil {
		t.Fatal(err)
	}
	var state string
	_ = s.Pool.QueryRow(ctx, `SELECT state::text FROM deliveries WHERE id=$1`, did).Scan(&state)
	if state != "cancelled" {
		t.Fatalf("pending delivery state after endpoint delete = %q, want cancelled", state)
	}
}

func TestDeleteLeavesInFlight(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	keyID := seedPipeline(t, s, "cy.*")
	res, _ := s.IngestEvent(ctx, store.IngestParams{ProducerKeyID: keyID, Topic: "cy.x", Payload: []byte(`{}`)})
	did := res.DeliveryIDs[0]
	var epID string
	_ = s.Pool.QueryRow(ctx, `SELECT endpoint_id FROM deliveries WHERE id=$1`, did).Scan(&epID)
	_, _ = s.Pool.Exec(ctx, `UPDATE deliveries SET state='in_flight', lease_until=now()+interval '30s' WHERE id=$1`, did)

	_ = s.SoftDeleteEndpoint(ctx, epID)
	var state string
	_ = s.Pool.QueryRow(ctx, `SELECT state::text FROM deliveries WHERE id=$1`, did).Scan(&state)
	if state != "in_flight" {
		t.Fatalf("in_flight delivery state = %q, want in_flight (left to finish)", state)
	}
}

func TestCancelOrphanedReconciles(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	keyID := seedPipeline(t, s, "cz.*")
	res, _ := s.IngestEvent(ctx, store.IngestParams{ProducerKeyID: keyID, Topic: "cz.x", Payload: []byte(`{}`)})
	did := res.DeliveryIDs[0]
	// straggler: sub soft-deleted but delivery left retry_scheduled (race window)
	_, _ = s.Pool.Exec(ctx, `UPDATE subscriptions SET deleted_at=now()`)
	_, _ = s.Pool.Exec(ctx, `UPDATE deliveries SET state='retry_scheduled' WHERE id=$1`, did)
	n, err := s.CancelOrphaned(ctx, 100)
	if err != nil || n != 1 {
		t.Fatalf("CancelOrphaned = %d, %v; want 1", n, err)
	}
	var state string
	_ = s.Pool.QueryRow(ctx, `SELECT state::text FROM deliveries WHERE id=$1`, did).Scan(&state)
	if state != "cancelled" {
		t.Fatalf("orphan state = %q, want cancelled", state)
	}
}
```

- [ ] **Step 5: Add cancellation to the soft-delete methods + `CancelOrphaned`**

In `internal/store/admin.go`, in `SoftDeleteEndpoint`, after the subscriptions update and before `tx.Commit`, add the delivery cancel:
```go
	if _, err := tx.Exec(ctx,
		`UPDATE deliveries SET state='cancelled', lease_until=NULL, updated_at=now()
		 WHERE endpoint_id=$1 AND state IN ('pending','retry_scheduled')`, id); err != nil {
		return err
	}
```
Rewrite `SoftDeleteSubscription` to use a tx and cancel the sub's deliveries:
```go
func (s *Store) SoftDeleteSubscription(ctx context.Context, id string) error {
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	ct, err := tx.Exec(ctx, `UPDATE subscriptions SET deleted_at=now() WHERE id=$1 AND deleted_at IS NULL`, id)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	if _, err := tx.Exec(ctx,
		`UPDATE deliveries SET state='cancelled', lease_until=NULL, updated_at=now()
		 WHERE subscription_id=$1 AND state IN ('pending','retry_scheduled')`, id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
```
Add `CancelOrphaned` (bounded; the janitor calls it under an advisory lock in M-A4b):
```go
// cancelOrphanedTx cancels non-terminal deliveries whose subscription is
// soft-deleted — the straggler reconciliation (design §4.2). Bounded by batch
// (FOR UPDATE SKIP LOCKED); honest bound is the reconciliation cadence (a
// just-deleted target may get MORE THAN ONE late attempt within an interval,
// not "at most one"). Takes a tx so the janitor can run it under an advisory
// lock (M-A4b); CancelOrphaned wraps it in its own tx for standalone use.
func cancelOrphanedTx(ctx context.Context, tx pgx.Tx, batch int) (int, error) {
	ct, err := tx.Exec(ctx,
		`UPDATE deliveries SET state='cancelled', lease_until=NULL, updated_at=now()
		 WHERE id IN (
		   SELECT d.id FROM deliveries d
		   JOIN subscriptions s ON s.id = d.subscription_id
		   WHERE s.deleted_at IS NOT NULL AND d.state IN ('pending','retry_scheduled')
		   LIMIT $1 FOR UPDATE OF d SKIP LOCKED)`, batch)
	if err != nil {
		return 0, err
	}
	return int(ct.RowsAffected()), nil
}

func (s *Store) CancelOrphaned(ctx context.Context, batch int) (int, error) {
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	n, err := cancelOrphanedTx(ctx, tx, batch)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return n, nil
}
```
(Ensure `internal/store/admin.go` imports `"github.com/jackc/pgx/v5"`.)

- [ ] **Step 6: Run tests to verify pass**

Run: `go test ./internal/domain/ -run TestCancelled -v && go test -tags integration ./internal/store/ -run 'TestDelete|TestCancelOrphaned' -v`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/domain/state.go internal/domain/state_cancelled_test.go internal/store/admin.go internal/store/cancel_test.go
git commit -m "feat(domain,store): cancelled terminal state + cancel-on-delete + orphan reconciliation"
```

### Task 16: Wire per-delivery `backoff_policy` (defined JSON + panic-guard)

**Files:**
- Create: `internal/backoff/policy_json.go`
- Create: `internal/backoff/policy_json_test.go`
- Modify: `internal/store/claim.go` (return `backoff_policy`)
- Create: `internal/store/backoff_claim_test.go`
- Modify: `internal/worker/worker.go` (use per-delivery policy in `record`)
- Create: `internal/worker/backoff_process_test.go` (proves `Process` applies it)
- Modify: `internal/admin/subscriptions.go` (validate `backoff_policy` at write → 422)

**Interfaces:**
- Produces: `backoff.Validate(raw []byte) error` (write path, 422 on invalid); `backoff.FromJSON(raw []byte, maxAttempts int) Policy` (read path, always safe — clamps, never panics, nil→Default). JSON shape `{"base_ms": int>0, "cap_ms": int>0}` with `cap_ms >= base_ms`; NO `max_attempts` here.
- Modifies: `store.ClaimedDelivery` gains `BackoffPolicy []byte`; `claim.go` SQL adds `sub.backoff_policy`; worker `record()` builds `backoff.FromJSON(d.BackoffPolicy, d.MaxAttempts)` instead of `w.Backoff`.

- [ ] **Step 1: Write the failing backoff test**

```go
package backoff

import (
	"fmt"
	"math"
	"math/rand"
	"testing"
	"time"
)

func TestValidate(t *testing.T) {
	good := []string{`{"base_ms":1000,"cap_ms":60000}`, `{"base_ms":5,"cap_ms":5}`}
	for _, g := range good {
		if err := Validate([]byte(g)); err != nil {
			t.Fatalf("Validate(%s) = %v, want nil", g, err)
		}
	}
	bad := []string{
		`{"base_ms":0,"cap_ms":1}`,                              // non-positive
		`{"base_ms":10,"cap_ms":5}`,                             // cap < base
		`{"base_ms":-1,"cap_ms":1}`,                             // negative
		`{nope`,                                                 // malformed
		`{"base_ms":1,"cap_ms":1,"max_attempts":3}`,             // unknown field
		fmt.Sprintf(`{"base_ms":1,"cap_ms":%d}`, math.MaxInt64), // above MaxPolicyMS
	}
	for _, b := range bad {
		if err := Validate([]byte(b)); err == nil {
			t.Fatalf("Validate(%s) = nil, want error", b)
		}
	}
	if err := Validate(nil); err != nil {
		t.Fatalf("nil policy must be valid (default): %v", err)
	}
}

func TestFromJSONSafeAndClamps(t *testing.T) {
	rnd := rand.New(rand.NewSource(1))
	if got := FromJSON(nil, 8); got != Default() {
		t.Fatalf("nil -> %v, want Default", got)
	}
	p := FromJSON([]byte(`{"base_ms":2000,"cap_ms":120000}`), 5)
	if p.Base != 2*time.Second || p.Cap != 120*time.Second || p.MaxAttempts != 5 {
		t.Fatalf("parsed = %+v", p)
	}
	// hostile values can NEVER reach a panicking ceil: negative AND MaxInt64
	// are both clamped to safe defaults BEFORE the time.Duration multiply.
	for _, raw := range []string{
		`{"base_ms":-9,"cap_ms":-9}`,
		fmt.Sprintf(`{"base_ms":%d,"cap_ms":%d}`, math.MaxInt64, math.MaxInt64),
	} {
		got := FromJSON([]byte(raw), 3)
		if got.Base <= 0 || got.Cap <= 0 || got.Cap > time.Duration(MaxPolicyMS)*time.Millisecond {
			t.Fatalf("clamp failed for %s: %+v", raw, got)
		}
		for n := 1; n <= 45; n++ {
			_ = got.NextDelay(n, 0, rnd) // must not panic at any attempt number
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/backoff/ -run 'TestValidate|TestFromJSON' -v`
Expected: FAIL — `Validate`/`FromJSON` undefined.

- [ ] **Step 3: Write the JSON policy parser**

```go
// internal/backoff/policy_json.go
package backoff

import (
	"bytes"
	"encoding/json"
	"errors"
	"time"
)

// MaxPolicyMS bounds base_ms/cap_ms so the resolved Cap can never overflow
// int64 nanoseconds NOR make NextDelay's `int64(ceil)+1` wrap negative (which
// panics rand.Int63n — see backoff.go). 7 days in ms: far above any sane
// webhook backoff, far below the int64-nanosecond overflow edge.
const MaxPolicyMS int64 = 7 * 24 * 60 * 60 * 1000 // 604_800_000

type policyJSON struct {
	BaseMS *int64 `json:"base_ms"`
	CapMS  *int64 `json:"cap_ms"`
}

// Validate is the write-time check (handler → 422). nil/empty is valid (default).
// Rejects malformed JSON, UNKNOWN FIELDS, missing/non-positive values, values
// above MaxPolicyMS, and cap < base — all checked on the int64 BEFORE any
// time.Duration multiplication (so a hostile value can't overflow during checks).
func Validate(raw []byte) error {
	if len(raw) == 0 {
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var p policyJSON
	if err := dec.Decode(&p); err != nil {
		return errors.New("backoff_policy: malformed JSON or unknown field")
	}
	if p.BaseMS == nil || p.CapMS == nil {
		return errors.New("backoff_policy: base_ms and cap_ms are required")
	}
	if *p.BaseMS <= 0 || *p.CapMS <= 0 {
		return errors.New("backoff_policy: base_ms and cap_ms must be > 0")
	}
	if *p.BaseMS > MaxPolicyMS || *p.CapMS > MaxPolicyMS {
		return errors.New("backoff_policy: base_ms and cap_ms must be <= 604800000 (7 days)")
	}
	if *p.CapMS < *p.BaseMS {
		return errors.New("backoff_policy: cap_ms must be >= base_ms")
	}
	return nil
}

// FromJSON is the read path. It NEVER errors and NEVER yields a policy that can
// overflow/panic NextDelay: base/cap are forced into (0, MaxPolicyMS]. The
// clamp is on the int64 ms BEFORE multiplication, so even math.MaxInt64 input
// is safe. nil/empty/invalid → Default with the given maxAttempts.
func FromJSON(raw []byte, maxAttempts int) Policy {
	d := Default()
	if maxAttempts > 0 {
		d.MaxAttempts = maxAttempts
	}
	if len(raw) == 0 {
		return d
	}
	var p policyJSON
	if err := json.Unmarshal(raw, &p); err != nil || p.BaseMS == nil || p.CapMS == nil {
		return d
	}
	clamp := func(ms int64, def time.Duration) time.Duration {
		if ms <= 0 || ms > MaxPolicyMS { // reject before multiplying
			return def
		}
		return time.Duration(ms) * time.Millisecond
	}
	base := clamp(*p.BaseMS, d.Base)
	cp := clamp(*p.CapMS, d.Cap)
	if cp < base {
		cp = base
	}
	return Policy{Base: base, Cap: cp, MaxAttempts: d.MaxAttempts}
}
```

- [ ] **Step 4: Return `backoff_policy` from the claim**

In `internal/store/claim.go`: add `BackoffPolicy []byte` to `ClaimedDelivery`; add `sub.backoff_policy` to both the inner `RETURNING`-fed SELECT column list and the `Scan`:
```go
	SELECT c.id, c.event_id, c.subscription_id, c.attempt_count, c.claim_version,
	       sub.max_attempts, e.topic, e.payload, ep.id, ep.url, ep.secret_ciphertext, sub.backoff_policy
	...
	).Scan(&d.ID, &d.EventID, &d.SubscriptionID, &d.AttemptCount, &d.ClaimVersion,
		&d.MaxAttempts, &d.Topic, &d.Payload, &d.EndpointID, &d.URL, &d.SecretCiphertext, &d.BackoffPolicy)
```

- [ ] **Step 5: Use the per-delivery policy in the worker**

In `internal/worker/worker.go`, change `record` to resolve the policy per delivery:
```go
func (w *Worker) record(ctx context.Context, d store.ClaimedDelivery, res store.AttemptResult) {
	pol := backoff.FromJSON(d.BackoffPolicy, d.MaxAttempts) // per-delivery (design §4.3); nil → w.Backoff-equivalent default
	switch err := w.Store.CompleteAttempt(ctx, res, pol, d.MaxAttempts); {
	...
```
(`w.Backoff` remains the process default for any caller that does not set a per-delivery policy; `FromJSON(nil, …)` already returns `Default()`, matching today's behavior. Leave the `Worker.Backoff` field in place.)

- [ ] **Step 6: Validate `backoff_policy` at write → 422**

In `internal/admin/subscriptions.go`, in `createSubscription` and `patchSubscription`, before calling the store, validate when a backoff body is present:
```go
	if len(req.BackoffPolicy) > 0 {
		if err := backoff.Validate(req.BackoffPolicy); err != nil {
			httpx.Problem(w, http.StatusUnprocessableEntity, "invalid backoff_policy", err.Error())
			return
		}
	}
```
(Add the `backoff` import.)

- [ ] **Step 7: Write a worker integration test proving per-sub backoff changes scheduling**

```go
//go:build integration

package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/mit112/hookrail/internal/backoff"
	"github.com/mit112/hookrail/internal/domain"
	"github.com/mit112/hookrail/internal/store"
)

func TestClaimReturnsBackoffPolicy(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	keyID, _, _ := s.CreateProducerKey(ctx, "bp")
	epID, _, _ := s.CreateEndpoint(ctx, [32]byte{}, "https://example.com/h", "")
	_, _ = s.CreateSubscriptionFull(ctx, store.SubInput{
		TopicPattern: "bp.*", EndpointID: epID, MaxAttempts: 8,
		BackoffPolicy: []byte(`{"base_ms":1234,"cap_ms":99999}`),
	})
	res, _ := s.IngestEvent(ctx, store.IngestParams{ProducerKeyID: keyID, Topic: "bp.x", Payload: []byte(`{}`)})
	ok, d, err := s.ClaimDelivery(ctx, res.DeliveryIDs[0], 30*time.Second)
	if err != nil || !ok {
		t.Fatalf("claim: %v %v", ok, err)
	}
	pol := backoff.FromJSON(d.BackoffPolicy, d.MaxAttempts)
	if pol.Base != 1234*time.Millisecond {
		t.Fatalf("per-sub base = %v, want 1.234s", pol.Base)
	}
	_ = domain.OutcomeRetryable
}
```

- [ ] **Step 8: Write a WORKER test proving `Process` applies the per-sub policy**

The store test above only proves the claim returns the policy. This proves the worker's
`record()` edit actually uses it (a worker still passing `w.Backoff` would fail here). It goes
through the real `Worker.Process` against a 503 carrying `Retry-After: 3600`: the default policy
(6h cap) would schedule ~1h out, but the per-sub 200ms cap clamps `Retry-After` to 200ms — so a
sub-second `next_attempt_at` can ONLY mean the per-delivery policy flowed through the worker.

```go
//go:build integration

package worker_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mit112/hookrail/internal/store"
)

// seedWithBackoff builds a pipeline whose subscription carries a 200ms cap,
// delivering to url; returns the delivery id. (Mirrors the package `seed`
// helper but sets backoff_policy and uses masterKey() so the worker can decrypt.)
func seedWithBackoff(t *testing.T, s *store.Store, url string) string {
	t.Helper()
	ctx := context.Background()
	keyID, _, err := s.CreateProducerKey(ctx, "bo")
	if err != nil {
		t.Fatal(err)
	}
	epID, _, err := s.CreateEndpoint(ctx, masterKey(), url, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateSubscriptionFull(ctx, store.SubInput{
		TopicPattern: "bo.*", EndpointID: epID, MaxAttempts: 8,
		BackoffPolicy: []byte(`{"base_ms":200,"cap_ms":200}`),
	}); err != nil {
		t.Fatal(err)
	}
	res, err := s.IngestEvent(ctx, store.IngestParams{ProducerKeyID: keyID, Topic: "bo.x", Payload: []byte(`{}`)})
	if err != nil || len(res.DeliveryIDs) != 1 {
		t.Fatalf("ingest: %v", err)
	}
	return res.DeliveryIDs[0]
}

func TestProcessUsesPerSubBackoff(t *testing.T) {
	s := testStore(t)
	recv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "3600") // 1 hour
		w.WriteHeader(503)
	}))
	defer recv.Close()
	id := seedWithBackoff(t, s, recv.URL)

	newWorker(s).Process(context.Background(), id)

	if got := state(t, s, id); got != "retry_scheduled" {
		t.Fatalf("state = %s, want retry_scheduled", got)
	}
	var next time.Time
	_ = s.Pool.QueryRow(context.Background(), `SELECT next_attempt_at FROM deliveries WHERE id=$1`, id).Scan(&next)
	if until := time.Until(next); until > time.Second {
		t.Fatalf("next_attempt_at %v out; per-sub 200ms cap not applied by the worker (default would honor the 1h Retry-After)", until)
	}
}
```

- [ ] **Step 9: Run tests to verify pass**

Run: `go test ./internal/backoff/ -v && go test -tags integration ./internal/store/ -run TestClaimReturnsBackoff -v && go test -tags integration ./internal/worker/ -run 'TestProcess|TestProcessUsesPerSubBackoff' -v`
Expected: PASS (existing worker/claim tests unchanged; the worker now uses the per-delivery policy).

- [ ] **Step 10: Commit**

```bash
git add internal/backoff/policy_json.go internal/backoff/policy_json_test.go internal/store/claim.go internal/store/backoff_claim_test.go internal/worker/worker.go internal/worker/backoff_process_test.go internal/admin/subscriptions.go
git commit -m "feat(backoff,worker): per-delivery backoff_policy with write validation + read-path panic-guard"
```

### Task 17: Best-effort per-worker `rate_limit_rps` (`Registry.SetRate` + `EndpointLimits`)

**Files:**
- Modify: `internal/ratelimit/bucket.go` (`Bucket.SetRate`, `Registry.SetRate`)
- Create: `internal/ratelimit/setrate_test.go`
- Create: `internal/store/limits.go` (`EndpointRateLimits`)
- Create: `internal/worker/limits.go` (`EndpointLimits` loader)
- Modify: `cmd/hookrail-worker/main.go` (start the loader goroutine)

**Interfaces:**
- Produces: `(*Bucket).SetRate(rate, burst float64)`; `(*Registry).SetRate(key string, rate, burst float64)`; `store.EndpointRateLimits(ctx) (map[string]float64, error)` = `MIN(rate_limit_rps)` per endpoint over active, non-deleted subs with a non-null rps; `worker.EndpointLimits{Store, Registry, Interval}` with `Run(ctx)` that periodically pushes `SetRate(endpointID, rps, rps*2)`.
- **Documented limits (design §4.3):** per-worker only (N replicas ≈ N×rate); PATCH takes up to one interval to propagate; explicitly NOT a global cap (P2).

- [ ] **Step 1: Write the failing ratelimit test**

```go
package ratelimit

import (
	"testing"
	"time"
)

func TestRegistrySetRateReconfigures(t *testing.T) {
	r := NewRegistry(1000, 2000)
	key := "ep1"
	// drain the default bucket quickly is unnecessary; just reconfigure to a
	// very low rate and confirm the next bucket honors it.
	r.SetRate(key, 0.0001, 1) // ~1 token, then starved
	now := time.Now()
	if !r.Allow(key, now) {
		t.Fatal("first token should be allowed (burst=1)")
	}
	if r.Allow(key, now) {
		t.Fatal("second immediate call must be denied at the reconfigured low rate")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/ratelimit/ -run TestRegistrySetRate -v`
Expected: FAIL — `SetRate` undefined.

- [ ] **Step 3: Add `SetRate` to bucket + registry**

```go
// append to internal/ratelimit/bucket.go

// SetRate reconfigures an existing bucket's rate and burst (design §4.3:
// per-key reconfiguration; buckets were immutable in P0). Tokens are clamped
// to the new burst so a shrink takes effect immediately.
func (b *Bucket) SetRate(rate, burst float64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.rate = rate
	b.burst = burst
	if b.tokens > burst {
		b.tokens = burst
	}
}

// SetRate gets-or-creates the key's bucket and reconfigures it.
func (r *Registry) SetRate(key string, rate, burst float64) {
	r.mu.Lock()
	b, ok := r.buckets[key]
	if !ok {
		b = NewBucket(rate, burst)
		r.buckets[key] = b
		r.mu.Unlock()
		return
	}
	r.mu.Unlock()
	b.SetRate(rate, burst)
}
```

- [ ] **Step 4: Add the store query**

```go
// internal/store/limits.go
package store

import "context"

// EndpointRateLimits returns the effective per-endpoint rate cap: the MIN
// rate_limit_rps across an endpoint's active, non-deleted subscriptions that
// set one. Endpoints with no rps-bearing sub are absent (keep the default).
func (s *Store) EndpointRateLimits(ctx context.Context) (map[string]float64, error) {
	rows, err := s.Pool.Query(ctx,
		`SELECT endpoint_id, MIN(rate_limit_rps)
		 FROM subscriptions
		 WHERE active AND deleted_at IS NULL AND rate_limit_rps IS NOT NULL
		 GROUP BY endpoint_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]float64{}
	for rows.Next() {
		var ep string
		var rps float64
		if err := rows.Scan(&ep, &rps); err != nil {
			return nil, err
		}
		out[ep] = rps
	}
	return out, rows.Err()
}
```

- [ ] **Step 5: Add the worker loader**

```go
// internal/worker/limits.go
package worker

import (
	"context"
	"log/slog"
	"time"

	"github.com/mit112/hookrail/internal/ratelimit"
	"github.com/mit112/hookrail/internal/store"
)

// EndpointLimits periodically pushes per-endpoint MIN(rate_limit_rps) into the
// worker's in-process Registry (design §4.3). Best-effort and per-worker: this
// is NOT a deployment-wide cap (true global cap is P2). A PATCH propagates
// within one Interval.
type EndpointLimits struct {
	Store        *store.Store
	Registry     *ratelimit.Registry
	Interval     time.Duration // e.g. 15s
	DefaultRate  float64       // worker default rps — what a reverted endpoint goes back to
	DefaultBurst float64
	applied      map[string]struct{} // endpoints currently carrying an override
}

func (e *EndpointLimits) Run(ctx context.Context) {
	t := time.NewTicker(e.Interval)
	defer t.Stop()
	e.Refresh(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			e.Refresh(ctx)
		}
	}
}

// Refresh pulls current per-endpoint limits and reconciles the registry. Exported
// so tests can drive one cycle deterministically.
func (e *EndpointLimits) Refresh(ctx context.Context) {
	limits, err := e.Store.EndpointRateLimits(ctx)
	if err != nil {
		slog.Warn("endpoint limits refresh failed; keeping previous", "err", err)
		return
	}
	if e.applied == nil {
		e.applied = map[string]struct{}{}
	}
	next := make(map[string]struct{}, len(limits))
	for ep, rps := range limits {
		e.Registry.SetRate(ep, rps, rps*2)
		next[ep] = struct{}{}
	}
	// Reconcile removals: an endpoint whose last limiting sub was paused,
	// deleted, or had rps cleared drops out of the query — revert its bucket to
	// the worker default, else it stays throttled at the old rate forever.
	for ep := range e.applied {
		if _, still := next[ep]; !still {
			e.Registry.SetRate(ep, e.DefaultRate, e.DefaultBurst)
		}
	}
	e.applied = next
}
```

- [ ] **Step 6: Start the loader in the worker main**

In `cmd/hookrail-worker/main.go`, hoist the default rps/burst into vars so both the registry and the loader share them, then start the loader. Replace the existing `limits := ratelimit.NewRegistry(...)` line with:
```go
	defRPS := envFloat("HOOKRAIL_DEFAULT_RPS", 1000)
	defBurst := envFloat("HOOKRAIL_DEFAULT_BURST", 2000)
	limits := ratelimit.NewRegistry(defRPS, defBurst)
	el := &worker.EndpointLimits{Store: s, Registry: limits, Interval: 15 * time.Second, DefaultRate: defRPS, DefaultBurst: defBurst}
	go el.Run(ctx)
```

- [ ] **Step 7: Write the store MIN-aggregation test**

```go
//go:build integration

package store_test

import (
	"context"
	"testing"

	"github.com/mit112/hookrail/internal/store"
)

func TestEndpointRateLimits(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	epID, _, _ := s.CreateEndpoint(ctx, [32]byte{}, "https://example.com/h", "")
	r1, r2 := 10.0, 4.0
	_, _ = s.CreateSubscriptionFull(ctx, store.SubInput{TopicPattern: "a.*", EndpointID: epID, MaxAttempts: 3, RateLimitRPS: &r1})
	subLow, _ := s.CreateSubscriptionFull(ctx, store.SubInput{TopicPattern: "b.*", EndpointID: epID, MaxAttempts: 3, RateLimitRPS: &r2})

	m, err := s.EndpointRateLimits(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if m[epID] != 4.0 {
		t.Fatalf("MIN rps = %v, want 4 (min of 10,4)", m[epID])
	}
	// soft-delete the lower sub → MIN should rise to 10
	if err := s.SoftDeleteSubscription(ctx, subLow); err != nil {
		t.Fatal(err)
	}
	m, _ = s.EndpointRateLimits(ctx)
	if m[epID] != 10.0 {
		t.Fatalf("MIN rps after delete = %v, want 10", m[epID])
	}
}
```

- [ ] **Step 8: Write the loader stale-reset unit test**

```go
//go:build integration

package worker_test

import (
	"context"
	"testing"
	"time"

	"github.com/mit112/hookrail/internal/ratelimit"
	"github.com/mit112/hookrail/internal/store"
	"github.com/mit112/hookrail/internal/worker"
)

// Reuses the worker package's existing integration testStore (worker_test.go).
// time is driven explicitly: the token bucket only accrues on elapsed wall
// time (bucket.go Allow), and SetRate does NOT refill — so the post-reset
// checks MUST advance the clock or they would all see 0 tokens.
func TestEndpointLimitsRevertsRemoved(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	epID, _, _ := s.CreateEndpoint(ctx, [32]byte{}, "https://example.com/h", "")
	low := 1.0
	subID, _ := s.CreateSubscriptionFull(ctx, store.SubInput{TopicPattern: "z.*", EndpointID: epID, MaxAttempts: 3, RateLimitRPS: &low})

	reg := ratelimit.NewRegistry(1000, 2000)
	el := &worker.EndpointLimits{Store: s, Registry: reg, Interval: time.Hour, DefaultRate: 1000, DefaultBurst: 2000}
	el.Refresh(ctx) // applies the 1 rps / burst 2 override for epID

	// throttled to ~1 rps: burst 2 allowed at t0, third denied (no time elapsed)
	t0 := time.Now()
	if !reg.Allow(epID, t0) || !reg.Allow(epID, t0) {
		t.Fatal("burst of 2 should be allowed")
	}
	if reg.Allow(epID, t0) {
		t.Fatal("endpoint should be throttled to its 1 rps override after burst")
	}
	// remove the only limiting sub, refresh → bucket reverts to the 1000 default.
	_ = s.SoftDeleteSubscription(ctx, subID)
	el.Refresh(ctx)
	// advance 1s so the reverted 1000 rps bucket accrues ~1000 tokens (capped at
	// the 2000 burst); the old 1 rps bucket would only accrue 1 in the same second.
	t1 := t0.Add(time.Second)
	allowed := 0
	for i := 0; i < 50; i++ {
		if reg.Allow(epID, t1) {
			allowed++
		}
	}
	if allowed < 50 {
		t.Fatalf("after removal the endpoint stayed throttled (allowed=%d/50) — stale override not reverted", allowed)
	}
}
```
Export the refresh hook: rename `refresh` → `Refresh` in `internal/worker/limits.go` and have `Run` call `e.Refresh` (done in Step 5). No new harness file is needed — `internal/worker/worker_test.go` already defines `testStore`.

- [ ] **Step 9: Run tests to verify pass**

Run: `go test ./internal/ratelimit/ -v && go test -tags integration ./internal/store/ -run TestEndpointRateLimits -v && go test -tags integration ./internal/worker/ -run TestEndpointLimitsReverts -v && go build ./...`
Expected: PASS + clean build.

- [ ] **Step 8: Commit**

```bash
git add internal/ratelimit/bucket.go internal/ratelimit/setrate_test.go internal/store/limits.go internal/store/limits_test.go internal/worker/limits.go internal/worker/limits_test.go cmd/hookrail-worker/main.go
git commit -m "feat(ratelimit,worker): best-effort per-worker rate_limit_rps via Registry.SetRate + EndpointLimits loader with stale-override reconcile"
```

### Task 18: Worker dead-letter writes `endpoint_id`

**Files:**
- Modify: `internal/store/attempt.go` (both `dead_letters` inserts set `endpoint_id`)
- Create: `internal/store/deadletter_endpointid_test.go`

**Interfaces:**
- Modifies: `CompleteAttempt` and `DeadLetterExhausted` populate `dead_letters.endpoint_id` (from the delivery row) so the endpoint-scoped DLQ keyset (design §2.1) works for newly dead-lettered rows, not just backfilled ones.

- [ ] **Step 1: Write the failing test**

```go
//go:build integration

package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/mit112/hookrail/internal/backoff"
	"github.com/mit112/hookrail/internal/domain"
	"github.com/mit112/hookrail/internal/store"
)

func TestDeadLetterCarriesEndpointID(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	id := mkDelivery(t, s)
	ok, d, err := s.ClaimDelivery(ctx, id, 30*time.Second)
	if err != nil || !ok {
		t.Fatalf("claim: %v %v", ok, err)
	}
	// permanent failure → dead-lettered
	err = s.CompleteAttempt(ctx, store.AttemptResult{
		DeliveryID: d.ID, AttemptNo: d.AttemptCount, ClaimVersion: d.ClaimVersion,
		Outcome: domain.OutcomePermanent, ErrorClass: "permanent", RequestedAt: time.Now(), CompletedAt: time.Now(),
	}, backoff.Default(), d.MaxAttempts)
	if err != nil {
		t.Fatal(err)
	}
	var epID *string
	_ = s.Pool.QueryRow(ctx, `SELECT endpoint_id FROM dead_letters WHERE delivery_id=$1`, id).Scan(&epID)
	if epID == nil || *epID == "" {
		t.Fatal("dead_letters.endpoint_id not populated by worker dead-letter")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -tags integration ./internal/store/ -run TestDeadLetterCarriesEndpointID -v`
Expected: FAIL — `endpoint_id` is NULL on the new dead-letter row.

- [ ] **Step 3: Set `endpoint_id` in both dead-letter inserts**

In `internal/store/attempt.go`, change the `CompleteAttempt` dead-letter upsert to source `endpoint_id` from the delivery:
```go
		if _, err := tx.Exec(ctx,
			`INSERT INTO dead_letters (delivery_id, final_error, endpoint_id)
			 SELECT $1, $2, endpoint_id FROM deliveries WHERE id = $1
			 ON CONFLICT (delivery_id) DO UPDATE
			   SET final_error = EXCLUDED.final_error, dead_at = now(), replayed_at = NULL,
			       endpoint_id = EXCLUDED.endpoint_id`,
			r.DeliveryID, r.ErrorClass); err != nil {
			return err
		}
```
Make the same change in `DeadLetterExhausted`:
```go
	if _, err := tx.Exec(ctx,
		`INSERT INTO dead_letters (delivery_id, final_error, endpoint_id)
		 SELECT $1, 'attempts_exhausted', endpoint_id FROM deliveries WHERE id = $1
		 ON CONFLICT (delivery_id) DO UPDATE
		   SET final_error = EXCLUDED.final_error, dead_at = now(), replayed_at = NULL,
		       endpoint_id = EXCLUDED.endpoint_id`, id); err != nil {
		return err
	}
```

- [ ] **Step 4: Run tests to verify pass**

Run: `go test -tags integration ./internal/store/ -run 'TestDeadLetter|TestComplete|TestAttempt' -v`
Expected: PASS (existing attempt tests unchanged; new endpoint_id populated).

- [ ] **Step 5: Commit**

```bash
git add internal/store/attempt.go internal/store/deadletter_endpointid_test.go
git commit -m "feat(store): worker dead-letter inserts carry endpoint_id for endpoint-scoped DLQ"
```

> **M-A4a gate — Codex gpt-5.5-xhigh pre-gate, THEN Opus.** Codex pre-pass focuses on the hot-path edits: claim SQL still fences on `claim_version`; `FromJSON` can never panic `NextDelay` (cap/base forced > 0); cancellation cannot touch `in_flight`; `SetRate` does not deadlock the registry mutex (note the unlock-before-`b.SetRate` ordering). Opus gate then runs full VERIFY and confirms no existing claim/attempt/worker test was weakened.

---

## Milestone M-A4b — Retention janitor (Tasks 19–21) · Codex pre-gate + Opus

The §5 exit-gate work: three data jobs + the cancel-orphaned pass, each bounded and advisory-locked, plus `ctl retention --once` and metrics. **Codex pre-gate** again (hot-path-adjacent + the tombstone-safety argument is the subtlest correctness claim in the slice).

### Task 19: Retention store functions

**Files:**
- Create: `internal/store/retention.go`
- Create: `internal/store/retention_test.go`

**Interfaces:**
- Produces: `TombstoneEventPayloads(ctx, age time.Duration, batch int) (int, error)`; `TrimDeliveryAttempts(ctx, attemptAge time.Duration, batch int) (int, error)` (sets `deliveries.attempts_truncated_at` in the SAME tx); `PurgeIdempotency(ctx, batch int) (int, error)` (bounded); `CancelOrphanedLocked(ctx, batch int) (int, error)`; each wraps its work in `BEGIN; SELECT pg_advisory_xact_lock(<key>); <bounded UPDATE/DELETE ... FOR UPDATE SKIP LOCKED>; COMMIT` (transaction-scoped lock, design §3, F9). All four passes are bounded + advisory-locked.
- **Tombstone safety (design §3, F1):** job 1 empties a payload only when no delivery is live AND no dead-letter is still replayable (un-replayed within `$age`). The same `$age` couples to replay-expiry (§4.1) so a replayable dead-letter never has an emptied payload.

- [ ] **Step 1: Write the failing test**

```go
//go:build integration

package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/mit112/hookrail/internal/store"
)

func TestTombstoneSkipsReplayableDeadLetter(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	keyID := seedPipeline(t, s, "tomb.*")
	res, _ := s.IngestEvent(ctx, store.IngestParams{ProducerKeyID: keyID, Topic: "tomb.x", Payload: []byte(`{"big":"payload"}`)})
	eid := func() string { var e string; _ = s.Pool.QueryRow(ctx, `SELECT event_id FROM deliveries WHERE id=$1`, res.DeliveryIDs[0]).Scan(&e); return e }()
	did := res.DeliveryIDs[0]
	// old event, but recently re-dead-lettered and NOT yet replayed (the re-dead-letter hole)
	_, _ = s.Pool.Exec(ctx, `UPDATE events SET created_at = now() - interval '60 days' WHERE id=$1`, eid)
	_, _ = s.Pool.Exec(ctx, `UPDATE deliveries SET state='dead_lettered' WHERE id=$1`, did)
	_, _ = s.Pool.Exec(ctx, `INSERT INTO dead_letters (delivery_id, final_error, endpoint_id, dead_at, replayed_at)
	                         SELECT id,'x',endpoint_id, now(), NULL FROM deliveries WHERE id=$1`, did)

	n, err := s.TombstoneEventPayloads(ctx, 30*24*time.Hour, 1000)
	if err != nil {
		t.Fatal(err)
	}
	var size int
	_ = s.Pool.QueryRow(ctx, `SELECT payload_size FROM events WHERE id=$1`, eid).Scan(&size)
	if size == 0 {
		t.Fatalf("tombstoned a replayable dead-letter's payload (n=%d) — F1 violation", n)
	}
}

func TestTombstoneEmptiesOldSettledPayload(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	keyID := seedPipeline(t, s, "set.*")
	res, _ := s.IngestEvent(ctx, store.IngestParams{ProducerKeyID: keyID, Topic: "set.x", Payload: []byte(`{"big":"payload"}`)})
	did := res.DeliveryIDs[0]
	var eid string
	_ = s.Pool.QueryRow(ctx, `SELECT event_id FROM deliveries WHERE id=$1`, did).Scan(&eid)
	_, _ = s.Pool.Exec(ctx, `UPDATE events SET created_at = now() - interval '60 days' WHERE id=$1`, eid)
	_, _ = s.Pool.Exec(ctx, `UPDATE deliveries SET state='succeeded' WHERE id=$1`, did)

	if _, err := s.TombstoneEventPayloads(ctx, 30*24*time.Hour, 1000); err != nil {
		t.Fatal(err)
	}
	var size int
	_ = s.Pool.QueryRow(ctx, `SELECT payload_size FROM events WHERE id=$1`, eid).Scan(&size)
	if size != 0 {
		t.Fatalf("old settled payload not tombstoned: size=%d", size)
	}
}

func TestTrimAttemptsSetsDurableMarker(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	id := mkDelivery(t, s)
	// one old attempt row
	_, _ = s.Pool.Exec(ctx,
		`INSERT INTO delivery_attempts (delivery_id, attempt_no, claim_version, status, requested_at, completed_at)
		 VALUES ($1, 1, 1, 'retryable_failure', now()-interval '60 days', now()-interval '60 days')`, id)
	n, err := s.TrimDeliveryAttempts(ctx, 30*24*time.Hour, 1000)
	if err != nil || n == 0 {
		t.Fatalf("trim = %d, %v", n, err)
	}
	var marked *time.Time
	_ = s.Pool.QueryRow(ctx, `SELECT attempts_truncated_at FROM deliveries WHERE id=$1`, id).Scan(&marked)
	if marked == nil {
		t.Fatal("attempts_truncated_at not set in the trim tx (F12)")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -tags integration ./internal/store/ -run 'TestTombstone|TestTrim' -v`
Expected: FAIL — retention funcs undefined.

- [ ] **Step 3: Write the retention store funcs**

```go
// internal/store/retention.go
package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// advisory lock keys (transaction-scoped; design §3, F9). Distinct per job so
// jobs don't serialize against each other unnecessarily.
const (
	lockTombstone = 0x484b_0001 // "HK"+1
	lockTrim      = 0x484b_0002
	lockIdem      = 0x484b_0003
	lockOrphan    = 0x484b_0004
)

func (s *Store) withAdvisoryTx(ctx context.Context, key int64, fn func(pgx.Tx) (int, error)) (int, error) {
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, key); err != nil {
		return 0, err
	}
	n, err := fn(tx)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return n, nil
}

// TombstoneEventPayloads empties payloads of OLD events with NO live delivery
// AND NO still-replayable dead-letter (design §3 job 1 / F1). age = replay
// expiry = RETENTION_EVENT_PAYLOAD_DAYS.
func (s *Store) TombstoneEventPayloads(ctx context.Context, age time.Duration, batch int) (int, error) {
	return s.withAdvisoryTx(ctx, lockTombstone, func(tx pgx.Tx) (int, error) {
		ct, err := tx.Exec(ctx,
			`UPDATE events SET payload='{}', payload_size=0
			 WHERE id IN (
			   SELECT e.id FROM events e
			   WHERE e.payload_size > 0 AND e.created_at < now() - $1::interval
			     AND NOT EXISTS (
			       SELECT 1 FROM deliveries d
			       WHERE d.event_id = e.id AND (
			         d.state IN ('pending','retry_scheduled','in_flight')
			         OR (d.state = 'dead_lettered' AND EXISTS (
			               SELECT 1 FROM dead_letters dl
			               WHERE dl.delivery_id = d.id AND dl.replayed_at IS NULL
			                 AND dl.dead_at >= now() - $1::interval))))
			   LIMIT $2 FOR UPDATE SKIP LOCKED)`,
			age, batch)
		if err != nil {
			return 0, err
		}
		return int(ct.RowsAffected()), nil
	})
}

// TrimDeliveryAttempts deletes old attempt rows and, in the SAME tx, stamps
// attempts_truncated_at on each affected delivery so the marker is durable
// (design §3 job 2 / F12).
func (s *Store) TrimDeliveryAttempts(ctx context.Context, attemptAge time.Duration, batch int) (int, error) {
	return s.withAdvisoryTx(ctx, lockTrim, func(tx pgx.Tx) (int, error) {
		rows, err := tx.Query(ctx,
			`WITH doomed AS (
			   SELECT id, delivery_id FROM delivery_attempts
			   WHERE completed_at < now() - $1::interval
			   LIMIT $2 FOR UPDATE SKIP LOCKED),
			 del AS (DELETE FROM delivery_attempts WHERE id IN (SELECT id FROM doomed) RETURNING delivery_id),
			 mark AS (UPDATE deliveries SET attempts_truncated_at = now()
			          WHERE id IN (SELECT DISTINCT delivery_id FROM del))
			 SELECT count(*) FROM del`,
			attemptAge, batch)
		if err != nil {
			return 0, err
		}
		defer rows.Close()
		var n int
		if rows.Next() {
			if err := rows.Scan(&n); err != nil {
				return 0, err
			}
		}
		return n, rows.Err()
	})
}

// PurgeIdempotency deletes expired idempotency keys, BOUNDED by batch (design
// §3 job 3 — every pass is bounded + SKIP LOCKED, no unbounded DELETE).
func (s *Store) PurgeIdempotency(ctx context.Context, batch int) (int, error) {
	return s.withAdvisoryTx(ctx, lockIdem, func(tx pgx.Tx) (int, error) {
		ct, err := tx.Exec(ctx,
			`DELETE FROM idempotency_keys
			 WHERE (producer_key_id, idem_key) IN (
			   SELECT producer_key_id, idem_key FROM idempotency_keys
			   WHERE expires_at < now()
			   LIMIT $1 FOR UPDATE SKIP LOCKED)`, batch)
		if err != nil {
			return 0, err
		}
		return int(ct.RowsAffected()), nil
	})
}

// CancelOrphanedLocked runs the §4.2 reconciliation under the same transaction-
// scoped advisory lock pattern as the other jobs (design §3). cancelOrphanedTx
// is defined in Task 15.
func (s *Store) CancelOrphanedLocked(ctx context.Context, batch int) (int, error) {
	return s.withAdvisoryTx(ctx, lockOrphan, func(tx pgx.Tx) (int, error) {
		return cancelOrphanedTx(ctx, tx, batch)
	})
}
```
Every retention pass is now bounded (`LIMIT $batch`) and advisory-locked (transaction-scoped), per the design contract — no unbounded DELETE, no unlocked job.

- [ ] **Step 4: Run tests to verify pass**

Run: `go test -tags integration ./internal/store/ -run 'TestTombstone|TestTrim|TestPurge' -v`
Expected: PASS — replayable dead-letter NOT tombstoned; old settled payload tombstoned; trim sets the durable marker.

- [ ] **Step 5: Commit**

```bash
git add internal/store/retention.go internal/store/retention_test.go
git commit -m "feat(store): retention funcs — tombstone (replayable-safe), attempt trim+marker, idempotency purge (advisory-locked)"
```

### Task 20: Scheduler retention janitor goroutine + metrics

**Files:**
- Create: `internal/scheduler/retention.go`
- Create: `internal/scheduler/retention_test.go`
- Modify: `internal/obs/metrics.go` (retention metrics)
- Modify: `cmd/hookrail-scheduler/main.go` (start the janitor)

**Interfaces:**
- Produces: `scheduler.Janitor` with `Run(ctx)` — a ticker (second goroutine alongside the sweeper) that each tick runs the four passes within a wall-clock budget (`RETENTION_TICK_BUDGET`), advancing `obs.RetentionRowsPruned{job}` and `obs.RetentionTickSeconds`. Disabled when `RETENTION_ENABLED=false`.
- Consumes: the Task 19 store funcs + `CancelOrphaned`.

- [ ] **Step 1: Write the failing test**

```go
//go:build integration

package scheduler_test

import (
	"context"
	"testing"
	"time"

	"github.com/mit112/hookrail/internal/scheduler"
	"github.com/mit112/hookrail/internal/store"
)

// uses the scheduler package's own testStore harness (mirror the store one).
func TestJanitorRunOnce(t *testing.T) {
	s := schedTestStore(t)
	ctx := context.Background()
	// seed one old settled event so tombstone has work
	keyID := schedSeed(t, s, "jan.*")
	res, _ := s.IngestEvent(ctx, store.IngestParams{ProducerKeyID: keyID, Topic: "jan.x", Payload: []byte(`{"x":"y"}`)})
	did := res.DeliveryIDs[0]
	var eid string
	_ = s.Pool.QueryRow(ctx, `SELECT event_id FROM deliveries WHERE id=$1`, did).Scan(&eid)
	_, _ = s.Pool.Exec(ctx, `UPDATE events SET created_at=now()-interval '60 days' WHERE id=$1`, eid)
	_, _ = s.Pool.Exec(ctx, `UPDATE deliveries SET state='succeeded' WHERE id=$1`, did)

	j := &scheduler.Janitor{
		Store: s, PayloadAge: 30 * 24 * time.Hour, AttemptAge: 30 * 24 * time.Hour,
		Batch: 1000, TickBudget: 30 * time.Second,
	}
	if err := j.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	var size int
	_ = s.Pool.QueryRow(ctx, `SELECT payload_size FROM events WHERE id=$1`, eid).Scan(&size)
	if size != 0 {
		t.Fatalf("janitor did not tombstone old payload: size=%d", size)
	}
}
```
Create `internal/scheduler/testutil_test.go` with the harness (one shared PG container, fresh DB per test):
```go
//go:build integration

package scheduler_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/mit112/hookrail/internal/store"
)

var (
	schedOnce sync.Once
	schedDSN  string
	schedSeq  atomic.Int64
)

func schedTestStore(t *testing.T) *store.Store {
	t.Helper()
	ctx := context.Background()
	schedOnce.Do(func() {
		pgc, err := tcpostgres.Run(ctx, "postgres:16-alpine",
			tcpostgres.WithDatabase("hookrail"), tcpostgres.WithUsername("hookrail"), tcpostgres.WithPassword("hookrail"),
			testcontainers.WithWaitStrategy(wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).WithStartupTimeout(60*time.Second)))
		if err != nil {
			t.Fatalf("pg container: %v", err)
		}
		schedDSN, err = pgc.ConnectionString(ctx, "sslmode=disable")
		if err != nil {
			t.Fatal(err)
		}
	})
	name := fmt.Sprintf("hookrail_s%d", schedSeq.Add(1))
	admin, err := pgx.Connect(ctx, schedDSN)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	_ = admin.Close(ctx)
	s, err := store.Open(ctx, strings.Replace(schedDSN, "/hookrail?", "/"+name+"?", 1))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s
}

// schedSeed creates a producer key + endpoint + one subscription on pattern.
func schedSeed(t *testing.T, s *store.Store, pattern string) string {
	t.Helper()
	ctx := context.Background()
	keyID, _, err := s.CreateProducerKey(ctx, "sched")
	if err != nil {
		t.Fatal(err)
	}
	epID, _, err := s.CreateEndpoint(ctx, [32]byte{}, "https://example.com/h", "seed")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateSubscriptionFull(ctx, store.SubInput{TopicPattern: pattern, EndpointID: epID, MaxAttempts: 3}); err != nil {
		t.Fatal(err)
	}
	return keyID
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -tags integration ./internal/scheduler/ -run TestJanitorRunOnce -v`
Expected: FAIL — `Janitor` undefined.

- [ ] **Step 3: Add the retention metrics**

In `internal/obs/metrics.go`, register:
```go
	RetentionRowsPruned = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "hookrail_retention_rows_pruned_total", Help: "Rows pruned by the retention janitor, by job."},
		[]string{"job"})
	RetentionTickSeconds = promauto.NewHistogram(prometheus.HistogramOpts{
		Name: "hookrail_retention_tick_seconds", Help: "Wall-clock duration of one retention tick."})
```
(Match the existing var-block + `promauto` style already used in `metrics.go`.)

- [ ] **Step 4: Write the janitor**

```go
// internal/scheduler/retention.go
package scheduler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/mit112/hookrail/internal/obs"
	"github.com/mit112/hookrail/internal/store"
)

type Janitor struct {
	Store      *store.Store
	PayloadAge time.Duration // RETENTION_EVENT_PAYLOAD_DAYS
	AttemptAge time.Duration // RETENTION_ATTEMPT_DAYS
	Batch      int           // RETENTION_BATCH
	Interval   time.Duration // RETENTION_INTERVAL
	TickBudget time.Duration // RETENTION_TICK_BUDGET
}

// RunOnce executes all four passes once, each capped by the tick budget. It
// AGGREGATES failures (and budget expiry) into a returned error so the CLI can
// exit nonzero and the scheduler can log honestly — a job failure must never
// surface as success (design honesty).
func (j *Janitor) RunOnce(ctx context.Context) error {
	start := time.Now()
	defer func() { obs.RetentionTickSeconds.Observe(time.Since(start).Seconds()) }()
	bctx, cancel := context.WithTimeout(ctx, j.TickBudget)
	defer cancel()

	var errs []error
	run := func(job string, fn func() (int, error)) {
		n, err := fn()
		if err != nil {
			slog.Warn("retention job failed", "job", job, "err", err)
			errs = append(errs, fmt.Errorf("%s: %w", job, err))
			return
		}
		if n > 0 {
			obs.RetentionRowsPruned.WithLabelValues(job).Add(float64(n))
			slog.Info("retention pruned", "job", job, "rows", n)
		}
	}
	run("tombstone", func() (int, error) { return j.Store.TombstoneEventPayloads(bctx, j.PayloadAge, j.Batch) })
	run("attempt_trim", func() (int, error) { return j.Store.TrimDeliveryAttempts(bctx, j.AttemptAge, j.Batch) })
	run("idempotency", func() (int, error) { return j.Store.PurgeIdempotency(bctx, j.Batch) })
	run("cancel_orphaned", func() (int, error) { return j.Store.CancelOrphanedLocked(bctx, j.Batch) })
	if err := bctx.Err(); err != nil {
		errs = append(errs, fmt.Errorf("tick budget: %w", err))
	}
	return errors.Join(errs...)
}

// Run ticks on the interval, running immediately on startup. A failing tick is
// logged (the scheduler keeps running); the CLI path treats it as a hard error.
func (j *Janitor) Run(ctx context.Context) {
	if err := j.RunOnce(ctx); err != nil {
		slog.Error("retention tick had failures", "err", err)
	}
	t := time.NewTicker(j.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := j.RunOnce(ctx); err != nil {
				slog.Error("retention tick had failures", "err", err)
			}
		}
	}
}
```

- [ ] **Step 5: Start the janitor in the scheduler main**

In `cmd/hookrail-scheduler/main.go`, after the sweeper is constructed and before `sw.Run(ctx)` (which blocks), start the janitor in a goroutine when enabled:
```go
	if cfg.RetentionEnabled {
		j := &scheduler.Janitor{
			Store: s, PayloadAge: cfg.EventPayloadRetention, AttemptAge: cfg.AttemptRetention,
			Batch: cfg.RetentionBatch, Interval: cfg.RetentionInterval, TickBudget: cfg.RetentionTickBudget,
		}
		go j.Run(ctx)
		slog.Info("retention janitor started", "interval", cfg.RetentionInterval)
	}
```

- [ ] **Step 6: Run tests to verify pass**

Run: `go test -tags integration ./internal/scheduler/ -run TestJanitor -v && go build ./...`
Expected: PASS + clean build.

- [ ] **Step 7: Commit**

```bash
git add internal/scheduler/retention.go internal/scheduler/retention_test.go internal/scheduler/testutil_test.go internal/obs/metrics.go cmd/hookrail-scheduler/main.go
git commit -m "feat(scheduler): retention janitor goroutine (4 passes, tick budget) + retention metrics"
```

### Task 21: `hookrail-ctl retention --once` + side-effect-free help dispatch

**Files:**
- Modify: `cmd/hookrail-ctl/main.go`
- Create: `cmd/hookrail-ctl/help_test.go`

**Interfaces:**
- Produces: `hookrail-ctl retention --once` runs one janitor tick and exits (ops escape hatch; design §3); reuses `scheduler.Janitor.RunOnce` and exits **nonzero** on any job failure (Finding 9). Also fixes a P0 footgun: `migrate --help` currently calls `config.Load`+`store.Open`+`Migrate` BEFORE parsing help — a doc query could mutate the schema. This task adds a centralized help guard so `--help`/`-h`/`help` (top-level or per-subcommand) exits 0 with NO config/DB access.

- [ ] **Step 1: Write the failing subprocess test (help must not touch config/DB)**

```go
// cmd/hookrail-ctl/help_test.go
package main_test

import (
	"os/exec"
	"path/filepath"
	"testing"
)

// TestCtlHelpHasNoSideEffects builds the binary and runs help variants with an
// EMPTY environment. config.Load() would fail (no DATABASE_URL) and exit 1 if
// reached, so a clean exit 0 proves help short-circuits before any side effect.
func TestCtlHelpHasNoSideEffects(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "hookrail-ctl")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	for _, args := range [][]string{
		{"--help"}, {"-h"}, {"help"},
		{"migrate", "--help"}, {"seed", "--help"}, {"retention", "--help"},
	} {
		cmd := exec.Command(bin, args...)
		cmd.Env = []string{} // no env at all: any config.Load/store.Open would fail nonzero
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("ctl %v: help must exit 0 with no env, got %v\n%s", args, err, out)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/hookrail-ctl/ -run TestCtlHelp -v`
Expected: FAIL — `migrate --help` exits nonzero (config.Load fails on empty env before help).

- [ ] **Step 3: Add the centralized help guard + the retention subcommand**

In `cmd/hookrail-ctl/main.go`, add helpers and intercept help at the very top of `main`, before any branch:
```go
func usage() {
	fmt.Fprintln(os.Stderr, "usage: hookrail-ctl <seed|migrate|retention> [flags]")
}

func wantsHelp(args []string) bool {
	for _, a := range args {
		if a == "--help" || a == "-h" || a == "help" {
			return true
		}
	}
	return false
}
```
At the very start of `main` (replacing the current `if len(os.Args) < 2 {...}` guard):
```go
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	if wantsHelp(os.Args[1:]) { // help NEVER loads config or opens the DB
		usage()
		os.Exit(0)
	}
```
Then add the `retention` branch alongside `migrate`/`seed` (help is already handled above, so this branch only runs for the real command):
```go
	if os.Args[1] == "retention" {
		fs := flag.NewFlagSet("retention", flag.ExitOnError)
		once := fs.Bool("once", false, "run one retention tick and exit")
		_ = fs.Parse(os.Args[2:])
		if !*once {
			fmt.Fprintln(os.Stderr, "usage: hookrail-ctl retention --once")
			os.Exit(2)
		}
		cfg, err := config.Load()
		if err != nil {
			slog.Error("config", "err", err)
			os.Exit(1)
		}
		s, err := store.Open(context.Background(), cfg.DatabaseURL)
		if err != nil {
			slog.Error("store", "err", err)
			os.Exit(1)
		}
		defer s.Close()
		j := &scheduler.Janitor{
			Store: s, PayloadAge: cfg.EventPayloadRetention, AttemptAge: cfg.AttemptRetention,
			Batch: cfg.RetentionBatch, TickBudget: cfg.RetentionTickBudget,
		}
		if err := j.RunOnce(context.Background()); err != nil {
			slog.Error("retention had failures", "err", err) // RunOnce aggregates job errors + budget
			os.Exit(1)
		}
		fmt.Println("retention tick complete")
		return
	}
```
Add the `scheduler` import; update the remaining usage strings to mention `retention`.

- [ ] **Step 4: Run tests to verify pass**

Run: `go test ./cmd/hookrail-ctl/ -run TestCtlHelp -v && go build ./... && go test -tags integration ./internal/scheduler/ -v`
Expected: PASS — every help variant exits 0 with no env; janitor tests still green.

- [ ] **Step 5: Commit**

```bash
git add cmd/hookrail-ctl/main.go cmd/hookrail-ctl/help_test.go
git commit -m "feat(ctl): retention --once (nonzero on failure) + side-effect-free help dispatch"
```

> **M-A4b gate — Codex gpt-5.5-xhigh pre-gate, THEN Opus.** Codex pre-pass scrutinizes the tombstone-safety SQL (no payload emptied while a dead-letter is replayable; same `$age` as replay-expiry), the transaction-scoped advisory lock (lock+work+commit share one pooled conn), `FOR UPDATE SKIP LOCKED` bounding, and the `TrimDeliveryAttempts` CTE setting the durable marker in the same tx. Opus gate runs full VERIFY.

---

## Milestone M-A5 — Contract, deploy wiring, e2e, docs (Tasks 22–25)

Extend (do NOT fork) the OpenAPI contract with schema-level conformance, wire the admin + scheduler scrape into compose, add an admin happy-path e2e, and document the admin/retention surface honestly.

### Task 22: Extend `api/openapi.yaml` (admin scheme + per-op servers + `cancelled` enum) + conformance

**Files:**
- Modify: `api/openapi.yaml`
- Create: `internal/admin/openapi_conformance_test.go`

**Interfaces:**
- Produces: an `adminToken` bearer scheme applied per-operation to `/v1/*` admin paths (root stays `producerKey`); a per-operation `servers:` override pointing admin ops at `:8082` while ingress stays `:8080` (design §6, N); `'cancelled'` added to the delivery-state enum. Conformance = **schema-level request/response validation** (kin-openapi), not a path-registration check.

- [ ] **Step 1: Write the failing conformance test**

```go
//go:build integration

package admin_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

// validateOpResponse does SCHEMA-LEVEL conformance directly against the
// operation's response schema (design §6). It deliberately does NOT use
// gorillamux: that router consumes document/path-item servers, NOT
// Operation.Servers, so it cannot prove the per-op :8082 override. Instead we
// assert the per-op server explicitly AND validate the body via Schema.VisitJSON.
func validateOpResponse(t *testing.T, doc *openapi3.T, method, path string, status int, body []byte) {
	t.Helper()
	pi := doc.Paths.Value(path)
	if pi == nil {
		t.Fatalf("path %s not in spec", path)
	}
	op := pi.GetOperation(method)
	if op == nil {
		t.Fatalf("operation %s %s not in spec", method, path)
	}
	if op.Servers == nil || len(*op.Servers) == 0 || (*op.Servers)[0].URL != "http://localhost:8082" {
		t.Fatalf("%s %s must declare per-op server http://localhost:8082 (design §6)", method, path)
	}
	resp := op.Responses.Status(status)
	if resp == nil || resp.Value == nil {
		t.Fatalf("no %d response defined for %s %s", status, method, path)
	}
	mt := resp.Value.Content.Get("application/json")
	if mt == nil || mt.Schema == nil || mt.Schema.Value == nil {
		t.Fatalf("no application/json schema for %s %s %d", method, path, status)
	}
	var v any
	if err := json.Unmarshal(body, &v); err != nil {
		t.Fatalf("body not JSON: %v", err)
	}
	if err := mt.Schema.Value.VisitJSON(v); err != nil {
		t.Fatalf("response does not conform (%s %s %d): %v", method, path, status, err)
	}
}

func TestAdminResponsesMatchOpenAPI(t *testing.T) {
	doc, err := openapi3.NewLoader().LoadFromFile("../../api/openapi.yaml")
	if err != nil {
		t.Fatalf("load spec: %v", err)
	}
	if err := doc.Validate(context.Background()); err != nil {
		t.Fatalf("spec invalid: %v", err)
	}
	srv, _ := newServer(t)

	// create-endpoint 201 conforms AND carries the per-op :8082 server
	ew := do(t, srv, "POST", "/v1/endpoints", map[string]string{"url": "https://example.com/h"})
	if ew.Code != 201 {
		t.Fatalf("create = %d body=%s", ew.Code, ew.Body.String())
	}
	validateOpResponse(t, doc, "POST", "/v1/endpoints", 201, ew.Body.Bytes())

	// list 200 conforms (the {items, next_cursor} envelope)
	lw := do(t, srv, "GET", "/v1/endpoints", nil)
	if lw.Code != 200 {
		t.Fatalf("list = %d", lw.Code)
	}
	validateOpResponse(t, doc, "GET", "/v1/endpoints", 200, lw.Body.Bytes())

	// NEGATIVE CONTROL: a body missing the required `secret`/`url` must FAIL the
	// create-201 schema — proving the schema is not vacuously permissive.
	pi := doc.Paths.Value("/v1/endpoints")
	schema := pi.GetOperation("POST").Responses.Status(201).Value.Content.Get("application/json").Schema.Value
	if err := schema.VisitJSON(map[string]any{"id": "x"}); err == nil {
		t.Fatal("create-201 schema accepted a body missing required url/secret — schema is vacuous")
	}
}
```
**Pin the dependency — do NOT use `@latest`** (every other dep in `go.mod` is pinned). Run `go get github.com/getkin/kin-openapi@v0.131.0` (or the current release), then `go mod tidy`, and commit the `go.mod`/`go.sum` deltas in this task. Verify `Paths.Value`, `PathItem.GetOperation`, `Responses.Status`, and `Schema.VisitJSON` exist at the chosen version (stable since v0.124); if a symbol differs, adapt to that version's equivalent rather than downgrading the check.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -tags integration ./internal/admin/ -run TestAdminResponsesMatchOpenAPI -v`
Expected: FAIL — admin paths/servers/scheme not yet in the spec.

- [ ] **Step 3: Extend the OpenAPI contract**

In `api/openapi.yaml`:
1. Add `'cancelled'` to the existing event-status enum on line ~66:
   ```yaml
   state: { type: string, enum: [pending, in_flight, retry_scheduled, succeeded, dead_lettered, cancelled] }
   ```
2. Add the admin security scheme under `components.securitySchemes`:
   ```yaml
   adminToken: { type: http, scheme: bearer, description: "HOOKRAIL_ADMIN_TOKEN — internal admin surface only" }
   ```
3. Add the component schemas (required fields make `VisitJSON` non-vacuous):
   ```yaml
   # under components.schemas
   Endpoint:
     type: object
     required: [id, url]
     properties:
       id: { type: string }
       url: { type: string }
       description: { type: string }
       created_at: { type: string, format: date-time }
       deleted_at: { type: string, format: date-time, nullable: true }
   EndpointCreated:
     type: object
     required: [id, url, secret]
     properties:
       id: { type: string }
       url: { type: string }
       secret: { type: string }
   SecretRotated:
     type: object
     required: [secret]
     properties: { secret: { type: string } }
   Subscription:
     type: object
     required: [id, topic_pattern, endpoint_id, max_attempts, active]
     properties:
       id: { type: string }
       topic_pattern: { type: string }
       endpoint_id: { type: string }
       max_attempts: { type: integer }
       rate_limit_rps: { type: number, nullable: true }
       backoff_policy: { type: object, nullable: true }
       active: { type: boolean }
       deleted_at: { type: string, format: date-time, nullable: true }
   IDCreated:
     type: object
     required: [id]
     properties: { id: { type: string } }
   DeadLetter:
     type: object
     required: [delivery_id, final_error, dead_at]
     properties:
       delivery_id: { type: string }
       endpoint_id: { type: string, nullable: true }
       final_error: { type: string }
       dead_at: { type: string, format: date-time }
       replayed_at: { type: string, format: date-time, nullable: true }
   Replayed:
     type: object
     required: [delivery_id, state]
     properties: { delivery_id: { type: string }, state: { type: string } }
   DeliveryListItem:
     type: object
     required: [id, event_id, endpoint_id, state]
     properties:
       id: { type: string }
       event_id: { type: string }
       endpoint_id: { type: string }
       state: { type: string, enum: [pending, in_flight, retry_scheduled, succeeded, dead_lettered, cancelled] }
   DeliveryTimeline:
     type: object
     required: [delivery_id, state, attempts_truncated, attempts]
     properties:
       delivery_id: { type: string }
       state: { type: string, enum: [pending, in_flight, retry_scheduled, succeeded, dead_lettered, cancelled] }
       attempts_truncated: { type: boolean }
       attempts: { type: array, items: { type: object } }
   ```
   The list envelope is expressed inline per list op (it references the item schema):
   ```yaml
   # 200 response body for every list op
   type: object
   required: [items, next_cursor]
   properties:
     items: { type: array, items: { $ref: "#/components/schemas/Endpoint" } }   # swap $ref per op: Subscription / DeadLetter / DeliveryListItem
     next_cursor: { type: string }
   ```
4. Add the admin paths — each operation carries the per-op server + security, and the response schemas above. Full mapping (status → schema; all error statuses `$ref: "#/components/responses/Problem"`):
   - `POST /v1/endpoints`: 201→`EndpointCreated`, 400, 422
   - `GET /v1/endpoints`: 200→list-of-`Endpoint`
   - `GET /v1/endpoints/{id}`: 200→`Endpoint`, 404
   - `PATCH /v1/endpoints/{id}`: 204 (no body), 404, 422
   - `DELETE /v1/endpoints/{id}`: 204, 404
   - `POST /v1/endpoints/{id}/rotate-secret`: 200→`SecretRotated`, 404
   - `POST /v1/subscriptions`: 201→`IDCreated`, 400, 409, 422
   - `GET /v1/subscriptions`: 200→list-of-`Subscription`
   - `GET /v1/subscriptions/{id}`: 200→`Subscription`, 404
   - `PATCH /v1/subscriptions/{id}`: 204, 404, 409, 422
   - `DELETE /v1/subscriptions/{id}`: 204, 404
   - `GET /v1/dlq`: 200→list-of-`DeadLetter`
   - `POST /v1/dlq/{delivery_id}/replay`: 200→`Replayed`, 404, 409, 410
   - `GET /v1/deliveries`: 200→list-of-`DeliveryListItem`
   - `GET /v1/deliveries/{id}`: 200→`DeliveryTimeline`, 404

   Every admin operation carries:
   ```yaml
   servers: [ { url: http://localhost:8082 } ]   # INTERNAL admin port (design §6)
   security: [ { adminToken: [] } ]
   ```
   Keep the existing `producerKey` global `security` for the ingress paths (`/v1/events*`).

> **Conformance scope:** the test (Step 1) validates the create-endpoint 201 AND the list 200 against these schemas, AND a negative control (a body missing required fields must fail). Because the schemas declare `required`, the negative control will fail validation, proving they are not vacuous. The implementer must write valid YAML for all 14 path-operations above — `doc.Validate(ctx)` in the test rejects a malformed/incomplete spec.

- [ ] **Step 4: Run test to verify pass**

Run: `go test -tags integration ./internal/admin/ -run TestAdminResponsesMatchOpenAPI -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add api/openapi.yaml internal/admin/openapi_conformance_test.go go.mod go.sum
git commit -m "feat(openapi): admin bearer scheme + per-op :8082 servers + cancelled enum; schema conformance test"
```

### Task 23: Compose — admin service + scheduler scrape

**Files:**
- Modify: `deploy/compose/docker-compose.yml`
- Modify: `deploy/compose/prometheus.yml`

**Interfaces:**
- Produces: a `hookrail-admin` service on `:8082` (internal; with `HOOKRAIL_ADMIN_TOKEN` in its env) and a `hookrail-scheduler` scrape target on `:8083` in Prometheus.

- [ ] **Step 1: Add the admin service**

In `deploy/compose/docker-compose.yml`, add after the `scheduler` service (mirroring its `depends_on`):
```yaml
  admin:
    build: ../..
    command: ["hookrail-admin"]
    environment:
      <<: *hookrail-env
      HOOKRAIL_ADMIN_TOKEN: "dev-admin-token"   # dev only; real deploys inject a secret (Slice E)
    ports: ["8082:8082"]
    depends_on:
      postgres: { condition: service_healthy }
      redis: { condition: service_healthy }
      migrate: { condition: service_completed_successfully }
```
> Confirm the `*hookrail-env` anchor merge syntax matches the file's existing style; if the file uses `environment: *hookrail-env` without a merge, instead define the env inline by copying the anchored vars plus `HOOKRAIL_ADMIN_TOKEN`.

- [ ] **Step 2: Add the scheduler scrape target**

In `deploy/compose/prometheus.yml`, append:
```yaml
  - job_name: hookrail-scheduler
    static_configs:
      - targets: ["scheduler:8083"]
```

- [ ] **Step 3: Verify compose config is valid**

Run: `docker compose -f deploy/compose/docker-compose.yml config >/dev/null && echo OK`
Expected: `OK` (compose parses; no service runs needed here).

- [ ] **Step 4: Commit**

```bash
git add deploy/compose/docker-compose.yml deploy/compose/prometheus.yml
git commit -m "feat(compose): add hookrail-admin service (:8082) and scheduler :8083 scrape target"
```

### Task 24: Admin happy-path e2e

**Files:**
- Create: `test/e2e/admin_e2e_test.go`

**Interfaces:**
- Produces: an e2e test (build tag `e2e`) that drives the running compose stack through the admin API: create endpoint → create subscription → ingest an event (public API) → poll `GET /v1/deliveries` (admin) until the delivery reaches `succeeded`. Uses `E2E_ADMIN_URL` (default `http://localhost:8082`) and `E2E_ADMIN_TOKEN` (default `dev-admin-token`).

- [ ] **Step 1: Write the e2e test**

```go
//go:build e2e

package e2e

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"testing"
	"time"
)

var (
	adminURL   = env("E2E_ADMIN_URL", "http://localhost:8082")
	adminToken = env("E2E_ADMIN_TOKEN", "dev-admin-token")
)

func adminReq(t *testing.T, method, path string, body any) (*http.Response, []byte) {
	t.Helper()
	var rdr *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	} else {
		rdr = bytes.NewReader(nil)
	}
	req, _ := http.NewRequest(method, adminURL+path, rdr)
	req.Header.Set("Authorization", "Bearer "+adminToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("admin %s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	buf := new(bytes.Buffer)
	_, _ = buf.ReadFrom(resp.Body)
	return resp, buf.Bytes()
}

func TestAdminHappyPath(t *testing.T) {
	if os.Getenv("E2E_ADMIN_TOKEN") == "" && adminToken == "" {
		t.Skip("admin token unset")
	}
	// create endpoint pointing at the in-stack receiver
	resp, b := adminReq(t, "POST", "/v1/endpoints", map[string]string{"url": "http://test-receiver:9090/succeed"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create endpoint = %d: %s", resp.StatusCode, b)
	}
	var ep struct{ ID string }
	_ = json.Unmarshal(b, &ep)

	// create subscription
	resp, b = adminReq(t, "POST", "/v1/subscriptions", map[string]any{"topic_pattern": "e2eadmin.*", "endpoint_id": ep.ID, "max_attempts": 5})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create sub = %d: %s", resp.StatusCode, b)
	}

	// ingest via the PUBLIC api (postEvent uses E2E_PRODUCER_KEY from make seed)
	got := postEvent(t, "e2eadmin.created", `{"hello":"admin"}`)
	if len(got.DeliveryIDs) == 0 {
		t.Fatal("no deliveries created — is the subscription active and the seeded key valid?")
	}

	// poll admin deliveries until succeeded
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		resp, b = adminReq(t, "GET", "/v1/deliveries/"+got.DeliveryIDs[0], nil)
		var tl struct{ State string `json:"state"` }
		_ = json.Unmarshal(b, &tl)
		if tl.State == "succeeded" {
			return
		}
		time.Sleep(time.Second)
	}
	t.Fatal("delivery did not reach succeeded within 30s")
}
```
> Note: the seeded producer key from `make seed` subscribes to `demo.*` by default. For this e2e the subscription created above (`e2eadmin.*`) must match the ingested topic; the seeded producer key authorizes ingestion regardless of topic. Confirm the e2e harness in CI runs `make seed` and exports `E2E_PRODUCER_KEY` (existing P0 flow) and add `E2E_ADMIN_TOKEN=dev-admin-token` to the e2e job env.

- [ ] **Step 2: Run e2e against a live stack**

Run: `make up && make seed` (export the printed `producer_key` as `E2E_PRODUCER_KEY`), then `make e2e`.
Expected: `TestAdminHappyPath` PASS along with the existing P0 e2e tests.

- [ ] **Step 3: Commit**

```bash
git add test/e2e/admin_e2e_test.go
git commit -m "test(e2e): admin happy path — create endpoint+sub via admin, ingest, poll to succeeded"
```

### Task 25: README admin + retention documentation

**Files:**
- Modify: `README.md`

**Interfaces:**
- Produces: a documented admin/retention section that states the honest limits verbatim (design §9 residual risks): `rate_limit_rps` is per-worker best-effort (NOT a global cap; P2); rotation/URL cutover is eventual (bounded by the worker HTTP attempt timeout); pagination is best-effort (ULID not strictly monotonic intra-ms); ingest↔delete reconciliation may allow more than one late attempt within an interval.

- [ ] **Step 1: Add the admin/retention section**

Append to `README.md` an "Admin & retention (P1 Slice A)" section covering: the internal `hookrail-admin` binary (`:8082`, bearer `HOOKRAIL_ADMIN_TOKEN`, refuses boot without it, NetworkPolicy-internal in k3s); the CRUD/query/DLQ-replay endpoints; the retention janitor (env knobs `RETENTION_*` with defaults) and `hookrail-ctl retention --once`; and a **"Limits & honesty"** subsection stating each residual risk above in plain language. Do not claim a global rate cap or strict pagination.

- [ ] **Step 2: Verify build + full suite**

Run: `make build && make lint && make test && make itest`
Expected: all green (README change is docs-only; confirm nothing else regressed).

- [ ] **Step 3: Commit**

```bash
git add README.md
git commit -m "docs(readme): document admin surface + retention with honest residual-risk limits"
```

> **M-A5 gate (Opus):** full VERIFY **plus `make e2e`** against the real compose stack (now 11 services incl. `admin`). Confirm the OpenAPI conformance test validates real responses (not just path registration), the admin per-op `servers:` override targets `:8082`, `cancelled` is in the enum, Prometheus scrapes `scheduler:8083`, and the README states the limits honestly (no global-cap / strict-pagination over-promise). This is the **P1 Slice A exit**.

---

## Self-review (author checklist, run against the design)

**Spec coverage (design §-by-§):**
- §1 topology / admin binary / `:8083` listener → Tasks 3–5, 23. `internal/httpx` extraction → Task 1.
- §1.1 admin auth (digest constant-time, ops-exempt) → Tasks 3, 4, 11.
- §2 admin surface (all routes) → Tasks 8–14; status conventions (404/409/410/422) → Tasks 9, 13.
- §2.1 immutable-id keyset, clamp, opaque cursor, ULID caveat → Task 1 (helpers) + Tasks 8, 9, 12, 14 (use); ULID intra-ms best-effort documented in README (Task 25) and design §9.
- §3 retention (3 jobs + cancel-orphaned, advisory xact lock, batch+budget, config wiring) → Tasks 2 (config), 19, 20, 21; tombstone safety → Task 19 test.
- §4.1 atomic replay → Task 13. §4.2 cancelled + reconciliation → Tasks 15, 19/20. §4.3 backoff + rate_limit + rotation cutover → Tasks 10, 16, 17.
- §5 migration 0002 + endpoint_id sequencing → Tasks 6, 7 (SET NOT NULL appended in 7), 18 (dead_letters.endpoint_id).
- §6 OpenAPI extend + per-op servers + cancelled enum + conformance → Task 22.
- §7 testing strategy → unit + integration tests in every task; e2e → Task 24.
- §8 milestone grouping + all-Opus + Codex pre-gate on M-A4a/b → bridge map + per-milestone gate notes.
- §9 residual risks → documented in README (Task 25) + the relevant task notes.

**Placeholder scan:** no "TBD"/"add error handling"/"similar to Task N", and no undefined-symbol scaffolding lines — every test code block compiles as written (the earlier `mkEndpoint`/`deadLetterOne` intent-marker lines have been deleted).

**Type consistency:** `admin.New` signature changes once (Task 4, 6 args) then gains `replayAge` (Task 13, 7 args) — every call site (binary + test harness + `server_test.go`) updated in the same task. `api.New` gains `idemTTL` (Task 2) with both call sites updated. `writeJSON`/`writePage`/`stub` are defined once in Task 4. `ClaimedDelivery.BackoffPolicy`, `DeliveryView.AttemptsTruncated`, `SubscriptionRow.BackoffPolicy` (`json.RawMessage`), `store.ErrNotFound`/`ErrConflict`, `ReplayOutcome`, `DLQFilter`/`DeliveryFilter`, `cancelOrphanedTx` are defined before use. `CancelOrphanedLocked` (Task 19) is what the janitor calls (Task 20); standalone `CancelOrphaned` (Task 15) wraps `cancelOrphanedTx` in its own tx.

**Codex round-1 REWORK folded (14 findings):** all-compile fixes (imports + every changed-signature call site enumerated); NOT NULL moved to migration `0003` (golang-migrate never re-runs an applied version); all four retention passes bounded + advisory-locked; backoff overflow closed with `MaxPolicyMS` + validate-before-multiply + `DisallowUnknownFields`; OpenAPI conformance done via `Schema.VisitJSON` + explicit per-op `Servers` assertion (kin-openapi pinned, not `@latest`); `q.MaxLen` on the API producer; `EndpointLimits` reverts removed overrides; replay tests cover vs-claim / step-2 rollback / unchanged `claim_version` / all-losers-409; janitor aggregates errors so the CLI exits nonzero; `SubscriptionRow.BackoffPolicy` is `json.RawMessage`; `IsTerminalForWorker` includes `cancelled`; collation-safe `($1 = '' OR id < $1)` keysets; ctl help is side-effect-free with a subprocess test.

**Known follow-through risks to watch at gate-time (raise, don't silently fix):**
- The `ALTER TYPE ... ADD VALUE 'cancelled'` transaction behavior on the PG-16 test container (Task 6 note). If golang-migrate's tx wrapper rejects it, split the enum add into its own migration.
- The `ListDLQ`/`ListDeliveries` dynamic-`$N` SQL builders keep `$2` as the limit while appending filters from `$3` — verify the generated SQL in the tests; inline the state-cast branch if the `add` helper composition is fragile.
- kin-openapi API surface (`Paths.Value`, `GetOperation`, `Responses.Status`, `Schema.VisitJSON`) at the pinned version — adapt if a symbol differs there.

---

## Execution Handoff

**Plan complete and saved to `docs/superpowers/plans/2026-06-18-hookrail-p1-sliceA.md`.**

This plan is executed through the **Reasonix bridge**, not the generic subagent/inline runners: DeepSeek implements each milestone in the bounded Ralph loop, Claude is the only pusher and the milestone gate (all Opus; Codex gpt-5.5-xhigh pre-gate on M-A4a/M-A4b). Per-milestone, set `.agent/MILESTONE` to the hyphenated `TASKS: N-M` range from the bridge map above, run `./.agent/ralph.sh`, then Prompt C (V4 Pro pre-review) → Claude gate. The next step before any implementation is the **Codex adversarial review of this plan**, then fold its findings.




