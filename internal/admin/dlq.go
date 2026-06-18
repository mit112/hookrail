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

// replayDLQ is a stub; replaced in Task 13.
func (s *Server) replayDLQ(w http.ResponseWriter, r *http.Request) { stub(w) }

func parseTime(v string) (time.Time, bool) {
	if v == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, v)
	return t, err == nil
}
