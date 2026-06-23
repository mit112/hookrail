// internal/admin/server.go
package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/mit112/hookrail/internal/httpx"
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
	replayAge   time.Duration
}

func New(s *store.Store, q Publisher, masterKey [32]byte, pol ssrf.Policy, limits *ratelimit.Registry, token string, replayAge time.Duration) *Server {
	return &Server{store: s, queue: q, masterKey: masterKey, policy: pol, limits: limits, tokenDigest: digest(token), replayAge: replayAge}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	// Admin surface (design §2) — every /v1/* route is registered via the single
	// routes() registry, each wrapped in authz(minRole) (RBAC R1, authz.go).
	for _, rt := range s.routes() {
		mux.HandleFunc(rt.Method+" "+rt.Pattern, s.authz(rt.MinRole, rt.handler))
	}
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
