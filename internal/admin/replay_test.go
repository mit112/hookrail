//go:build integration

package admin_test

import (
	"net/http"
	"testing"
)

func TestReplayHandlerStatusCodes(t *testing.T) {
	srv, st := newServer(t)
	// unknown → 404
	if w := do(t, srv, "POST", "/v1/dlq/nope/replay", nil); w.Code != http.StatusNotFound {
		t.Fatalf("unknown replay = %d, want 404", w.Code)
	}
	// dead-lettered → 200
	id := seedDeadLetter(t, st, "rh.*")
	if w := do(t, srv, "POST", "/v1/dlq/"+id+"/replay", nil); w.Code != http.StatusOK {
		t.Fatalf("replay = %d, want 200", w.Code)
	}
	// replaying again (now pending) → 409
	if w := do(t, srv, "POST", "/v1/dlq/"+id+"/replay", nil); w.Code != http.StatusConflict {
		t.Fatalf("double replay = %d, want 409", w.Code)
	}
}
