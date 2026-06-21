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
	mux.HandleFunc("POST /v1/deliveries/{id}/skip", s.authAdmin(s.skipDelivery))
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
