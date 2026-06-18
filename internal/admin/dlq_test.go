//go:build integration

package admin_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

func TestDLQBrowse(t *testing.T) {
	srv, st := newServer(t)
	ctx := context.Background()
	// seed a dead-lettered delivery directly (store-level, no worker needed)
	did := seedDeadLetter(t, st, "dlq.*")
	w := do(t, srv, "GET", "/v1/dlq", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("dlq = %d", w.Code)
	}
	var page struct {
		Items []struct{ DeliveryID string `json:"delivery_id"` } `json:"items"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &page)
	found := false
	for _, it := range page.Items {
		if it.DeliveryID == did {
			found = true
		}
	}
	if !found {
		t.Fatalf("dead-lettered delivery %s not in DLQ page: %s", did, w.Body.String())
	}
	_ = ctx
}

type dlqPage struct {
	Items      []struct{ DeliveryID string `json:"delivery_id"` } `json:"items"`
	NextCursor string                                              `json:"next_cursor"`
}

func TestDLQFilterAndCursor(t *testing.T) {
	srv, st := newServer(t)
	ctx := context.Background()
	// three dead-lettered deliveries on three distinct endpoints
	want := map[string]bool{}
	var oneDID string
	for _, p := range []string{"f1.*", "f2.*", "f3.*"} {
		did := seedDeadLetter(t, st, p)
		want[did] = true
		oneDID = did
	}
	// replayed=false returns exactly the three un-replayed rows
	var all dlqPage
	_ = json.Unmarshal(do(t, srv, "GET", "/v1/dlq?replayed=false", nil).Body.Bytes(), &all)
	if len(all.Items) != 3 {
		t.Fatalf("replayed=false returned %d, want 3", len(all.Items))
	}
	// replayed=true returns none (nothing replayed yet)
	var none dlqPage
	_ = json.Unmarshal(do(t, srv, "GET", "/v1/dlq?replayed=true", nil).Body.Bytes(), &none)
	if len(none.Items) != 0 {
		t.Fatalf("replayed=true returned %d, want 0", len(none.Items))
	}
	// endpoint filter: each seed used a distinct endpoint → filtering by one
	// endpoint returns exactly that one dead-letter
	var epID string
	_ = st.Pool.QueryRow(ctx, `SELECT endpoint_id FROM deliveries WHERE id=$1`, oneDID).Scan(&epID)
	var byEp dlqPage
	_ = json.Unmarshal(do(t, srv, "GET", "/v1/dlq?endpoint_id="+epID, nil).Body.Bytes(), &byEp)
	if len(byEp.Items) != 1 || byEp.Items[0].DeliveryID != oneDID {
		t.Fatalf("endpoint filter = %v, want exactly [%s]", byEp.Items, oneDID)
	}
	// keyset paging: limit=2 → page1 (2 items, cursor), page2 (exactly 1 item);
	// the union of both pages must equal all three seeded ids, no overlap.
	var page1 dlqPage
	_ = json.Unmarshal(do(t, srv, "GET", "/v1/dlq?limit=2", nil).Body.Bytes(), &page1)
	if len(page1.Items) != 2 || page1.NextCursor == "" {
		t.Fatalf("page1 items=%d cursor=%q, want 2 + non-empty cursor", len(page1.Items), page1.NextCursor)
	}
	var page2 dlqPage
	_ = json.Unmarshal(do(t, srv, "GET", "/v1/dlq?limit=2&cursor="+page1.NextCursor, nil).Body.Bytes(), &page2)
	if len(page2.Items) != 1 {
		t.Fatalf("page2 items=%d, want exactly 1 (the remaining row)", len(page2.Items))
	}
	union := map[string]bool{}
	for _, it := range append(append([]struct{ DeliveryID string `json:"delivery_id"` }{}, page1.Items...), page2.Items...) {
		if union[it.DeliveryID] {
			t.Fatalf("cursor page overlap: %s on both pages", it.DeliveryID)
		}
		union[it.DeliveryID] = true
	}
	for did := range want {
		if !union[did] {
			t.Fatalf("paged result missing seeded delivery %s", did)
		}
	}
	// malformed cursor → 400
	if bad := do(t, srv, "GET", "/v1/dlq?cursor=!!!not-base64", nil); bad.Code != http.StatusBadRequest {
		t.Fatalf("malformed cursor = %d, want 400", bad.Code)
	}
}
