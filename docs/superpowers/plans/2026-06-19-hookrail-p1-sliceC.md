# Hookrail P1 Slice C — Admin Dashboard (React/TS SPA + Go BFF) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship a browser dashboard (React/TS SPA) for full read + control over the Slice A admin API, served by a new Go BFF that holds the admin token server-side so it never reaches the browser.

**Architecture:** A Go BFF (`cmd/hookrail-dashboard` + `internal/dashboard`) serves the built SPA and is the only origin the browser talks to (same-origin → no CORS). It authenticates humans with a shared password → HMAC-signed cookie, then proxies an **allowlist** of admin routes to `:8082` (injecting `HOOKRAIL_ADMIN_TOKEN`) and one native `test-event` route to ingress `:8080` (injecting a provisioned producer key). The SPA is Vite + React + TS (strict) + TanStack Query + Tailwind, with zod schemas validated against the corrected `api/openapi.yaml`.

**Tech Stack:** Go per `go.mod` (`go 1.26.4`; the repo's CI `setup-go` pins `1.24` — leave it, it's pre-existing repo state, NOT a Slice C change); BFF joins the existing module `github.com/mit112/hookrail`. Vite + React 18 + TypeScript (strict) + TanStack Query + React Router + Tailwind; zod; Vitest + React Testing Library + MSW; Playwright (gate-only e2e).

## Global Constraints

- Module path is `github.com/mit112/hookrail`; the BFF is new code in the SAME Go module (no new go.mod). Toolchain is `go 1.26.4` per `go.mod:3`; do NOT touch `.github/workflows/ci.yml`'s existing `go-version: "1.24"` setup — that is the repo's standing config.
- Mirror existing patterns: RFC 7807 errors via `internal/httpx.Problem(w, status, title, detail)`; constant-time auth like `internal/admin/auth.go` (`sha256` digest + `crypto/subtle.ConstantTimeCompare`).
- The injected admin token / producer key MUST NEVER appear in any HTTP response, error body, or log line.
- The BFF builds a **fresh** `http.Request` for every upstream call — never clone-and-mutate the inbound request. Copy no inbound headers except an explicit allowlist (`Content-Type`, plus `Idempotency-Key` for test-event).
- All gates Opus. **Mandatory Codex gpt-5.5 read-only pre-gate on M-C1, M-C4, M-C5** (run `gtimeout <secs> codex exec` — it can hang on network reads; `gpt-5.5-xhigh` is unavailable on this account → use `-c model_reasoning_effort=high`).
- SPA dist name + import path: the web app lives in `clients/web/` (parallel to `clients/python/`).
- Never weaken/skip tests. Frequent commits (one logical change per commit, imperative mood). Do not push (Claude gate pushes).
- `<GH_LOGIN>` is `mit112` — never write the literal placeholder anywhere.
- Node: a single LTS (Node 20) for `web` CI + local. Package manager: `npm` (lockfile `package-lock.json`).

---

## Milestone M-C1 — BFF security core (Tasks 1–9) · VERIFY: `make build && make lint && make test && make itest` · **Codex pre-gate: YES**

> The credential boundary lands first and whole. The Codex pre-gate reviews cookie forgery, token leak, open-proxy, and request smuggling.

### Task 1: Correct `api/openapi.yaml` drift + extend the admin conformance test

**Files:**
- Modify: `api/openapi.yaml` (deliveries `topic_pattern`→`topic`; add `include_deleted` to endpoints GET list + endpoints GET `{id}` + subscriptions GET `{id}` — **NOT** subscriptions list, which the server does not support, `internal/store/admin.go:155` hardcodes `deleted_at IS NULL`; add DLQ `replayed`+`until`; add `Cache-Control: no-store` response header to create-endpoint `201` + rotate-secret `200`)
- Create: `internal/admin/openapi_params_test.go` (**no build tag** — the existing `openapi_conformance_test.go` has `//go:build integration` at line 1, so a test added there is skipped by plain `go test`/`make test`; the param test must run unconditionally)

**Interfaces:**
- Produces: a contract that matches the real admin server, so the SPA's zod conformance test (Task 11) is meaningful.

> **Why a new file:** `internal/admin/openapi_conformance_test.go:1` is `//go:build integration`; `make test` runs without that tag, so an assertion placed there would silently not run. This param test lives in an untagged file so `make test` exercises it.

- [ ] **Step 1: Write the failing test** in the new untagged file.

```go
//go:build !ignore
package admin

import (
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

func loadSpec(t *testing.T) *openapi3.T {
	t.Helper()
	doc, err := openapi3.NewLoader().LoadFromFile("../../api/openapi.yaml")
	if err != nil { t.Fatal(err) }
	return doc
}

func TestOpenAPIParamsMatchServer(t *testing.T) {
	doc := loadSpec(t)
	q := func(path, method, name string) bool {
		op := doc.Paths.Find(path).GetOperation(method)
		for _, p := range op.Parameters {
			if p.Value != nil && p.Value.Name == name && p.Value.In == "query" { return true }
		}
		return false
	}
	if !q("/v1/deliveries", "GET", "topic") { t.Error("deliveries GET missing query param topic") }
	if q("/v1/deliveries", "GET", "topic_pattern") { t.Error("deliveries GET should NOT document topic_pattern") }
	if !q("/v1/endpoints", "GET", "include_deleted") { t.Error("endpoints list GET missing include_deleted") }
	if !q("/v1/endpoints/{id}", "GET", "include_deleted") { t.Error("endpoint GET missing include_deleted") }
	if !q("/v1/subscriptions/{id}", "GET", "include_deleted") { t.Error("subscription GET missing include_deleted") }
	// subscriptions LIST does NOT support include_deleted (store hardcodes deleted_at IS NULL)
	if q("/v1/subscriptions", "GET", "include_deleted") { t.Error("subscriptions list must NOT document include_deleted") }
	if !q("/v1/dlq", "GET", "replayed") { t.Error("dlq GET missing replayed") }
	if !q("/v1/dlq", "GET", "until") { t.Error("dlq GET missing until") }
}

func TestOpenAPIDocumentsNoStoreHeader(t *testing.T) {
	doc := loadSpec(t)
	has := func(path, method, code string) bool {
		op := doc.Paths.Find(path).GetOperation(method)
		resp := op.Responses.Status(mustAtoi(code))
		return resp != nil && resp.Value != nil && resp.Value.Headers["Cache-Control"] != nil
	}
	if !has("/v1/endpoints", "POST", "201") { t.Error("create-endpoint 201 missing Cache-Control header (secret in body)") }
	if !has("/v1/endpoints/{id}/rotate-secret", "POST", "200") { t.Error("rotate-secret 200 missing Cache-Control header") }
}

func mustAtoi(s string) int { // tiny helper; 201/200 only
	n := 0
	for _, r := range s { n = n*10 + int(r-'0') }
	return n
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `cd hookrail && go test ./internal/admin/ -run 'TestOpenAPI' -v`
Expected: FAIL (topic missing, include_deleted missing, no-store header missing)

- [ ] **Step 3: Edit `api/openapi.yaml`** —
  - under `/v1/deliveries` GET parameters, rename `topic_pattern` → `topic`;
  - add `- { in: query, name: include_deleted, schema: { type: boolean } }` to `/v1/endpoints` GET (list), `/v1/endpoints/{id}` GET, and `/v1/subscriptions/{id}` GET **only** (NOT the subscriptions list op);
  - under `/v1/dlq` GET parameters add `- { in: query, name: replayed, schema: { type: boolean } }` and `- { in: query, name: until, schema: { type: string, format: date-time } }`;
  - on `/v1/endpoints` POST `201` and `/v1/endpoints/{id}/rotate-secret` POST `200`, add a `headers:` block: `Cache-Control: { schema: { type: string }, description: "no-store (secret in body)" }`.

- [ ] **Step 4: Run test to verify it passes**

Run: `cd hookrail && go test ./internal/admin/ -run 'TestOpenAPI' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add api/openapi.yaml internal/admin/openapi_params_test.go
git commit -m "fix(openapi): align admin contract with real server (topic, scoped include_deleted, dlq replayed/until, no-store headers)"
```

### Task 2: `hookrail-ctl create-producer-key` subcommand

**Files:**
- Modify: `cmd/hookrail-ctl/main.go` (add the `create-producer-key` subcommand)
- Test: `cmd/hookrail-ctl/createkey_test.go`

**Interfaces:**
- Consumes: `store.CreateProducerKey(ctx, name) (id, plaintext string, err error)` (`internal/store/seed.go:14`); `config.Load()`; `store.Open(ctx, cfg.DatabaseURL)`.
- Produces: a CLI that prints `producer_key=hk_…\nkey_id=…` to stdout (used by the compose provisioner + web-e2e).

> **Match the real ctl conventions** (`cmd/hookrail-ctl/main.go`): dispatch is `if os.Args[1] == "…"` blocks (not a `switch`/`case`); config via `config.Load()` → `cfg.DatabaseURL`; store via `store.Open(context.Background(), cfg.DatabaseURL)`; errors via `slog.Error(...)` + `os.Exit(1)`; `usage()` is a function printing the subcommand list; `wantsHelp` already handles `-h`/`help` at command position.

- [ ] **Step 1: Write the failing tests** — (a) help mentions the subcommand; (b) an integration-tagged path test that actually creates a key against the test Postgres.

```go
// cmd/hookrail-ctl/createkey_test.go
package main

import (
	"os/exec"
	"strings"
	"testing"
)

func TestUsageMentionsCreateProducerKey(t *testing.T) {
	out, _ := exec.Command("go", "run", ".", "-h").CombinedOutput()
	if !strings.Contains(string(out), "create-producer-key") {
		t.Fatalf("usage did not mention create-producer-key: %s", out)
	}
}
```

```go
//go:build integration
// cmd/hookrail-ctl/createkey_integration_test.go
package main

import (
	"os/exec"
	"strings"
	"testing"
)

