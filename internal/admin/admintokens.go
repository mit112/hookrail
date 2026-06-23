package admin

import (
	"net/http"

	"github.com/mit112/hookrail/internal/httpx"
)

const (
	maxAdminBody         = 64 << 10
	maxActiveAdminTokens = 256
)

type createAdminTokenReq struct {
	Role  string `json:"role"`
	Label string `json:"label"`
}

// createAdminToken mints a role-scoped admin token. The plaintext is returned
// once with Cache-Control: no-store and never persisted (only its SHA-256).
func (s *Server) createAdminToken(w http.ResponseWriter, r *http.Request) {
	var req createAdminTokenReq
	if !decodeJSON(w, r, &req, maxAdminBody) {
		return
	}
	if _, ok := ParseRole(req.Role); !ok {
		httpx.Problem(w, http.StatusUnprocessableEntity, "invalid role", "role must be viewer, operator, or admin")
		return
	}
	if req.Label == "" || len(req.Label) > 200 {
		httpx.Problem(w, http.StatusUnprocessableEntity, "invalid label", "label must be 1..200 chars")
		return
	}
	id, plaintext, capped, err := s.store.CreateAdminTokenCapped(r.Context(), req.Role, req.Label, maxActiveAdminTokens)
	if err != nil {
		httpx.Problem(w, http.StatusServiceUnavailable, "create failed", "could not persist token")
		return
	}
	if capped {
		httpx.Problem(w, http.StatusConflict, "too many tokens", "active admin-token limit reached")
		return
	}
	w.Header().Set("Cache-Control", "no-store") // plaintext token in body
	writeJSON(w, http.StatusCreated, map[string]string{
		"id": id, "token": plaintext, "role": req.Role, "label": req.Label,
	})
}

func (s *Server) listAdminTokens(w http.ResponseWriter, r *http.Request) {
	cursor, err := httpx.DecodeCursor(r.URL.Query().Get("cursor"))
	if err != nil {
		httpx.Problem(w, http.StatusBadRequest, "bad cursor", "cursor is not decodable")
		return
	}
	limit := httpx.ClampLimit(r.URL.Query().Get("limit"), 50, 200)
	rows, err := s.store.ListAdminTokens(r.Context(), cursor, limit)
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

func (s *Server) revokeAdminToken(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	found, err := s.store.RevokeAdminToken(r.Context(), id)
	if err != nil {
		httpx.Problem(w, http.StatusServiceUnavailable, "revoke failed", "query error")
		return
	}
	if !found {
		// Distinguish "never existed" (404) from "already revoked" (204 no-op).
		exists, err := s.store.AdminTokenExists(r.Context(), id)
		if err != nil {
			httpx.Problem(w, http.StatusServiceUnavailable, "revoke failed", "query error")
			return
		}
		if !exists {
			httpx.Problem(w, http.StatusNotFound, "not found", "no admin token with that id")
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}
