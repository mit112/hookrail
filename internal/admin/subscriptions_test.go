//go:build integration

package admin_test

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestSubscriptionLifecycle(t *testing.T) {
	srv, _ := newServer(t)
	// need an endpoint first
	ew := do(t, srv, "POST", "/v1/endpoints", map[string]string{"url": "https://example.com/h"})
	var ep struct{ ID string }
	_ = json.Unmarshal(ew.Body.Bytes(), &ep)

	// create subscription
	w := do(t, srv, "POST", "/v1/subscriptions", map[string]any{
		"topic_pattern": "orders.*", "endpoint_id": ep.ID, "max_attempts": 5,
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("create sub = %d body=%s", w.Code, w.Body.String())
	}
	var sub struct{ ID string }
	_ = json.Unmarshal(w.Body.Bytes(), &sub)

	// reject out-of-range max_attempts (CHECK → 422)
	bad := do(t, srv, "POST", "/v1/subscriptions", map[string]any{
		"topic_pattern": "x.*", "endpoint_id": ep.ID, "max_attempts": 999,
	})
	if bad.Code != http.StatusUnprocessableEntity {
		t.Fatalf("max_attempts=999 = %d, want 422", bad.Code)
	}

	// pause via PATCH active=false
	if p := do(t, srv, "PATCH", "/v1/subscriptions/"+sub.ID, map[string]any{"active": false}); p.Code != http.StatusNoContent {
		t.Fatalf("pause = %d", p.Code)
	}

	// soft-delete then PATCH must be rejected (F3: cannot resume a deleted sub)
	if d := do(t, srv, "DELETE", "/v1/subscriptions/"+sub.ID, nil); d.Code != http.StatusNoContent {
		t.Fatalf("delete = %d", d.Code)
	}
	if p := do(t, srv, "PATCH", "/v1/subscriptions/"+sub.ID, map[string]any{"active": true}); p.Code != http.StatusConflict {
		t.Fatalf("resume-deleted = %d, want 409", p.Code)
	}
}

func TestCreateSubAgainstDeletedEndpointRejected(t *testing.T) {
	srv, _ := newServer(t)
	ew := do(t, srv, "POST", "/v1/endpoints", map[string]string{"url": "https://example.com/h"})
	var ep struct{ ID string }
	_ = json.Unmarshal(ew.Body.Bytes(), &ep)
	_ = do(t, srv, "DELETE", "/v1/endpoints/"+ep.ID, nil)
	w := do(t, srv, "POST", "/v1/subscriptions", map[string]any{"topic_pattern": "a.*", "endpoint_id": ep.ID, "max_attempts": 3})
	if w.Code != http.StatusConflict && w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("create against deleted endpoint = %d, want 409/422", w.Code)
	}
}
