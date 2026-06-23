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
	// Content-Type with parameters is accepted (mime.ParseMediaType).
	if c := call("application/json; charset=utf-8", `{"a":"x"}`); c != http.StatusOK {
		t.Errorf("application/json; charset=utf-8 = %d, want untouched 200", c)
	}
	// Structurally valid JSON that exceeds the 32-byte cap -> 413.
	if c := call("application/json", `{"a":"`+strings.Repeat("x", 100)+`"}`); c != http.StatusRequestEntityTooLarge {
		t.Errorf("oversized = %d, want 413", c)
	}
	// A valid object followed by trailing data is rejected (no cap bypass).
	if c := call("application/json", `{"a":"x"}{"a":"y"}`); c != http.StatusBadRequest {
		t.Errorf("trailing data = %d, want 400", c)
	}
	// A small valid object + a huge suffix surfaces as 413 (cap hit on the suffix).
	if c := call("application/json", `{"a":"x"}`+strings.Repeat(" ", 100)); c != http.StatusRequestEntityTooLarge {
		t.Errorf("valid+huge-suffix = %d, want 413", c)
	}
	// Happy path leaves the recorder untouched (200); handler writes real status later.
	if c := call("application/json", `{"a":"x"}`); c != http.StatusOK {
		t.Errorf("valid body wrote %d, want untouched 200", c)
	}
}
