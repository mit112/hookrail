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
