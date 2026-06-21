package admin

import (
	"errors"
	"net/http"
	"strings"

	"github.com/mit112/hookrail/internal/httpx"
	"github.com/mit112/hookrail/internal/store"
)

// orderedKeyCursorSep separates subscription_id and ordering_key in the
// opaque keyset cursor for GET /v1/ordered-keys pagination.
const orderedKeyCursorSep = "\x00"

func decodeOrderedKeyCursor(raw string) (subID, key string, err error) {
	cursor, err := httpx.DecodeCursor(raw)
	if err != nil {
		return "", "", err
	}
	if cursor == "" {
		return "", "", nil
	}
	// Split on the first \x00; ordering_key may contain additional data
	// but never \x00 (TEXT column constraint in practice).
	idx := strings.Index(cursor, orderedKeyCursorSep)
	if idx < 0 {
		return "", "", errors.New("malformed cursor")
	}
	return cursor[:idx], cursor[idx+1:], nil
}

func (s *Server) listOrderedKeys(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if q.Get("blocked") != "true" {
		// Only ?blocked=true is supported for now.
		httpx.Problem(w, http.StatusBadRequest, "bad request", "?blocked=true is required")
		return
	}

	afterSubID, afterKey, err := decodeOrderedKeyCursor(q.Get("cursor"))
	if err != nil {
		httpx.Problem(w, http.StatusBadRequest, "bad cursor", "cursor is not decodable")
		return
	}

	limit := httpx.ClampLimit(q.Get("limit"), 50, 200)
	f := store.BlockedKeyFilter{
		AfterSubID: afterSubID,
		AfterKey:   afterKey,
		Limit:      limit,
	}

	rows, err := s.store.BlockedKeys(r.Context(), f)
	if err != nil {
		httpx.Problem(w, http.StatusServiceUnavailable, "list failed", "query error")
		return
	}

	// writePage base64-encodes the returned key once (the house cursor
	// contract); return the RAW composite so decodeOrderedKeyCursor can
	// DecodeCursor + split it on the next page.
	writePage(w, rows, len(rows), limit, func() string {
		if len(rows) == 0 {
			return ""
		}
		last := rows[len(rows)-1]
		return last.SubscriptionID + orderedKeyCursorSep + last.OrderingKey
	})
}
