package admin

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"

	"github.com/mit112/hookrail/internal/httpx"
)

// decodeJSON enforces application/json + a body-size cap and decodes exactly one
// JSON value into v. On any failure it writes a Problem (415 / 413 / 400) and
// returns false. It requires the body to END after the single value, so a small
// valid object followed by a large suffix cannot slip past the MaxBytes cap.
func decodeJSON(w http.ResponseWriter, r *http.Request, v any, maxBytes int64) bool {
	// Accept "application/json" with optional parameters (e.g. "; charset=utf-8").
	mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mt != "application/json" {
		httpx.Problem(w, http.StatusUnsupportedMediaType, "bad content-type", "application/json required")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(v); err != nil {
		return decodeFail(w, err)
	}
	// Reject trailing data: a second token must be clean EOF (through the same
	// capped reader, so a huge suffix surfaces as a 413, junk as a 400).
	if err := dec.Decode(&json.RawMessage{}); !errors.Is(err, io.EOF) {
		return decodeFail(w, err)
	}
	return true
}

func decodeFail(w http.ResponseWriter, err error) bool {
	var mbe *http.MaxBytesError
	if errors.As(err, &mbe) {
		httpx.Problem(w, http.StatusRequestEntityTooLarge, "request too large", "request body exceeds limit")
		return false
	}
	httpx.Problem(w, http.StatusBadRequest, "invalid body", "expected exactly one JSON object")
	return false
}
