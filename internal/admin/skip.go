package admin

import (
	"errors"
	"net/http"

	"github.com/mit112/hookrail/internal/httpx"
	"github.com/mit112/hookrail/internal/store"
)

func (s *Server) skipDelivery(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	nextHead, err := s.store.SkipHead(r.Context(), id, "admin", "manual skip via API")
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			httpx.Problem(w, http.StatusNotFound, "not found", "no delivery with that id")
			return
		}
		if errors.Is(err, store.ErrConflict) {
			httpx.Problem(w, http.StatusConflict, "conflict", "delivery is not dead-lettered or not the head")
			return
		}
		httpx.Problem(w, http.StatusServiceUnavailable, "skip failed", "query error")
		return
	}
	// best-effort re-publish; sweeper repairs on failure (design §4.1 step 5)
	if nextHead != nil {
		if perr := s.queue.Publish(r.Context(), *nextHead); perr != nil {
			_ = perr // non-fatal
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"delivery_id":           id,
		"state":                 "skipped",
		"next_head_delivery_id": nextHead,
	})
}