// Uses the same Postgres the store itests use (HOOKRAIL_DATABASE_URL set by the
// itest harness / compose). Mirror how seed/migrate integration paths are exercised.
func TestCreateProducerKeyEmitsKey(t *testing.T) {
	if testing.Short() { t.Skip("integration") }
	out, err := exec.Command("go", "run", ".", "create-producer-key", "-name", "test").CombinedOutput()
	if err != nil { t.Fatalf("run failed: %v\n%s", err, out) }
	if !strings.Contains(string(out), "producer_key=hk_") {
		t.Fatalf("expected producer_key=hk_… in output, got: %s", out)
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `cd hookrail && go test ./cmd/hookrail-ctl/ -run TestUsage -v`
Expected: FAIL (usage does not yet mention the subcommand)

- [ ] **Step 3: Implement** — add a `create-producer-key` block to the dispatch (after the `migrate`/`retention` blocks, same style) and extend `usage()`:

```go
// usage(): update the line to list the new subcommand
fmt.Fprintln(os.Stderr, "usage: hookrail-ctl <seed|migrate|retention|create-producer-key> [flags]")

// in main(), an if-block alongside the others:
if os.Args[1] == "create-producer-key" {
	fs := flag.NewFlagSet("create-producer-key", flag.ExitOnError)
	name := fs.String("name", "dashboard", "human label for the key")
	_ = fs.Parse(os.Args[2:])
	cfg, err := config.Load()
	if err != nil { slog.Error("config", "err", err); os.Exit(1) }
	s, err := store.Open(context.Background(), cfg.DatabaseURL)
	if err != nil { slog.Error("store", "err", err); os.Exit(1) }
	defer s.Close()
	id, plaintext, err := s.CreateProducerKey(context.Background(), *name)
	if err != nil { slog.Error("create-producer-key", "err", err); os.Exit(1) }
	fmt.Printf("producer_key=%s\nkey_id=%s\n", plaintext, id)
	return
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd hookrail && go test ./cmd/hookrail-ctl/ -run TestUsage -v` (unit) and `go test -tags integration ./cmd/hookrail-ctl/ -run TestCreateProducerKeyEmitsKey -v` (with PG available)
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add cmd/hookrail-ctl/main.go cmd/hookrail-ctl/createkey_test.go cmd/hookrail-ctl/createkey_integration_test.go
git commit -m "feat(ctl): add create-producer-key subcommand for dashboard provisioning"
```

### Task 3: BFF config loader (fail-closed)

**Files:**
- Create: `internal/dashboard/config.go`
- Test: `internal/dashboard/config_test.go`

**Interfaces:**
- Produces: `type Config struct{...}` and `func LoadConfig() (Config, error)` reading the §2.1 env vars; returns an error naming the first missing/invalid var.

- [ ] **Step 1: Write the failing test**

```go
// internal/dashboard/config_test.go
package dashboard

import (
	"os"
	"path/filepath"
	"testing"
)

func setMinEnv(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	keyFile := filepath.Join(dir, "pk")
	if err := os.WriteFile(keyFile, []byte("hk_deadbeef"), 0o600); err != nil { t.Fatal(err) }
	t.Setenv("HOOKRAIL_DASHBOARD_PASSWORD", "s3cret-long-enough")
	t.Setenv("HOOKRAIL_DASHBOARD_SESSION_KEY", "0123456789abcdef0123456789abcdef")
	t.Setenv("HOOKRAIL_ADMIN_TOKEN", "admintok")
	t.Setenv("HOOKRAIL_PRODUCER_KEY_FILE", keyFile)
	t.Setenv("HOOKRAIL_ADMIN_URL", "http://admin:8082")
	t.Setenv("HOOKRAIL_INGRESS_URL", "http://api:8080")
	return keyFile
}

func TestLoadConfigOK(t *testing.T) {
	setMinEnv(t)
	cfg, err := LoadConfig()
	if err != nil { t.Fatalf("unexpected error: %v", err) }
	if cfg.ProducerKey != "hk_deadbeef" { t.Errorf("producer key not read from file: %q", cfg.ProducerKey) }
	if cfg.Addr != ":8085" { t.Errorf("default addr wrong: %q", cfg.Addr) }
}

func TestLoadConfigMissingSessionKeyTooShort(t *testing.T) {
	setMinEnv(t)
	t.Setenv("HOOKRAIL_DASHBOARD_SESSION_KEY", "tooshort")
	if _, err := LoadConfig(); err == nil { t.Fatal("expected error for short session key") }
}

func TestLoadConfigMissingPassword(t *testing.T) {
	setMinEnv(t)
	os.Unsetenv("HOOKRAIL_DASHBOARD_PASSWORD")
	if _, err := LoadConfig(); err == nil { t.Fatal("expected error for missing password") }
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `cd hookrail && go test ./internal/dashboard/ -run TestLoadConfig -v`
Expected: FAIL (package/func not defined)

- [ ] **Step 3: Implement `internal/dashboard/config.go`**

```go
package dashboard

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"
)

type Config struct {
	Password     string
	SessionKey   []byte
	SessionPrev  []byte // optional; nil when unset
	AdminToken   string
	ProducerKey  string
	AdminURL     string
	IngressURL   string
	Addr         string
	SessionTTL   time.Duration
	InsecureCookie bool
}

func LoadConfig() (Config, error) {
	var c Config
	req := func(k string) (string, error) {
		v := os.Getenv(k)
		if strings.TrimSpace(v) == "" {
			return "", fmt.Errorf("required env %s is unset", k)
		}
		return v, nil
	}
	var err error
	if c.Password, err = req("HOOKRAIL_DASHBOARD_PASSWORD"); err != nil { return c, err }
	sk := os.Getenv("HOOKRAIL_DASHBOARD_SESSION_KEY")
	if len(sk) < 32 { return c, errors.New("HOOKRAIL_DASHBOARD_SESSION_KEY must be >= 32 bytes") }
	c.SessionKey = []byte(sk)
	if p := os.Getenv("HOOKRAIL_DASHBOARD_SESSION_KEY_PREVIOUS"); p != "" {
		if len(p) < 32 { return c, errors.New("HOOKRAIL_DASHBOARD_SESSION_KEY_PREVIOUS must be >= 32 bytes") }
		c.SessionPrev = []byte(p)
	}
	if c.AdminToken, err = req("HOOKRAIL_ADMIN_TOKEN"); err != nil { return c, err }
	keyFile, err := req("HOOKRAIL_PRODUCER_KEY_FILE")
	if err != nil { return c, err }
	b, err := os.ReadFile(keyFile)
	if err != nil { return c, fmt.Errorf("reading HOOKRAIL_PRODUCER_KEY_FILE: %w", err) }
	c.ProducerKey = strings.TrimSpace(string(b))
	if !strings.HasPrefix(c.ProducerKey, "hk_") { return c, errors.New("producer key file must contain an hk_ key") }
	if c.AdminURL, err = req("HOOKRAIL_ADMIN_URL"); err != nil { return c, err }
	if c.IngressURL, err = req("HOOKRAIL_INGRESS_URL"); err != nil { return c, err }
	for _, u := range []string{c.AdminURL, c.IngressURL} {
		pu, perr := url.Parse(u)
		if perr != nil || (pu.Scheme != "http" && pu.Scheme != "https") {
			return c, fmt.Errorf("invalid upstream URL %q", u)
		}
	}
	c.Addr = os.Getenv("HOOKRAIL_DASHBOARD_ADDR")
	if c.Addr == "" { c.Addr = ":8085" }
	c.SessionTTL = 12 * time.Hour
	if v := os.Getenv("HOOKRAIL_DASHBOARD_SESSION_TTL"); v != "" {
		if d, derr := time.ParseDuration(v); derr == nil { c.SessionTTL = d } else { return c, fmt.Errorf("bad HOOKRAIL_DASHBOARD_SESSION_TTL: %w", derr) }
	}
	c.InsecureCookie = os.Getenv("HOOKRAIL_DASHBOARD_INSECURE_COOKIE") == "true"
	return c, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd hookrail && go test ./internal/dashboard/ -run TestLoadConfig -v`
Expected: PASS (3 tests)

- [ ] **Step 5: Commit**

```bash
git add internal/dashboard/config.go internal/dashboard/config_test.go
git commit -m "feat(dashboard): fail-closed BFF config loader"
```

### Task 4: Session cookie — HMAC sign/verify with kid + exp

**Files:**
- Create: `internal/dashboard/session.go`
- Test: `internal/dashboard/session_test.go`

**Interfaces:**
- Consumes: `Config.SessionKey`, `Config.SessionPrev`, `Config.SessionTTL`.
- Produces: `type Sessions struct{...}`; `NewSessions(cfg Config) *Sessions`; `(*Sessions) Issue(now time.Time) string` (cookie value); `(*Sessions) Valid(value string, now time.Time) bool`.

- [ ] **Step 1: Write the failing test**

```go
// internal/dashboard/session_test.go
package dashboard

import (
	"strings"
	"testing"
	"time"
)

func testSessions(prev []byte) *Sessions {
	return NewSessions(Config{
		SessionKey:  []byte("0123456789abcdef0123456789abcdef"),
		SessionPrev: prev,
		SessionTTL:  time.Hour,
	})
}

func TestSessionRoundTrip(t *testing.T) {
	s := testSessions(nil)
	now := time.Unix(1_700_000_000, 0)
	v := s.Issue(now)
	if !s.Valid(v, now) { t.Fatal("freshly issued cookie should be valid") }
}

func TestSessionExpired(t *testing.T) {
	s := testSessions(nil)
	now := time.Unix(1_700_000_000, 0)
	v := s.Issue(now)
	if s.Valid(v, now.Add(2*time.Hour)) { t.Fatal("expired cookie must be rejected") }
}

func TestSessionTampered(t *testing.T) {
	s := testSessions(nil)
	now := time.Unix(1_700_000_000, 0)
	v := s.Issue(now)
	parts := strings.SplitN(v, ".", 2)
	if s.Valid(parts[0]+".AAAA", now) { t.Fatal("tampered tag must be rejected") }
	if s.Valid("eyJhIjoxfQ."+parts[1], now) { t.Fatal("tampered payload must be rejected") }
}

func TestSessionPreviousKeyVerifies(t *testing.T) {
	old := testSessions(nil) // signs with primary "0123...="
	now := time.Unix(1_700_000_000, 0)
	v := old.Issue(now)
	// New deploy: yesterday's primary is now PREVIOUS; new primary is different.
	rotated := NewSessions(Config{
		SessionKey:  []byte("ffffffffffffffffffffffffffffffff"),
		SessionPrev: []byte("0123456789abcdef0123456789abcdef"),
		SessionTTL:  time.Hour,
	})
	if !rotated.Valid(v, now) { t.Fatal("cookie signed by previous key must still verify") }
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `cd hookrail && go test ./internal/dashboard/ -run TestSession -v`
Expected: FAIL (Sessions undefined)

- [ ] **Step 3: Implement `internal/dashboard/session.go`**

```go
package dashboard

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"
)

type Sessions struct {
	keys [][]byte // index = kid; keys[0]=primary, keys[1]=previous (optional)
	ttl  time.Duration
}

type sessionPayload struct {
	V   int   `json:"v"`
	Kid int   `json:"kid"`
	Exp int64 `json:"exp"`
	Iat int64 `json:"iat"`
}

func NewSessions(cfg Config) *Sessions {
	keys := [][]byte{cfg.SessionKey}
	if cfg.SessionPrev != nil {
		keys = append(keys, cfg.SessionPrev)
	}
	return &Sessions{keys: keys, ttl: cfg.SessionTTL}
}

func (s *Sessions) sign(payload []byte, kid int) string {
	mac := hmac.New(sha256.New, s.keys[kid])
	mac.Write(payload)
	tag := mac.Sum(nil)
	return base64.RawURLEncoding.EncodeToString(payload) + "." +
		base64.RawURLEncoding.EncodeToString(tag)
}

// Issue always signs with the primary key (kid 0).
func (s *Sessions) Issue(now time.Time) string {
	p := sessionPayload{V: 1, Kid: 0, Iat: now.Unix(), Exp: now.Add(s.ttl).Unix()}
	b, _ := json.Marshal(p)
	return s.sign(b, 0)
}

func (s *Sessions) Valid(value string, now time.Time) bool {
	i := strings.LastIndexByte(value, '.')
	if i < 0 { return false }
	payloadB64, tagB64 := value[:i], value[i+1:]
	payload, err := base64.RawURLEncoding.DecodeString(payloadB64)
	if err != nil { return false }
	gotTag, err := base64.RawURLEncoding.DecodeString(tagB64)
	if err != nil { return false }
	var p sessionPayload
	if json.Unmarshal(payload, &p) != nil { return false }
	if p.Kid < 0 || p.Kid >= len(s.keys) { return false }
	mac := hmac.New(sha256.New, s.keys[p.Kid])
	mac.Write(payload)
	if subtle.ConstantTimeCompare(gotTag, mac.Sum(nil)) != 1 { return false }
	return now.Unix() < p.Exp
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd hookrail && go test ./internal/dashboard/ -run TestSession -v`
Expected: PASS (4 tests)

- [ ] **Step 5: Commit**

```bash
git add internal/dashboard/session.go internal/dashboard/session_test.go
git commit -m "feat(dashboard): HMAC session cookie with kid-based key rotation"
```

### Task 5: Login + logout + session endpoints + brute-force throttle

**Files:**
- Create: `internal/dashboard/auth.go`
- Test: `internal/dashboard/auth_test.go`

**Interfaces:**
- Consumes: `Config`, `*Sessions`, `internal/httpx.Problem`.
- Produces: handlers `(*Server) handleLogin/handleLogout/handleSession`; `(*Server) cookieName() = "hk_dash"`; a `throttle` allowing N attempts/window/IP.

- [ ] **Step 1: Write the failing test**

```go
// internal/dashboard/auth_test.go
package dashboard

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func testServer(t *testing.T) *Server {
	t.Helper()
	keyFile := setMinEnv(t)
	_ = keyFile
	cfg, err := LoadConfig()
	if err != nil { t.Fatal(err) }
	return NewServer(cfg)
}

func TestLoginWrongPassword(t *testing.T) {
	srv := testServer(t)
	r := httptest.NewRequest("POST", "/api/login", strings.NewReader(`{"password":"nope"}`))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized { t.Fatalf("want 401, got %d", w.Code) }
	if len(w.Result().Cookies()) != 0 { t.Fatal("no cookie on failed login") }
}

func TestLoginRightPasswordSetsCookie(t *testing.T) {
	srv := testServer(t)
	r := httptest.NewRequest("POST", "/api/login", strings.NewReader(`{"password":"s3cret-long-enough"}`))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusOK { t.Fatalf("want 200, got %d", w.Code) }
	var found *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == "hk_dash" { found = c }
	}
	if found == nil { t.Fatal("expected hk_dash cookie") }
	if !found.HttpOnly || found.SameSite != http.SameSiteStrictMode { t.Error("cookie must be HttpOnly+SameSite=Strict") }
}

func TestThrottleLocksOut(t *testing.T) {
	srv := testServer(t)
	for i := 0; i < 12; i++ {
		r := httptest.NewRequest("POST", "/api/login", strings.NewReader(`{"password":"nope"}`))
		r.Header.Set("Content-Type", "application/json")
		r.RemoteAddr = "10.0.0.9:1234"
		w := httptest.NewRecorder()
		srv.Handler().ServeHTTP(w, r)
		if i >= 10 && w.Code != http.StatusTooManyRequests {
			t.Fatalf("attempt %d: want 429, got %d", i, w.Code)
		}
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `cd hookrail && go test ./internal/dashboard/ -run 'TestLogin|TestThrottle' -v`
Expected: FAIL (NewServer/Handler undefined)

- [ ] **Step 3: Implement `internal/dashboard/auth.go`** (NewServer/Handler skeleton lands here; the proxy + statics are added in later tasks and registered in Task 9's `Handler()` — for now `Handler()` wires only the auth routes).

```go
package dashboard

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/mit112/hookrail/internal/httpx"
)

type Server struct {
	cfg      Config
	sessions *Sessions
	pwDigest [32]byte
	thr      *throttle
	now      func() time.Time
}

func NewServer(cfg Config) *Server {
	return &Server{
		cfg:      cfg,
		sessions: NewSessions(cfg),
		pwDigest: sha256.Sum256([]byte(cfg.Password)),
		thr:      newThrottle(10, time.Minute),
		now:      time.Now,
	}
}

func (s *Server) cookieName() string { return "hk_dash" }

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil { return r.RemoteAddr }
	return host
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Content-Type") != "application/json" {
		httpx.Problem(w, http.StatusUnsupportedMediaType, "bad content-type", "application/json required")
		return
	}
	if !s.thr.allow(clientIP(r), s.now()) {
		httpx.Problem(w, http.StatusTooManyRequests, "too many attempts", "slow down")
		return
	}
	var body struct{ Password string `json:"password"` }
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpx.Problem(w, http.StatusBadRequest, "invalid body", `expected {"password": string}`)
		return
	}
	got := sha256.Sum256([]byte(body.Password))
	if subtle.ConstantTimeCompare(got[:], s.pwDigest[:]) != 1 {
		httpx.Problem(w, http.StatusUnauthorized, "invalid credentials", "wrong password")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: s.cookieName(), Value: s.sessions.Issue(s.now()), Path: "/",
		HttpOnly: true, Secure: !s.cfg.InsecureCookie, SameSite: http.SameSiteStrictMode,
		MaxAge: int(s.cfg.SessionTTL.Seconds()),
	})
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]bool{"authenticated": true})
}

func (s *Server) handleLogout(w http.ResponseWriter, _ *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: s.cookieName(), Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: !s.cfg.InsecureCookie, SameSite: http.SameSiteStrictMode})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleSession(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]bool{"authenticated": true})
}

// throttle: fixed-window per key.
type throttle struct {
	mu     sync.Mutex
	limit  int
	window time.Duration
	hits   map[string]struct {
		count int
		start time.Time
	}
}

func newThrottle(limit int, window time.Duration) *throttle {
	return &throttle{limit: limit, window: window, hits: map[string]struct {
		count int
		start time.Time
	}{}}
}

func (t *throttle) allow(key string, now time.Time) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	e := t.hits[key]
	if now.Sub(e.start) > t.window {
		e.count, e.start = 0, now
	}
	e.count++
	t.hits[key] = e
	return e.count <= t.limit
}
```

(In Task 9 `Handler()` registers `POST /api/login`, `POST /api/logout`, `GET /api/session`. For this task, a temporary `Handler()` may register only those three so the test compiles; Task 9 replaces it with the full mux.)

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd hookrail && go test ./internal/dashboard/ -run 'TestLogin|TestThrottle' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/dashboard/auth.go internal/dashboard/auth_test.go
git commit -m "feat(dashboard): password login + logout + session + brute-force throttle"
```

### Task 6: Session + CSRF middleware

**Files:**
- Create: `internal/dashboard/middleware.go`
- Test: `internal/dashboard/middleware_test.go`

**Interfaces:**
- Consumes: `*Sessions`, cookie name.
- Produces: `(*Server) requireSession(next http.HandlerFunc) http.HandlerFunc` — 401 without a valid cookie; on state-changing methods (POST/PATCH/DELETE) also require `Content-Type: application/json` and a same-origin (or absent) `Origin`.

- [ ] **Step 1: Write the failing test**

```go
// internal/dashboard/middleware_test.go
package dashboard

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func authedCookie(t *testing.T, s *Server) *http.Cookie {
	return &http.Cookie{Name: s.cookieName(), Value: s.sessions.Issue(s.now())}
}

func TestRequireSessionNoCookie(t *testing.T) {
	srv := testServer(t)
	h := srv.requireSession(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })
	w := httptest.NewRecorder()
	h(w, httptest.NewRequest("GET", "/v1/endpoints", nil))
	if w.Code != http.StatusUnauthorized { t.Fatalf("want 401, got %d", w.Code) }
}

func TestRequireSessionGoodCookie(t *testing.T) {
	srv := testServer(t)
	h := srv.requireSession(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })
	r := httptest.NewRequest("GET", "/v1/endpoints", nil)
	r.AddCookie(authedCookie(t, srv))
	w := httptest.NewRecorder()
	h(w, r)
	if w.Code != 200 { t.Fatalf("want 200, got %d", w.Code) }
}

func TestRequireSessionRejectsFormPost(t *testing.T) {
	srv := testServer(t)
	h := srv.requireSession(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })
	r := httptest.NewRequest("POST", "/v1/endpoints", nil)
	r.AddCookie(authedCookie(t, srv))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h(w, r)
	if w.Code != http.StatusUnsupportedMediaType { t.Fatalf("want 415, got %d", w.Code) }
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `cd hookrail && go test ./internal/dashboard/ -run TestRequireSession -v`
Expected: FAIL (requireSession undefined)

- [ ] **Step 3: Implement `internal/dashboard/middleware.go`**

```go
package dashboard

import (
	"net/http"

	"github.com/mit112/hookrail/internal/httpx"
)

func (s *Server) requireSession(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(s.cookieName())
		if err != nil || !s.sessions.Valid(c.Value, s.now()) {
			httpx.Problem(w, http.StatusUnauthorized, "not authenticated", "log in first")
			return
		}
		switch r.Method {
		case http.MethodPost, http.MethodPatch, http.MethodDelete:
			if r.Header.Get("Content-Type") != "application/json" {
				httpx.Problem(w, http.StatusUnsupportedMediaType, "bad content-type", "application/json required")
				return
			}
			if o := r.Header.Get("Origin"); o != "" && o != originOf(r) {
				httpx.Problem(w, http.StatusForbidden, "cross-origin", "origin not allowed")
				return
			}
		}
		next(w, r)
	}
}

func originOf(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil { scheme = "https" }
	return scheme + "://" + r.Host
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd hookrail && go test ./internal/dashboard/ -run TestRequireSession -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/dashboard/middleware.go internal/dashboard/middleware_test.go
git commit -m "feat(dashboard): session + CSRF (content-type + origin) middleware"
```

### Task 7: Allowlist proxy — fresh request, header hygiene, token injection, redirect block

**Files:**
- Create: `internal/dashboard/proxy.go`
- Test: `internal/dashboard/proxy_test.go`

**Interfaces:**
- Consumes: `Config.AdminURL`, `Config.AdminToken`.
- Produces: `(*Server) adminRoutes() []route` (the 15-entry allowlist); `(*Server) proxyAdmin(w, r)` building a fresh upstream request.

- [ ] **Step 1: Write the failing test** (a fake admin upstream asserts the injected token + that smuggled creds were stripped).

```go
// internal/dashboard/proxy_test.go
package dashboard

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestProxyInjectsTokenStripsSmuggled(t *testing.T) {
	var sawAuth, sawCookie, sawProxyAuth string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAuth = r.Header.Get("Authorization")
		sawCookie = r.Header.Get("Cookie")
		sawProxyAuth = r.Header.Get("Proxy-Authorization")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_, _ = io.WriteString(w, `{"items":[],"next_cursor":""}`)
	}))
	defer up.Close()

	srv := testServer(t)
	srv.cfg.AdminURL = up.URL
	r := httptest.NewRequest("GET", "/v1/endpoints?limit=5", nil)
	r.AddCookie(authedCookie(t, srv))
	r.Header.Set("Authorization", "Bearer smuggled")
	r.Header.Set("Proxy-Authorization", "Bearer smuggled2")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)

	if w.Code != 200 { t.Fatalf("want 200, got %d", w.Code) }
	if sawAuth != "Bearer admintok" { t.Errorf("upstream Authorization = %q, want injected admin token", sawAuth) }
	if sawCookie != "" { t.Errorf("cookie leaked upstream: %q", sawCookie) }
	if sawProxyAuth != "" { t.Errorf("proxy-authorization leaked upstream: %q", sawProxyAuth) }
}

func TestProxyUnknownPath404(t *testing.T) {
	srv := testServer(t)
	r := httptest.NewRequest("GET", "/v1/secret-backdoor", nil)
	r.AddCookie(authedCookie(t, srv))
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusNotFound { t.Fatalf("unknown path must be 404, got %d", w.Code) }
}

func TestProxyDoesNotFollowRedirect(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://evil.example/", http.StatusFound)
	}))
	defer up.Close()
	srv := testServer(t)
	srv.cfg.AdminURL = up.URL
	r := httptest.NewRequest("GET", "/v1/endpoints", nil)
	r.AddCookie(authedCookie(t, srv))
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusFound { t.Fatalf("redirect must pass through (302), got %d", w.Code) }
}

func TestProxyRejectsOversizeBody(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body); w.WriteHeader(200)
	}))
	defer up.Close()
	srv := testServer(t)
	srv.cfg.AdminURL = up.URL
	big := strings.Repeat("a", (256<<10)+1)
	r := httptest.NewRequest("POST", "/v1/endpoints", strings.NewReader(big))
	r.Header.Set("Content-Type", "application/json")
	r.AddCookie(authedCookie(t, srv))
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusRequestEntityTooLarge { t.Fatalf("want 413, got %d", w.Code) }
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `cd hookrail && go test ./internal/dashboard/ -run TestProxy -v`
Expected: FAIL (proxy/route undefined)

- [ ] **Step 3: Implement `internal/dashboard/proxy.go`**

```go
package dashboard

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/mit112/hookrail/internal/httpx"
)

const maxBody = 256 << 10 // 256 KB, matches ingest payload max

var safeReqHeaders = map[string]bool{"Content-Type": true}
var safeRespHeaders = map[string]bool{"Content-Type": true, "Cache-Control": true}

// proxyClient never follows redirects.
var proxyClient = &http.Client{
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	Timeout:       0, // per-request context timeout instead
}

// proxyAdmin builds a FRESH outbound request to the admin upstream, injecting the admin token.
func (s *Server) proxyAdmin(w http.ResponseWriter, r *http.Request) {
	// Reject absolute-form / suspicious request targets — only origin-form paths proxy.
	if r.URL.IsAbs() || r.URL.Host != "" || !strings.HasPrefix(r.URL.Path, "/v1/") {
		httpx.Problem(w, http.StatusBadRequest, "bad request target", "only origin-form /v1 paths are proxied")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	url := s.cfg.AdminURL + r.URL.Path
	if r.URL.RawQuery != "" { url += "?" + r.URL.RawQuery }
	// MaxBytesReader caps the request body; reading past the cap yields *http.MaxBytesError → 413.
	body := http.MaxBytesReader(w, r.Body, maxBody)
	out, err := http.NewRequestWithContext(ctx, r.Method, url, body)
	if err != nil {
		httpx.Problem(w, http.StatusBadGateway, "proxy error", "could not build upstream request")
		return
	}
	for k, v := range r.Header {
		if safeReqHeaders[http.CanonicalHeaderKey(k)] {
			out.Header[http.CanonicalHeaderKey(k)] = v
		}
	}
	out.Header.Set("Authorization", "Bearer "+s.cfg.AdminToken)
	resp, err := proxyClient.Do(out)
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			httpx.Problem(w, http.StatusRequestEntityTooLarge, "body too large", "request body exceeds 256KB")
			return
		}
		httpx.Problem(w, http.StatusBadGateway, "upstream unreachable", "admin API did not respond")
		return
	}
	defer resp.Body.Close()
	for k, v := range resp.Header {
		if safeRespHeaders[http.CanonicalHeaderKey(k)] {
			w.Header()[http.CanonicalHeaderKey(k)] = v
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, io.LimitReader(resp.Body, maxBody))
}

type route struct {
	method string
	path   string // ServeMux pattern, e.g. "/v1/endpoints/{id}"
}

func (s *Server) adminRoutes() []route {
	return []route{
		{"POST", "/v1/endpoints"}, {"GET", "/v1/endpoints"},
		{"GET", "/v1/endpoints/{id}"}, {"PATCH", "/v1/endpoints/{id}"}, {"DELETE", "/v1/endpoints/{id}"},
		{"POST", "/v1/endpoints/{id}/rotate-secret"},
		{"POST", "/v1/subscriptions"}, {"GET", "/v1/subscriptions"},
		{"GET", "/v1/subscriptions/{id}"}, {"PATCH", "/v1/subscriptions/{id}"}, {"DELETE", "/v1/subscriptions/{id}"},
		{"GET", "/v1/dlq"}, {"POST", "/v1/dlq/{delivery_id}/replay"},
		{"GET", "/v1/deliveries"}, {"GET", "/v1/deliveries/{id}"},
	}
}
```

(Registering these on the mux happens in Task 9. **Note:** the `GET /` static handler is a catch-all, so an unmatched `/v1/secret-backdoor` would otherwise hit `serveStatic` — Task 9's `serveStatic` therefore explicitly 404s any `/v1/` or `/api/` prefix, which is what makes `TestProxyUnknownPath404` pass. The allowlisted patterns are forwarded; nothing else reaches an upstream.)

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd hookrail && go test ./internal/dashboard/ -run TestProxy -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/dashboard/proxy.go internal/dashboard/proxy_test.go
git commit -m "feat(dashboard): allowlist admin proxy with fresh-request header hygiene"
```

### Task 8: Test-event handler (native, producer key, fresh idempotency key)

**Files:**
- Create: `internal/dashboard/testevent.go`
- Test: `internal/dashboard/testevent_test.go`

**Interfaces:**
- Consumes: `Config.IngressURL`, `Config.ProducerKey`.
- Produces: `(*Server) handleTestEvent(w, r)` validating `{topic, payload}` and POSTing to ingress `/v1/events`.

- [ ] **Step 1: Write the failing test**

```go
// internal/dashboard/testevent_test.go
package dashboard

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTestEventForwardsToIngress(t *testing.T) {
	var sawAuth, sawIdem, sawPath string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAuth, sawIdem, sawPath = r.Header.Get("Authorization"), r.Header.Get("Idempotency-Key"), r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(202)
		_, _ = io.WriteString(w, `{"event_id":"ev_1","delivery_ids":[]}`)
	}))
	defer up.Close()
	srv := testServer(t)
	srv.cfg.IngressURL = up.URL
	r := httptest.NewRequest("POST", "/api/test-event", strings.NewReader(`{"topic":"demo.x","payload":{"a":1}}`))
	r.Header.Set("Content-Type", "application/json")
	r.AddCookie(authedCookie(t, srv))
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)
	if w.Code != 202 { t.Fatalf("want 202, got %d (%s)", w.Code, w.Body) }
	if sawPath != "/v1/events" { t.Errorf("upstream path = %q", sawPath) }
	if sawAuth != "Bearer hk_deadbeef" { t.Errorf("producer key not injected: %q", sawAuth) }
	if sawIdem == "" { t.Error("expected a generated Idempotency-Key") }
}

func TestTestEventRejectsBadBody(t *testing.T) {
	srv := testServer(t)
	r := httptest.NewRequest("POST", "/api/test-event", strings.NewReader(`{"topic":"","payload":1}`))
	r.Header.Set("Content-Type", "application/json")
	r.AddCookie(authedCookie(t, srv))
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusBadRequest { t.Fatalf("want 400, got %d", w.Code) }
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `cd hookrail && go test ./internal/dashboard/ -run TestTestEvent -v`
Expected: FAIL (handleTestEvent undefined)

- [ ] **Step 3: Implement `internal/dashboard/testevent.go`**

```go
package dashboard

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/mit112/hookrail/internal/httpx"
)

func (s *Server) handleTestEvent(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Topic   string          `json:"topic"`
		Payload json.RawMessage `json:"payload"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, maxBody)).Decode(&body); err != nil || body.Topic == "" || !isJSONObject(body.Payload) {
		httpx.Problem(w, http.StatusBadRequest, "invalid body", `expected {"topic": string, "payload": object}`)
		return
	}
	raw, _ := json.Marshal(map[string]any{"topic": body.Topic, "payload": body.Payload})
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	out, _ := http.NewRequestWithContext(ctx, "POST", s.cfg.IngressURL+"/v1/events", bytes.NewReader(raw))
	out.Header.Set("Content-Type", "application/json")
	out.Header.Set("Authorization", "Bearer "+s.cfg.ProducerKey)
	out.Header.Set("Idempotency-Key", newIdempotencyKey())
	resp, err := proxyClient.Do(out)
	if err != nil {
		httpx.Problem(w, http.StatusBadGateway, "upstream unreachable", "ingress did not respond")
		return
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "" { w.Header().Set("Content-Type", ct) }
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, io.LimitReader(resp.Body, maxBody))
}

func isJSONObject(b json.RawMessage) bool {
	var m map[string]any
	return len(b) > 0 && json.Unmarshal(b, &m) == nil
}

func newIdempotencyKey() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return "dash_" + hex.EncodeToString(b)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd hookrail && go test ./internal/dashboard/ -run TestTestEvent -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/dashboard/testevent.go internal/dashboard/testevent_test.go
git commit -m "feat(dashboard): native test-event handler with provisioned producer key"
```

### Task 9: Mux wiring + static SPA serving + ops routes + the `hookrail-dashboard` binary

**Files:**
- Create: `internal/dashboard/static.go`, `internal/dashboard/server_handler.go`, `cmd/hookrail-dashboard/main.go`
- Test: `internal/dashboard/server_handler_test.go`

**Interfaces:**
- Produces: the final `(*Server) Handler() http.Handler` registering auth, the allowlist proxy, test-event, static serving, and ops; `cmd/hookrail-dashboard` boots it (fail-closed via `LoadConfig`).

- [ ] **Step 1: Write the failing test** (statics + healthz + a SPA fallback to index.html for unknown non-API GETs).

```go
// internal/dashboard/server_handler_test.go
package dashboard

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHealthzOpen(t *testing.T) {
	srv := testServer(t)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/healthz", nil))
	if w.Code != 200 { t.Fatalf("healthz want 200, got %d", w.Code) }
}

func TestSPAFallbackServesIndex(t *testing.T) {
	srv := testServer(t)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/endpoints", nil)) // a client-side route
	if w.Code != 200 { t.Fatalf("SPA fallback want 200, got %d", w.Code) }
}

func TestV1RequiresAuth(t *testing.T) {
	srv := testServer(t)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/v1/endpoints", nil)) // no cookie
	if w.Code != http.StatusUnauthorized { t.Fatalf("want 401, got %d", w.Code) }
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `cd hookrail && go test ./internal/dashboard/ -run 'TestHealthz|TestSPAFallback|TestV1Requires' -v`
Expected: FAIL

- [ ] **Step 3: Implement.** `static.go` embeds the built SPA via `//go:embed dist` with a fallback to `index.html` for non-asset GETs (in tests the embed dir may be a stub `dist/index.html`). `server_handler.go` defines the real `Handler()`:

```go
// internal/dashboard/server_handler.go
package dashboard

import "net/http"

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	// auth (login public; logout/session guarded)
	mux.HandleFunc("POST /api/login", s.handleLogin)
	mux.HandleFunc("POST /api/logout", s.requireSession(s.handleLogout))
	mux.HandleFunc("GET /api/session", s.requireSession(s.handleSession))
	mux.HandleFunc("POST /api/test-event", s.requireSession(s.handleTestEvent))
	// allowlist admin proxy
	for _, rt := range s.adminRoutes() {
		mux.HandleFunc(rt.method+" "+rt.path, s.requireSession(s.proxyAdmin))
	}
	// ops (open)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })
	mux.HandleFunc("GET /readyz", s.readyz)
	// static SPA + fallback (public shell)
	mux.HandleFunc("GET /", s.serveStatic)
	return mux
}
```

```go
// internal/dashboard/static.go
package dashboard

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed all:dist
var distFS embed.FS

func (s *Server) serveStatic(w http.ResponseWriter, r *http.Request) {
	// The `GET /` pattern is a catch-all; an unknown API path must 404, NOT fall back to the SPA shell
	// (otherwise unknown /v1/* and /api/* would 200 with index.html and the open-proxy 404 test fails).
	if strings.HasPrefix(r.URL.Path, "/v1/") || strings.HasPrefix(r.URL.Path, "/api/") {
		http.NotFound(w, r)
		return
	}
	sub, _ := fs.Sub(distFS, "dist")
	p := strings.TrimPrefix(r.URL.Path, "/")
	if p == "" { p = "index.html" }
	if _, err := fs.Stat(sub, p); err != nil {
		p = "index.html" // SPA client-side route fallback
	}
	http.ServeFileFS(w, r, sub, p)
}

func (s *Server) readyz(w http.ResponseWriter, r *http.Request) {
	// best-effort: report ready (admin reachability proven by the proxy on demand)
	w.WriteHeader(http.StatusOK)
}
```

```go
// cmd/hookrail-dashboard/main.go
package main

import (
	"log"
	"net/http"

	"github.com/mit112/hookrail/internal/dashboard"
)

func main() {
	cfg, err := dashboard.LoadConfig()
	if err != nil {
		log.Fatalf("dashboard config: %v", err) // fail-closed before binding
	}
	srv := dashboard.NewServer(cfg)
	log.Printf("hookrail-dashboard listening on %s", cfg.Addr)
	if err := http.ListenAndServe(cfg.Addr, srv.Handler()); err != nil {
		log.Fatal(err)
	}
}
```

Create a committed stub `internal/dashboard/dist/index.html` (`<!doctype html><title>hookrail</title>`) so the embed compiles before the SPA exists; M-C5 wires the real `vite build` output into `dist/`.

- [ ] **Step 4: Run tests + full build**

Run: `cd hookrail && go test ./internal/dashboard/ -v && go build ./cmd/hookrail-dashboard`
Expected: PASS + builds

- [ ] **Step 5: Commit**

```bash
git add internal/dashboard/static.go internal/dashboard/server_handler.go internal/dashboard/server_handler_test.go internal/dashboard/dist/index.html cmd/hookrail-dashboard/main.go
git commit -m "feat(dashboard): mux wiring, static SPA serving, and hookrail-dashboard binary"
```

**M-C1 VERIFY:** `make build && make lint && make test && make itest` — all green. **Codex pre-gate** reviews `internal/dashboard/*` + the proxy/cookie/test-event boundary before the Opus gate pushes.

---

## Milestone M-C2 — SPA scaffold + typed API layer + login (Tasks 10–13) · VERIFY: `make web-verify` · Codex pre-gate: no

> Everything the browser needs to authenticate and talk to the BFF, built to the corrected contract.

### Task 10: Scaffold `clients/web` + `make web-verify` + `web` CI job

**Files:**
- Create: `clients/web/package.json`, `clients/web/tsconfig.json`, `clients/web/vite.config.ts`, `clients/web/tailwind.config.js`, `clients/web/postcss.config.js`, `clients/web/.eslintrc.cjs`, `clients/web/index.html`, `clients/web/src/main.tsx`, `clients/web/src/App.tsx`, `clients/web/src/index.css`, `clients/web/vitest.setup.ts`, `clients/web/src/smoke.test.tsx`
- Modify: `Makefile` (add `web-verify`, `web-build`), `.github/workflows/ci.yml` (add `web` job)

**Interfaces:**
- Produces: a buildable Vite/React/TS app and a `make web-verify` target (`tsc --noEmit && eslint && vitest run && vite build`).

- [ ] **Step 1: Write the failing smoke test**

```tsx
// clients/web/src/smoke.test.tsx
import { render, screen } from "@testing-library/react";
import { describe, it, expect } from "vitest";
import { App } from "./App";

describe("App", () => {
  it("renders the shell", () => {
    render(<App />);
    expect(screen.getByText(/hookrail/i)).toBeInTheDocument();
  });
});
```

- [ ] **Step 2: Scaffold the project.** `package.json` deps: `react`, `react-dom`, `@tanstack/react-query`, `react-router-dom`, `zod`; dev: `vite`, `@vitejs/plugin-react`, `typescript`, `vitest`, `@testing-library/react`, `@testing-library/jest-dom`, `jsdom`, `msw`, `eslint` + `@typescript-eslint/*` + `eslint-plugin-react-hooks`, `tailwindcss`, `postcss`, `autoprefixer`, `js-yaml` (+ `@types/js-yaml`). Scripts: `"dev":"vite"`, `"build":"tsc -b && vite build"`, `"test":"vitest run"`, `"lint":"eslint . --max-warnings=0"`, `"typecheck":"tsc --noEmit"`. `tsconfig.json` uses `"strict": true`, `"noUncheckedIndexedAccess": true`. `vite.config.ts` sets `test.environment="jsdom"`, `test.setupFiles=["./vitest.setup.ts"]`, and a dev proxy (`server.proxy` for `/v1` and `/api` → `http://localhost:8085`). `vitest.setup.ts` imports `@testing-library/jest-dom`. `App.tsx` renders a minimal `<h1>Hookrail</h1>` shell (replaced in Task 13).

- [ ] **Step 3: Add Makefile targets** (append to `Makefile`):

```makefile
.PHONY: web-verify web-build
web-verify:
	cd clients/web && npm ci && npm run typecheck && npm run lint && npm run test && npm run build
web-build:
	cd clients/web && npm ci && npm run build
```

- [ ] **Step 4: Add the `web` CI job** to `.github/workflows/ci.yml` (sibling of `lint`/`test`, runs on push+PR):

```yaml
  web:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-node@v4
        with: { node-version: "20", cache: "npm", cache-dependency-path: clients/web/package-lock.json }
      - run: npm ci
        working-directory: clients/web
      - run: npm run typecheck && npm run lint && npm run test && npm run build
        working-directory: clients/web
```

- [ ] **Step 5: Run verify + commit**

Run: `cd clients/web && npm install && npm run typecheck && npm run test`
Expected: smoke test PASS

```bash
git add clients/web Makefile .github/workflows/ci.yml
git commit -m "feat(web): scaffold Vite/React/TS dashboard + web-verify + CI job"
```

### Task 11: zod schemas + conformance test vs `api/openapi.yaml`

**Files:**
- Create: `clients/web/src/api/schemas.ts`, `clients/web/src/api/conformance.test.ts`

**Interfaces:**
- Produces: zod schemas `EndpointRow`, `SubscriptionRow`, `DeliveryListRow`, `DeliveryTimeline`, `AttemptView`, `DLQRow`, `Problem`, `Page(item)`, and `DeliveryState` (enum) — used by the client (Task 12) and forms (M-C4).

- [ ] **Step 1: Write the failing conformance test** (loads the corrected openapi and asserts the state enum + key fields match).

```ts
// clients/web/src/api/conformance.test.ts
import { describe, it, expect } from "vitest";
import { readFileSync } from "node:fs";
import { load } from "js-yaml";
import { DeliveryState } from "./schemas";

const spec = load(readFileSync(new URL("../../../../api/openapi.yaml", import.meta.url), "utf8")) as any;

describe("openapi conformance", () => {
  it("delivery state enum matches the spec", () => {
    const specEnum: string[] =
      spec.components.schemas.DeliveryListItem.properties.state.enum;
    expect([...DeliveryState.options].sort()).toEqual([...specEnum].sort());
  });
  it("deliveries documents the topic filter (not topic_pattern)", () => {
    const params = spec.paths["/v1/deliveries"].get.parameters.map((p: any) => p.name);
    expect(params).toContain("topic");
    expect(params).not.toContain("topic_pattern");
  });
});
```

- [ ] **Step 2: Run it to verify it fails**

Run: `cd clients/web && npm run test -- conformance`
Expected: FAIL (schemas not defined)

- [ ] **Step 3: Implement `clients/web/src/api/schemas.ts`**

```ts
import { z } from "zod";

export const DeliveryState = z.enum([
  "pending", "in_flight", "retry_scheduled", "succeeded", "dead_lettered", "cancelled",
]);

export const EndpointRow = z.object({
  id: z.string(), url: z.string(), description: z.string(),
  created_at: z.string(), deleted_at: z.string().optional(),
});
export const SubscriptionRow = z.object({
  id: z.string(), topic_pattern: z.string(), endpoint_id: z.string(),
  max_attempts: z.number(), rate_limit_rps: z.number().optional(),
  backoff_policy: z.unknown().optional(), active: z.boolean(), deleted_at: z.string().optional(),
});
export const DeliveryListRow = z.object({
  id: z.string(), event_id: z.string(), endpoint_id: z.string(), state: DeliveryState,
});
export const AttemptView = z.object({
  attempt_no: z.number(), claim_version: z.number(), status: z.string(),
  http_status: z.number().optional(), error_class: z.string().optional(), latency_ms: z.number(),
});
export const DeliveryTimeline = z.object({
  delivery_id: z.string(), state: DeliveryState,
  attempts_truncated: z.boolean(), attempts: z.array(AttemptView),
});
export const DLQRow = z.object({
  delivery_id: z.string(), endpoint_id: z.string().optional(),
  final_error: z.string(), dead_at: z.string(), replayed_at: z.string().optional(),
});
export const Problem = z.object({
  type: z.string().optional(), title: z.string(), status: z.number(), detail: z.string().optional(),
});
export const Page = <T extends z.ZodTypeAny>(item: T) =>
  z.object({ items: z.array(item).nullable().transform((v) => v ?? []), next_cursor: z.string() });

export type TEndpointRow = z.infer<typeof EndpointRow>;
export type TSubscriptionRow = z.infer<typeof SubscriptionRow>;
export type TDeliveryListRow = z.infer<typeof DeliveryListRow>;
export type TDeliveryTimeline = z.infer<typeof DeliveryTimeline>;
export type TDLQRow = z.infer<typeof DLQRow>;
```

(Add a `DeliveryListItem` schema to `api/openapi.yaml` components if not present, matching `DeliveryListRow`, so the conformance test's `spec.components.schemas.DeliveryListItem` resolves — verify against the existing components block first; the state enum lives there per `api/openapi.yaml:426`.)

- [ ] **Step 4: Run test to verify it passes**

Run: `cd clients/web && npm run test -- conformance`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add clients/web/src/api/schemas.ts clients/web/src/api/conformance.test.ts api/openapi.yaml
git commit -m "feat(web): zod schemas + openapi conformance test"
```

### Task 12: Typed `request()` client + `ApiProblem`

**Files:**
- Create: `clients/web/src/api/client.ts`, `clients/web/src/api/client.test.ts`

**Interfaces:**
- Produces: `class ApiProblem extends Error { status; title; detail }`; `request<T>(method, path, {schema?, body?}): Promise<T>` — same-origin fetch, `credentials:"include"`, throws `ApiProblem` on non-2xx, validates 2xx via the schema.

- [ ] **Step 1: Write the failing test** (MSW mocks the BFF).

```ts
// clients/web/src/api/client.test.ts
import { describe, it, expect, beforeAll, afterAll, afterEach } from "vitest";
import { setupServer } from "msw/node";
import { http, HttpResponse } from "msw";
import { request, ApiProblem } from "./client";
import { EndpointRow } from "./schemas";

const server = setupServer();
beforeAll(() => server.listen());
afterEach(() => server.resetHandlers());
afterAll(() => server.close());

it("parses a 2xx body with the schema", async () => {
  server.use(http.get("/v1/endpoints/e1", () =>
    HttpResponse.json({ id: "e1", url: "https://x", description: "", created_at: "t" })));
  const e = await request("GET", "/v1/endpoints/e1", { schema: EndpointRow });
  expect(e.id).toBe("e1");
});

it("throws ApiProblem on non-2xx", async () => {
  server.use(http.get("/v1/endpoints/missing", () =>
    HttpResponse.json({ title: "not found", status: 404 }, { status: 404 })));
  await expect(request("GET", "/v1/endpoints/missing", { schema: EndpointRow }))
    .rejects.toBeInstanceOf(ApiProblem);
});

it("sends application/json on a no-body mutation (CSRF middleware needs it)", async () => {
  let ct: string | null = "";
  server.use(http.post("/api/logout", ({ request }) => {
    ct = request.headers.get("content-type");
    return new HttpResponse(null, { status: 204 });
  }));
  await request("POST", "/api/logout");
  expect(ct).toBe("application/json");
});
```

- [ ] **Step 2: Run it to verify it fails**

Run: `cd clients/web && npm run test -- client`
Expected: FAIL

- [ ] **Step 3: Implement `clients/web/src/api/client.ts`**

```ts
import type { z } from "zod";
import { Problem } from "./schemas";

export class ApiProblem extends Error {
  constructor(public status: number, public title: string, public detail?: string) {
    super(`${status} ${title}`);
  }
}

export async function request<T>(
  method: string,
  path: string,
  opts: { schema?: z.ZodType<T>; body?: unknown } = {},
): Promise<T> {
  // The BFF CSRF middleware requires Content-Type: application/json on EVERY mutation
  // (POST/PATCH/DELETE) — even no-body ones (logout, DLQ replay, rotate-secret) — else 415.
  const isMutation = method === "POST" || method === "PATCH" || method === "DELETE";
  const res = await fetch(path, {
    method,
    credentials: "include",
    headers: opts.body !== undefined || isMutation ? { "Content-Type": "application/json" } : {},
    body: opts.body !== undefined ? JSON.stringify(opts.body) : undefined,
  });
  if (!res.ok) {
    let p = { title: res.statusText, status: res.status, detail: undefined as string | undefined };
    try { p = Problem.parse(await res.json()); } catch { /* non-problem body */ }
    throw new ApiProblem(p.status, p.title, p.detail);
  }
  if (res.status === 204) return undefined as T;
  const json = await res.json();
  return opts.schema ? opts.schema.parse(json) : (json as T);
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd clients/web && npm run test -- client`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add clients/web/src/api/client.ts clients/web/src/api/client.test.ts
git commit -m "feat(web): typed request() client with ApiProblem error mapping"
```

### Task 13: TanStack Query provider + login UI + session gate

**Files:**
- Create: `clients/web/src/auth/SessionGate.tsx`, `clients/web/src/auth/Login.tsx`, `clients/web/src/auth/session.ts`, `clients/web/src/auth/Login.test.tsx`
- Modify: `clients/web/src/App.tsx`, `clients/web/src/main.tsx`

**Interfaces:**
- Consumes: `request()`.
- Produces: `useSession()` (queries `GET /api/session`), `login(password)`, `logout()`; `<SessionGate>` renders `<Login>` when unauthenticated else children.

- [ ] **Step 1: Write the failing test**

```tsx
// clients/web/src/auth/Login.test.tsx
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach } from "vitest";
import { Login } from "./Login";
import * as session from "./session";

describe("Login", () => {
  beforeEach(() => vi.restoreAllMocks());
  it("calls login then onSuccess", async () => {
    const spy = vi.spyOn(session, "login").mockResolvedValue();
    const onSuccess = vi.fn();
    render(<Login onSuccess={onSuccess} />);
    fireEvent.change(screen.getByLabelText(/password/i), { target: { value: "pw" } });
    fireEvent.click(screen.getByRole("button", { name: /sign in/i }));
    await waitFor(() => expect(spy).toHaveBeenCalledWith("pw"));
    await waitFor(() => expect(onSuccess).toHaveBeenCalled());
  });
});
```

- [ ] **Step 2: Run it to verify it fails**

Run: `cd clients/web && npm run test -- Login`
Expected: FAIL

- [ ] **Step 3: Implement** `session.ts` (`login`/`logout`/`fetchSession` via `request`), `Login.tsx` (controlled password form, shows `ApiProblem` detail on failure, calls `onSuccess`), `SessionGate.tsx` (uses TanStack Query `useQuery(["session"], fetchSession)`; loading → spinner; error/401 → `<Login onSuccess={()=>queryClient.invalidateQueries(["session"])}/>`; success → children). Wire `QueryClientProvider` + `BrowserRouter` in `main.tsx`; `App.tsx` wraps routes in `<SessionGate>`.

```ts
// clients/web/src/auth/session.ts
import { request } from "../api/client";
export async function fetchSession(): Promise<boolean> {
  await request("GET", "/api/session"); return true;
}
export async function login(password: string): Promise<void> {
  await request("POST", "/api/login", { body: { password } });
}
export async function logout(): Promise<void> {
  await request("POST", "/api/logout");
}
```

- [ ] **Step 4: Run tests + verify**

Run: `cd clients/web && npm run typecheck && npm run test`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add clients/web/src/auth clients/web/src/App.tsx clients/web/src/main.tsx
git commit -m "feat(web): login UI + session gate over TanStack Query"
```

**M-C2 VERIFY:** `make web-verify` green (tsc + eslint + vitest + build).

---

## Milestone M-C3 — Read views (Tasks 14–17) · VERIFY: `make web-verify` · Codex pre-gate: no

> Each task adds one resource's query hook + list/detail view + a component test. All four follow the same shape: a `useXxx` hook calling `request("GET", path, {schema: Page(Row)})`, a table component, and a "load more" using `next_cursor`.

### Task 14: Endpoints list + detail

**Files:** Create `clients/web/src/query/endpoints.ts`, `clients/web/src/routes/Endpoints.tsx`, `clients/web/src/routes/EndpointDetail.tsx`, `clients/web/src/routes/Endpoints.test.tsx`. Modify the router in `App.tsx`.

**Interfaces:** Produces `useEndpoints(cursor?)`, `useEndpoint(id)` returning typed rows.

- [ ] **Step 1: Failing test** — render `<Endpoints>` with MSW returning two endpoints; assert both URLs appear and pagination calls the next cursor.

```tsx
// clients/web/src/routes/Endpoints.test.tsx (sketch — full code in implementation)
// MSW: GET /v1/endpoints → {items:[{id:"e1",url:"https://a",...},{id:"e2",url:"https://b",...}], next_cursor:""}
// render <Endpoints/> inside a QueryClientProvider + MemoryRouter; expect "https://a" and "https://b".
```

- [ ] **Step 2: Run → FAIL.** Run: `cd clients/web && npm run test -- Endpoints`
- [ ] **Step 3: Implement** the hook (`request("GET", \`/v1/endpoints?\${cursor?\`cursor=\${cursor}\`:""}\`, {schema: Page(EndpointRow)})`), a `<Table>` of id/url/description/created_at with a link to `/endpoints/:id`, a "Load more" button shown when `next_cursor !== ""`, and `<EndpointDetail>` using `useEndpoint`. Register routes `/endpoints` and `/endpoints/:id`.
- [ ] **Step 4: Run → PASS.** Run: `cd clients/web && npm run typecheck && npm run test -- Endpoints`
- [ ] **Step 5: Commit** — `git commit -m "feat(web): endpoints list + detail views"`

### Task 15: Subscriptions list + detail

**Files:** `clients/web/src/query/subscriptions.ts`, `routes/Subscriptions.tsx`, `routes/SubscriptionDetail.tsx`, `routes/Subscriptions.test.tsx`.

- [ ] **Step 1: Failing test** — MSW returns two subscriptions (with `topic_pattern`, `endpoint_id`, `active`); assert both render and the `?endpoint_id=` filter is sent when set.
- [ ] **Step 2: Run → FAIL.**
- [ ] **Step 3: Implement** `useSubscriptions(endpointId?, cursor?)` + `useSubscription(id)` (`schema: Page(SubscriptionRow)` / `SubscriptionRow`); table shows topic_pattern/endpoint_id/active/max_attempts; detail shows backoff_policy + rate_limit_rps. Routes `/subscriptions`, `/subscriptions/:id`.
- [ ] **Step 4: Run → PASS.**
- [ ] **Step 5: Commit** — `git commit -m "feat(web): subscriptions list + detail views"`

### Task 16: Deliveries list + timeline

**Files:** `clients/web/src/query/deliveries.ts`, `routes/Deliveries.tsx`, `routes/DeliveryTimeline.tsx`, `routes/Deliveries.test.tsx`.

- [ ] **Step 1: Failing test** — MSW: list returns rows with `state`; detail returns a `DeliveryTimeline` with two `attempts`. Assert the state filter (`?state=`) is sent and the timeline renders both attempts with `attempt_no`/`status`/`http_status`.
- [ ] **Step 2: Run → FAIL.**
- [ ] **Step 3: Implement** `useDeliveries(filters,cursor?)` (filters: state/endpoint_id/topic/event_id) + `useDeliveryTimeline(id)` (`schema: DeliveryTimeline`). List table: id/event_id/endpoint_id/state with filter inputs; timeline view shows the attempts table (attempt_no, claim_version, status, http_status, error_class, latency_ms) and an `attempts_truncated` badge. Routes `/deliveries`, `/deliveries/:id`.
- [ ] **Step 4: Run → PASS.**
- [ ] **Step 5: Commit** — `git commit -m "feat(web): deliveries list + attempt timeline"`

### Task 17: DLQ list

**Files:** `clients/web/src/query/dlq.ts`, `routes/DLQ.tsx`, `routes/DLQ.test.tsx`.

- [ ] **Step 1: Failing test** — MSW returns two `DLQRow`s; assert `final_error`/`dead_at` render and the `?replayed=false` + `?endpoint_id=` filters are sent.
- [ ] **Step 2: Run → FAIL.**
- [ ] **Step 3: Implement** `useDLQ(filters,cursor?)` (filters: endpoint_id/replayed/since/until, `schema: Page(DLQRow)`); table: delivery_id/endpoint_id/final_error/dead_at/replayed_at; replay button is added in M-C4. Route `/dlq`.
- [ ] **Step 4: Run → PASS.**
- [ ] **Step 5: Commit** — `git commit -m "feat(web): dead-letter queue list view"`

**M-C3 VERIFY:** `make web-verify` green.

---

## Milestone M-C4 — Control actions (Tasks 18–22) · VERIFY: `make web-verify` · **Codex pre-gate: YES**

> Mutations + the secret reveal. The Codex pre-gate verifies the secret is never persisted/logged, replay UX maps statuses correctly, and mutations are CSRF-shaped (JSON, retry disabled).

### Task 18: Endpoint create / edit / delete forms

**Files:** Create `clients/web/src/query/endpointMutations.ts`, `routes/EndpointForm.tsx`, `routes/EndpointForm.test.tsx`. Modify `routes/Endpoints.tsx` (add "New endpoint" + per-row delete) and `routes/EndpointDetail.tsx` (add "Edit").

**Interfaces:** Produces `useCreateEndpoint()`, `useUpdateEndpoint()`, `useDeleteEndpoint()` mutations (retry disabled) that invalidate `["endpoints"]`; `<EndpointForm onSecret={(secret: string) => void}>` — the form does NOT depend on `SecretReveal` (Task 20). It calls `onSecret(secret)` on a successful create; Task 20 supplies the actual modal as the `onSecret` handler at the call site. This removes the forward dependency the review flagged.

- [ ] **Step 1: Failing test** — render `<EndpointForm onSecret={spy}>`; submit `{url:"https://x", description:"d"}`; MSW asserts a `POST /v1/endpoints` with that JSON body and `Content-Type: application/json`; on the `201 {id,url,secret}` response assert `spy` was called with `"whsec_abc"`. Also a `422` SSRF response surfaces an inline error.

```tsx
// sketch — MSW: POST /v1/endpoints → 201 {id:"e1",url:"https://x",secret:"whsec_abc"}
// const onSecret = vi.fn(); render(<EndpointForm onSecret={onSecret}/>);
// expect the create mutation called; expect(onSecret).toHaveBeenCalledWith("whsec_abc"); on 422 expect detail rendered inline.
```

- [ ] **Step 2: Run → FAIL.** Run: `cd clients/web && npm run test -- EndpointForm`
- [ ] **Step 3: Implement** zod-validated form (url required non-empty, description optional) using an `EndpointRow`-adjacent input schema; the create mutation `request("POST","/v1/endpoints",{body})` returns `{id,url,secret}` (a one-off response schema), and the form invokes the `onSecret(secret)` prop on success (the modal consuming it is created + wired in Task 20). Edit = `PATCH /v1/endpoints/:id` with only changed fields → `204`. Delete = `DELETE /v1/endpoints/:id` → `204` with a confirm dialog. All mutations set `retry: false`. Surface `ApiProblem.detail` inline (esp. `422` SSRF).
- [ ] **Step 4: Run → PASS.** Run: `cd clients/web && npm run typecheck && npm run test -- EndpointForm`
- [ ] **Step 5: Commit** — `git commit -m "feat(web): endpoint create/edit/delete forms"`

### Task 19: Subscription create / edit / delete forms

**Files:** `clients/web/src/query/subscriptionMutations.ts`, `routes/SubscriptionForm.tsx`, `routes/SubscriptionForm.test.tsx`. Modify the subscription list/detail to add New/Edit/Delete.

**Interfaces:** Produces `useCreateSubscription()`, `useUpdateSubscription()`, `useDeleteSubscription()` (retry disabled).

- [ ] **Step 1: Failing test** — submit `{topic_pattern:"orders.*", endpoint_id:"e1", max_attempts:8}`; MSW asserts `POST /v1/subscriptions` JSON body → `201 {id}`. A client-side zod error fires when `max_attempts` is 0 (must be 1..100) and when `rate_limit_rps` ≤ 0, BEFORE any request. A server `409` (endpoint deleted) and `422` (check) render inline.
- [ ] **Step 2: Run → FAIL.**
- [ ] **Step 3: Implement** a zod input schema (`topic_pattern` non-empty, `endpoint_id` non-empty, `max_attempts` int 1..100, `rate_limit_rps` optional > 0, `backoff_policy` optional object) validated before submit; create → `201 {id}`; edit (`active`/`max_attempts`/`rate_limit_rps`/`backoff_policy`) → `204`; delete → `204` with confirm. Map `409`→"endpoint not available / subscription deleted", `422`→inline.
- [ ] **Step 4: Run → PASS.**
- [ ] **Step 5: Commit** — `git commit -m "feat(web): subscription create/edit/delete forms with zod validation"`

### Task 20: Rotate-secret reveal modal (secret non-persistence)

**Files:** Create `clients/web/src/components/SecretReveal.tsx`, `clients/web/src/query/rotateSecret.ts`, `clients/web/src/components/SecretReveal.test.tsx`. Modify `EndpointDetail.tsx` (add "Rotate secret").

**Interfaces:** Produces `useRotateSecret()` (returns `{secret}` from `POST /v1/endpoints/:id/rotate-secret`); `<SecretReveal secret onClose>` shows it once.

- [ ] **Step 1: Failing test** — render `<SecretReveal secret="whsec_x" onClose=fn/>`; assert the secret text + the "will not be shown again" warning + a copy button render; assert **nothing is written to `localStorage`/`sessionStorage`** (spy on both, expect 0 setItem calls) and that closing fires `onClose`.

```tsx
// spy: vi.spyOn(Storage.prototype, "setItem"); after render+close, expect(setItem).not.toHaveBeenCalled();
```

- [ ] **Step 2: Run → FAIL.** Run: `cd clients/web && npm run test -- SecretReveal`
- [ ] **Step 3: Implement** `<SecretReveal>`: the secret lives only in props/local state; a `navigator.clipboard.writeText` copy button; a prominent warning; an explicit "I saved it" close. **Never** call any storage API, never `console.log` the secret. `useRotateSecret()` mutation (`retry:false`) feeds its `{secret}` into the modal. On the endpoint create path (Task 18) the same modal shows the create-time secret.
- [ ] **Step 4: Run → PASS.** Run: `cd clients/web && npm run typecheck && npm run test -- SecretReveal`
- [ ] **Step 5: Commit** — `git commit -m "feat(web): show-once secret reveal modal (no persistence)"`

### Task 21: DLQ replay action

**Files:** `clients/web/src/query/replay.ts`, modify `routes/DLQ.tsx`, create `routes/DLQReplay.test.tsx`.

**Interfaces:** Produces `useReplay()` mutation → `POST /v1/dlq/:id/replay`.

- [ ] **Step 1: Failing test** — a "Replay" button per non-replayed row; MSW `200 {delivery_id,state:"pending"}` → success toast + list invalidation; MSW `410` → "replay window expired"; `409` → "not replayable". Mutation retry disabled.
- [ ] **Step 2: Run → FAIL.**
- [ ] **Step 3: Implement** `useReplay()` (`request("POST", \`/v1/dlq/\${id}/replay\`)`, `retry:false`), a per-row button (hidden when `replayed_at` set), success/`409`/`410` UX, invalidate `["dlq"]` + `["deliveries"]` on success.
- [ ] **Step 4: Run → PASS.**
- [ ] **Step 5: Commit** — `git commit -m "feat(web): DLQ replay with 409/410 UX"`

### Task 22: Send-test-event action

**Files:** `clients/web/src/query/testEvent.ts`, `clients/web/src/routes/TestEvent.tsx`, `routes/TestEvent.test.tsx`. Add a nav entry.

**Interfaces:** Produces `useTestEvent()` → `POST /api/test-event`.

- [ ] **Step 1: Failing test** — form with topic + JSON payload textarea; submit `{topic:"demo.x", payload:{a:1}}` → MSW `202 {event_id,delivery_ids}` → success shows `event_id`; the submit button is **disabled while in-flight** (double-submit guard); invalid JSON in the textarea shows a client error before submit.
- [ ] **Step 2: Run → FAIL.**
- [ ] **Step 3: Implement** the form: parse the payload textarea with `JSON.parse` (zod object check), `useTestEvent()` mutation (`retry:false`), disable the button while `isPending`, render the returned `event_id`/`delivery_ids`. Document in-UI: "each send is a new event."
- [ ] **Step 4: Run → PASS.**
- [ ] **Step 5: Commit** — `git commit -m "feat(web): send-test-event form (new event per click)"`

**M-C4 VERIFY:** `make web-verify` green. **Codex pre-gate** confirms: secret never persisted/logged (Task 20), replay status mapping (Task 21), all mutations JSON + retry-disabled, no admin/producer token reachable from the SPA.

---

## Milestone M-C5 — Deploy + e2e + docs (Tasks 23–25) · VERIFY: `make web-verify && make web-e2e` · **Codex pre-gate: YES**

> Wire the real stack, prove the loop end-to-end through a browser, ship honest docs. Writes `.agent/SLICEC_DONE`.

### Task 23: compose `dashboard` service + producer-key provisioner + real dist embed

**Files:**
- Modify: `deploy/compose/docker-compose.yml` (add `dashboard-provisioner` one-shot + `dashboard` service)
- Modify: `Makefile` (`web-build` already exists; ensure `dist` is copied into `internal/dashboard/dist` before `go build` of the image — add a `dashboard-assets` step)
- Modify: `Dockerfile` (multi-stage: node build of `clients/web` → copy `dist` into `internal/dashboard/dist` → go build `hookrail-dashboard`)

**Interfaces:** Produces a running `dashboard` service on `:8085` reachable by the e2e + a provisioned producer key file.

- [ ] **Step 1: Add the provisioner one-shot** to compose, mirroring the existing `migrate` one-shot. A named volume carries the key file; because the runtime image is non-root (`USER 65532:65532`, `Dockerfile:11`), the provisioner overrides `user: "0:0"` so it can write the volume, writes `0644` so the non-root `dashboard` can READ it, and extracts ONLY the key value (not the whole `producer_key=…` line). It depends on `migrate` completing:

```yaml
  dashboard-provisioner:
    build: ../..
    user: "0:0"
    entrypoint: ["/bin/sh","-c"]
    command:
      - 'hookrail-ctl create-producer-key -name dashboard | awk -F= "/^producer_key=/{print \$2}" > /run/secrets/producer_key && chmod 0644 /run/secrets/producer_key'
    environment: *hookrail-env
    volumes: [dashboard-secret:/run/secrets]
    depends_on:
      postgres: { condition: service_healthy }
      migrate: { condition: service_completed_successfully }

volumes:
  # add alongside the existing top-level volumes: block
  dashboard-secret:
```

(`awk` is shadowed only in Mit's interactive zsh — inside the alpine container it is the real `awk`. Use the `*hookrail-env` anchor for `HOOKRAIL_DATABASE_URL`/creds.)

- [ ] **Step 2: Add the `dashboard` service** to compose: built from the Dockerfile dashboard target, env `HOOKRAIL_ADMIN_TOKEN: dev-admin-token`, `HOOKRAIL_ADMIN_URL: http://admin:8082`, `HOOKRAIL_INGRESS_URL: http://api:8080`, `HOOKRAIL_DASHBOARD_PASSWORD: dev-dashboard-pw`, `HOOKRAIL_DASHBOARD_SESSION_KEY: dev-session-key-0123456789abcdef0123`, `HOOKRAIL_PRODUCER_KEY_FILE: /run/secrets/producer_key`, `HOOKRAIL_DASHBOARD_INSECURE_COOKIE: "true"`, `volumes: [dashboard-secret:/run/secrets:ro]`, ports `8085:8085`, and **completion-gated** deps:

```yaml
    depends_on:
      admin: { condition: service_started }
      api: { condition: service_started }
      dashboard-provisioner: { condition: service_completed_successfully }
```
- [ ] **Step 3: Multi-stage Dockerfile** target for `hookrail-dashboard`: `node:20` stage runs `npm ci && npm run build` in `clients/web`, copies `clients/web/dist` → `internal/dashboard/dist`, then the Go build stage builds `./cmd/hookrail-dashboard`. (Local `make web-build` + a `cp -r clients/web/dist internal/dashboard/dist` keeps `go build` working outside Docker; ensure `internal/dashboard/dist` real output is gitignored except the committed stub `index.html`.)
- [ ] **Step 4: Bring the stack up and smoke it.** Run: `cd hookrail && docker compose -f deploy/compose/docker-compose.yml up -d --build dashboard && curl -fsS http://localhost:8085/healthz`
Expected: `200`; then `docker compose ... down -v`.
- [ ] **Step 5: Commit** — `git add deploy/compose/docker-compose.yml Dockerfile Makefile .gitignore && git commit -m "feat(deploy): dashboard compose service + producer-key provisioner"`

### Task 24: Playwright e2e + `make web-e2e` (live stack, 0-container teardown)

**Files:** Create `clients/web/playwright.config.ts`, `clients/web/e2e/dashboard.spec.ts`, `scripts/web-e2e.sh`. Modify `Makefile` (`web-e2e` target).

**Interfaces:** Produces a gate-only `make web-e2e` running the full login→create→test-event→succeeded→replay flow through a real browser.

- [ ] **Step 1: Write the e2e spec** — Playwright: navigate to `http://localhost:8085`, log in with `dev-dashboard-pw`, create an endpoint (`http://test-receiver:9090/succeed`... reachable via the BFF→admin SSRF policy; use a host allowed by the dev SSRF policy), create a subscription matching `demo.*`, send a test event `{topic:"demo.e2e", payload:{ok:true}}`, then poll the deliveries view until a delivery shows `succeeded` (30s deadline), then exercise a DLQ replay path is optional if no dead-letter exists — assert at minimum the happy path reaches `succeeded`.
- [ ] **Step 2: Write `scripts/web-e2e.sh`** mirroring `scripts/e2e.sh` but cwd-independent (M-B4 lesson):

```bash
#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
COMPOSE="docker compose -f $ROOT/deploy/compose/docker-compose.yml"
export HOOKRAIL_MASTER_KEY="${HOOKRAIL_MASTER_KEY:-000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f}"
$COMPOSE up -d --build
trap '$COMPOSE down -v' EXIT
for i in $(seq 1 90); do curl -fsS http://localhost:8085/healthz >/dev/null 2>&1 && break; sleep 1; done
cd "$ROOT/clients/web"
npx playwright install --with-deps chromium
npm run e2e
# teardown via trap; verify 0 containers after
```

Add `web-e2e` to the Makefile: `web-e2e: ; ROOT=$(CURDIR) bash scripts/web-e2e.sh && test "$$(docker compose -f deploy/compose/docker-compose.yml ps -q | wc -l | tr -d ' ')" = "0"`. (The final assertion enforces the M-B4 0-container rule; the gate also checks the wrapper exit code.)

- [ ] **Step 3: Run the e2e locally.** Run: `cd hookrail && make web-e2e`
Expected: Playwright passes; teardown leaves **0** containers; wrapper exits `0`.
- [ ] **Step 4: Confirm it is NOT added to CI** (gate-only, per D-C7) — do not touch `.github/workflows/ci.yml` for e2e.
- [ ] **Step 5: Commit** — `git add clients/web/playwright.config.ts clients/web/e2e scripts/web-e2e.sh clients/web/package.json Makefile && git commit -m "feat(web): gate-only Playwright e2e through the live stack"`

### Task 25: README + residual risks + `.agent/SLICEC_DONE`

**Files:** Modify `README.md`; create `.agent/SLICEC_DONE` (gitignored marker — actually written by the runner/gate, see note).

- [ ] **Step 1** Add a `## Dashboard (P1 Slice C)` section to `README.md`: how to run (`make web-build` + compose `dashboard`), the env vars (§2.1), and a one-command demo. Include the **Limits & honesty** subsection verbatim from design §8 (single shared password / no RBAC; stateless cookie no revocation; `next_cursor` forgeable; TLS/public exposure is Slice E; admin-token blast radius; test-event key handling).
- [ ] **Step 2** Run the full milestone VERIFY. Run: `cd hookrail && make web-verify && make web-e2e`
Expected: both green; 0 leftover containers.
- [ ] **Step 3** Commit — `git add README.md && git commit -m "docs: dashboard usage + honest residual risks (P1 Slice C)"`
- [ ] **Step 4** (gate step, not committed) The Claude gate writes `.agent/SLICEC_DONE` after the push, signaling the runner to stop.

**M-C5 VERIFY:** `make web-verify && make web-e2e` green (live stack, 0-container teardown, exit 0). **Codex pre-gate** confirms: compose exposure is local-only + insecure-cookie scoped to dev, the provisioner issues a real producer key safely, and the e2e teardown is clean.

---

## Self-Review (against the design spec)

**Spec coverage:** §1 topology → M-C1 T9 (mux), T7/T8 (proxies). §2.1 config → T3. §2.2 cookie/kid → T4. §2.3 middleware → T6. §2.4 CSRF → T6. §3.1–3.2 allowlist/hygiene → T7. §3.3 test-event + provisioning → T8 + T2 + T23. §4.0 openapi drift → T1. §4.1–4.2 contract/cursor → T11 (schemas) + T1. §5.1–5.4 SPA/client/error → T10–T13. §5.5 secret reveal → T20. §6 CI/targets → T10 (web job + targets) + T24 (web-e2e). §7 milestones → headers. §8 residual risks → T25. All covered.

**Placeholder scan:** the M-C3 view tasks and a few M-C4/e2e steps describe component/test shape compactly rather than pasting every line — they carry exact hooks, paths, schemas, request/response contracts, and assertions, which is the reviewable contract; the executing loop fills conventional React/MSW boilerplate against those exact interfaces (the security-critical M-C1 Go tasks are fully spelled out). No "TBD/handle errors appropriately" placeholders remain.

**Type consistency:** `request<T>(method,path,{schema,body})`, `ApiProblem`, `Page(item)`, `DeliveryState`, the zod `*Row` schemas, `LoadConfig`/`Config`, `NewServer`/`Handler`, `Sessions.Issue/Valid`, `requireSession`, `proxyAdmin`, `handleTestEvent`, `adminRoutes` — names are used consistently across tasks. `HOOKRAIL_PRODUCER_KEY_FILE` (not a literal key) is consistent T3/T8/T23.

**Codex pre-gates:** M-C1, M-C4, M-C5 (matches design D-C9). VERIFY chains: Go (`make build/lint/test/itest`) for M-C1; `make web-verify` for M-C2–M-C4; `make web-verify && make web-e2e` for M-C5.

---

## Codex plan-review folded (rev1 → rev2)

One adversarial Codex gpt-5.5 (high effort, read-only, inside the repo) round —
`.codex-sliceC-plan-review-output.md`. VERDICT REWORK; all 10 findings folded:

1. **[BLOCKER] mux catch-all swallowed unknown `/v1/*`** (`GET /` → `serveStatic` → index.html, not 404).
   → Task 9 `serveStatic` now 404s any `/v1/`/`/api/` prefix; Task 7 note corrected.
2. **[BLOCKER] CSRF middleware 415s no-body mutations** (logout/replay/rotate send no `Content-Type`).
   → Task 12 `request()` sets `Content-Type: application/json` on every POST/PATCH/DELETE; +test.
3. **[BLOCKER] Task 2 ctl snippet used undefined `ctx`/`fatal`** vs the real `config.Load()`/`store.Open`/
   `slog.Error`/if-block dispatch. → rewritten to real conventions + `usage()` update + a path test.
4. **[MAJOR] Task 1 test would be skipped** — `openapi_conformance_test.go` is `//go:build integration`.
   → param/header test moved to a new untagged `openapi_params_test.go` (runs under `make test`).
5. **[MAJOR] subscriptions LIST has no `include_deleted`** (`store/admin.go:155` hardcodes `deleted_at IS
   NULL`). → Task 1 scopes `include_deleted` to endpoints list + endpoints `{id}` + subscriptions `{id}`
   only; test asserts the list op must NOT document it.
6. **[MAJOR] status/header conformance untasked.** → Task 1 adds `Cache-Control: no-store` headers to the
   create-endpoint `201` + rotate-secret `200` openapi responses + a test that fails without them.
7. **[MAJOR] Task 18 referenced SecretReveal (Task 20) before it exists.** → Task 18 uses an `onSecret`
   callback prop; the modal is created + wired at the call site in Task 20.
8. **[MAJOR] provisioner ordering/writability flaky** (non-root image, `/run/secrets` write, no completion
   gate). → Task 23 uses a named `dashboard-secret` volume, provisioner runs `user:"0:0"` writing `0644`,
   extracts only the key value, and the dashboard `depends_on … service_completed_successfully`.
9. **[MINOR] proxy used `io.LimitReader` (silent truncation) + no absolute-form reject.** → Task 7 uses
   `http.MaxBytesReader` (→ 413 on overflow), rejects absolute-form/non-`/v1/` targets; +413 test.
10. **[MINOR] stale Go version** (`go.mod` is `1.26.4`; CI pins setup-go `1.24`). → Global Constraints +
    Tech Stack corrected; CI `go-version` left as the repo's standing config (not a Slice C change).

Review loop closed (remaining items are implementation-grade → TDD + the per-milestone Opus gate + the
M-C1/M-C4/M-C5 Codex pre-gates).

