package admin

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/mit112/hookrail/internal/httpx"
)

// decodeJSON enforces application/json + a body-size cap and decodes into v.
// On any failure it writes a Problem (415 / 413 / 400) and returns false.
func decodeJSON(w http.ResponseWriter, r *http.Request, v any, maxBytes int64) bool {
	if r.Header.Get("Content-Type") != "application/json" {
		httpx.Problem(w, http.StatusUnsupportedMediaType, "bad content-type", "application/json required")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			httpx.Problem(w, http.StatusRequestEntityTooLarge, "request too large", "request body exceeds limit")
			return false
		}
		httpx.Problem(w, http.StatusBadRequest, "invalid body", "could not decode JSON")
		return false
	}
	return true
}
