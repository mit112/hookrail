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

func (s *Server) replayDLQ(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("delivery_id")
	out, headID, err := s.store.ReplayDeadLetter(r.Context(), id, s.replayAge)
	if err != nil {
		httpx.Problem(w, http.StatusServiceUnavailable, "replay failed", "query error")
		return
	}
	switch out {
	case store.ReplayOK:
		// best-effort re-publish; sweeper repairs on failure (design §4.1 step 5)
		publishID := id
		if headID != nil {
			publishID = *headID
		}
		if perr := s.queue.Publish(r.Context(), publishID); perr != nil {
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

func parseTime(v string) (time.Time, bool) {
	if v == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, v)
	return t, err == nil
}
