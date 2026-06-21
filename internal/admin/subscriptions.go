package admin

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/mit112/hookrail/internal/backoff"
	"github.com/mit112/hookrail/internal/httpx"
	"github.com/mit112/hookrail/internal/store"
)

type createSubReq struct {
	TopicPattern  string          `json:"topic_pattern"`
	EndpointID    string          `json:"endpoint_id"`
	MaxAttempts   int             `json:"max_attempts"`
	RateLimitRPS  *float64        `json:"rate_limit_rps"`
	BackoffPolicy json.RawMessage `json:"backoff_policy"`
	Ordered       bool            `json:"ordered"`
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
	if len(req.BackoffPolicy) > 0 {
		if err := backoff.Validate(req.BackoffPolicy); err != nil {
			httpx.Problem(w, http.StatusUnprocessableEntity, "invalid backoff_policy", err.Error())
			return
		}
	}
	id, err := s.store.CreateSubscriptionFull(r.Context(), store.SubInput{
		TopicPattern: req.TopicPattern, EndpointID: req.EndpointID,
		MaxAttempts: req.MaxAttempts, RateLimitRPS: req.RateLimitRPS, BackoffPolicy: req.BackoffPolicy,
		Ordered: req.Ordered,
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
	Ordered       *bool           `json:"ordered"`
}

func (s *Server) patchSubscription(w http.ResponseWriter, r *http.Request) {
	var req patchSubReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Problem(w, http.StatusBadRequest, "invalid body", "malformed patch")
		return
	}
	id := r.PathValue("id")
	if len(req.BackoffPolicy) > 0 {
		if err := backoff.Validate(req.BackoffPolicy); err != nil {
			httpx.Problem(w, http.StatusUnprocessableEntity, "invalid backoff_policy", err.Error())
			return
		}
	}
	if req.Ordered != nil {
		current, err := s.store.GetSubscription(r.Context(), id, false)
		if errors.Is(err, store.ErrNotFound) {
			// will be handled below by UpdateSubscription
		} else if err != nil {
			httpx.Problem(w, http.StatusServiceUnavailable, "update failed", "query error")
			return
		} else if current.Ordered != *req.Ordered {
			httpx.Problem(w, http.StatusConflict, "ordered immutable",
				"ordered flag cannot be changed after creation")
			return
		}
	}
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
