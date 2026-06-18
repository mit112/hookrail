//go:build integration

package admin_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestRotateSecret(t *testing.T) {
	srv, _ := newServer(t)
	ew := do(t, srv, "POST", "/v1/endpoints", map[string]string{"url": "https://example.com/h"})
	var ep struct{ ID, Secret string }
	_ = json.Unmarshal(ew.Body.Bytes(), &ep)

	w := do(t, srv, "POST", "/v1/endpoints/"+ep.ID+"/rotate-secret", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("rotate = %d body=%s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
	var out struct{ Secret string }
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if !strings.HasPrefix(out.Secret, "whsec_") || out.Secret == ep.Secret {
		t.Fatalf("rotated secret invalid or unchanged: %q", out.Secret)
	}
	// rotating an unknown endpoint → 404
	if nf := do(t, srv, "POST", "/v1/endpoints/nope/rotate-secret", nil); nf.Code != http.StatusNotFound {
		t.Fatalf("rotate unknown = %d, want 404", nf.Code)
	}
}
