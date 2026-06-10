// Package api is the ingress fast path (§3.1): authenticate, validate,
// idempotency, ONE PG transaction, best-effort XADD, 202.
// The 202 durability boundary is the PG commit — never Redis.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/mit112/hookrail/internal/ratelimit"
	"github.com/mit112/hookrail/internal/store"
)

// MaxPayloadBytes is the §5 cap; requests above it get 413.
const MaxPayloadBytes = 256 * 1024

type Publisher interface {
	Publish(ctx context.Context, deliveryID string) error
	Ping(ctx context.Context) error
}

type Server struct {
	store  *store.Store
	queue  Publisher
	limits *ratelimit.Registry // per producer key (§10: floods → 429)
}

func New(s *store.Store, q Publisher, limits *ratelimit.Registry) *Server {
	return &Server{store: s, queue: q, limits: limits}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/events", s.authProducer(s.postEvent))
	mux.HandleFunc("GET /v1/events/{id}", s.authProducer(s.getEvent))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("GET /readyz", s.readyz)
	mux.Handle("GET /metrics", promhttp.Handler())
	return mux
}

type postEventReq struct {
	Topic   string          `json:"topic"`
	Payload json.RawMessage `json:"payload"`
}

func (s *Server) postEvent(w http.ResponseWriter, r *http.Request) {
	keyID := r.Context().Value(producerKeyCtx).(string)
	if !s.limits.Allow(keyID, time.Now()) {
		problem(w, http.StatusTooManyRequests, "rate limited", "per-key ingest rate exceeded")
		return
	}
	// envelope overhead beyond the payload cap is small; cap the whole body
	// slightly above MaxPayloadBytes and enforce the exact cap on payload below
	r.Body = http.MaxBytesReader(w, r.Body, MaxPayloadBytes+4096)
	var req postEventReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			problem(w, http.StatusRequestEntityTooLarge, "payload too large", "payload is capped at 256KB")
			return
		}
		problem(w, http.StatusBadRequest, "invalid body", "expected {\"topic\": string, \"payload\": object}")
		return
	}
	if req.Topic == "" || len(req.Payload) == 0 {
		problem(w, http.StatusBadRequest, "invalid body", "topic and payload are required")
		return
	}
	if len(req.Payload) > MaxPayloadBytes {
		problem(w, http.StatusRequestEntityTooLarge, "payload too large", "payload is capped at 256KB")
		return
	}

	res, err := s.store.IngestEvent(r.Context(), store.IngestParams{
		ProducerKeyID: keyID,
		Topic:         req.Topic,
		Payload:       req.Payload,
		IdemKey:       r.Header.Get("Idempotency-Key"),
		IdemTTL:       24 * time.Hour,
	})
	switch {
	case errors.Is(err, store.ErrIdempotencyConflict):
		problem(w, http.StatusConflict, "idempotency conflict", "Idempotency-Key was reused with a different body")
		return
	case err != nil:
		// §10: PG down → 503, no silent accept. The 202 promise requires the commit.
		slog.Error("ingest failed", "err", err)
		problem(w, http.StatusServiceUnavailable, "ingest unavailable", "could not durably record the event")
		return
	}

	if !res.Replayed {
		// Best-effort XADD after commit (§3.1): failure only delays delivery
		// until the next sweep; it never loses it.
		for _, id := range res.DeliveryIDs {
			if err := s.queue.Publish(r.Context(), id); err != nil {
				slog.Warn("post-commit publish failed; sweeper will repair", "delivery_id", id, "err", err)
				break
			}
		}
	} else {
		w.Header().Set("Idempotent-Replay", "true")
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(res)
}

func (s *Server) getEvent(w http.ResponseWriter, r *http.Request) {
	status, err := s.store.GetEventStatus(r.Context(), r.PathValue("id"))
	if err != nil {
		problem(w, http.StatusNotFound, "event not found", "no event with that id")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(status)
}

func (s *Server) readyz(w http.ResponseWriter, r *http.Request) {
	// PG is the 202 durability boundary (§3.1) — without it we cannot serve.
	if err := s.store.Pool.Ping(r.Context()); err != nil {
		problem(w, http.StatusServiceUnavailable, "not ready", "postgres unreachable")
		return
	}
	// Redis down only degrades dispatch latency (XADD is best-effort; the
	// sweeper repairs) — report it, but do NOT fail readiness over it.
	if err := s.queue.Ping(r.Context()); err != nil {
		w.Header().Set("X-Hookrail-Redis", "degraded")
	}
	w.WriteHeader(http.StatusOK)
}
