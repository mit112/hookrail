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

// rotateSecret generates a new endpoint secret and returns it once with
// Cache-Control: no-store. Cutover is eventual (bounded by worker HTTP attempt
// timeout). A deleted/absent endpoint returns 404.
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
