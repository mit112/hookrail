package admin

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDecodeJSON(t *testing.T) {
	type body struct {
		A string `json:"a"`
	}
	call := func(ct, payload string) int {
		r := httptest.NewRequest("POST", "/x", strings.NewReader(payload))
		if ct != "" {
			r.Header.Set("Content-Type", ct)
		}
		w := httptest.NewRecorder()
		var v body
		decodeJSON(w, r, &v, 32)
		return w.Code
	}
	if c := call("text/plain", `{"a":"x"}`); c != http.StatusUnsupportedMediaType {
		t.Errorf("wrong content-type = %d, want 415", c)
	}
	// Structurally valid JSON that exceeds the 32-byte cap -> 413 (the cap is
	// hit while reading the long string value, not a syntax error).
	if c := call("application/json", `{"a":"`+strings.Repeat("x", 100)+`"}`); c != http.StatusRequestEntityTooLarge {
		t.Errorf("oversized = %d, want 413", c)
	}
	// Happy path leaves the recorder untouched (200); the handler writes the real status later.
	if c := call("application/json", `{"a":"x"}`); c != http.StatusOK {
		t.Errorf("valid body wrote %d, want untouched 200", c)
	}
}
