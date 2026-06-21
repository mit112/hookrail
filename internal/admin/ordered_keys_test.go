//go:build integration

package admin_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mit112/hookrail/internal/store"
)

func TestOrderedKeysBlockedList(t *testing.T) {
	srv, st := newServer(t)
	ctx := context.Background()

	keyID := seedPipeline(t, st, "orders.*")

	var subID string
	if err := st.Pool.QueryRow(ctx, `SELECT id FROM subscriptions LIMIT 1`).Scan(&subID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Pool.Exec(ctx, `UPDATE subscriptions SET ordered=true WHERE id=$1`, subID); err != nil {
		t.Fatal(err)
	}

	ingest := func(seq int) string {
		res, err := st.IngestEvent(ctx, store.IngestParams{
			ProducerKeyID:       keyID,
			Topic:               "orders.x",
			Payload:             []byte(fmt.Sprintf(`{"seq":%d}`, seq)),
			OrderingKey:         "cust-1",
			OrderedKeyBacklogMax: 10000,
		})
		if err != nil {
			t.Fatalf("ingest seq %d: %v", seq, err)
		}
		if len(res.DeliveryIDs) != 1 {
			t.Fatalf("ingest seq %d: got %d deliveries, want 1", seq, len(res.DeliveryIDs))
		}
		return res.DeliveryIDs[0]
	}

	did1 := ingest(1)
	did2 := ingest(2)
	did3 := ingest(3)
	_, _ = did2, did3 // used to build backlog; assertions check BacklogCount/OldestSuccessorAgeSec

	// Drive did1 → dead_lettered, then RecomputeCursor to set head_delivery_id + blocked_reason
	if _, err := st.Pool.Exec(ctx, `UPDATE deliveries SET state='dead_lettered', lease_until=NULL WHERE id=$1`, did1); err != nil {
		t.Fatal(err)
	}
	{
		tx, err := st.Pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx) //nolint:errcheck
		if _, err := tx.Exec(ctx,
			`SELECT true FROM ordered_key_state WHERE subscription_id=$1 AND ordering_key=$2 FOR UPDATE`,
			subID, "cust-1"); err != nil {
			t.Fatal(err)
		}
		if err := st.RecomputeCursor(ctx, tx, subID, "cust-1"); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
	}
	time.Sleep(10 * time.Millisecond)

	// Sub-test A: no auth → 401
	t.Run("no auth", func(t *testing.T) {
		w := doNoAuth(t, srv, "GET", "/v1/ordered-keys?blocked=true", nil)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("no auth = %d, want 401: %s", w.Code, w.Body.String())
		}
	})

	// Sub-test B: wrong token → 401
	t.Run("wrong token", func(t *testing.T) {
		w := doWithToken(t, srv, "GET", "/v1/ordered-keys?blocked=true", nil, "wrong")
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("wrong token = %d, want 401: %s", w.Code, w.Body.String())
		}
	})

	// Sub-test C: valid request returns blocked key
	t.Run("valid block list", func(t *testing.T) {
		w := do(t, srv, "GET", "/v1/ordered-keys?blocked=true", nil)
		if w.Code != http.StatusOK {
			t.Fatalf("valid block list = %d, want 200: %s", w.Code, w.Body.String())
		}
		var page struct {
			Items      []store.BlockedKeyRow `json:"items"`
			NextCursor string               `json:"next_cursor"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if len(page.Items) != 1 {
			t.Fatalf("items = %d, want 1", len(page.Items))
		}
		item := page.Items[0]
		if item.SubscriptionID != subID {
			t.Fatalf("subscription_id = %q, want %q", item.SubscriptionID, subID)
		}
		if item.OrderingKey != "cust-1" {
			t.Fatalf("ordering_key = %q, want cust-1", item.OrderingKey)
		}
		if item.HeadDeliveryID == nil || *item.HeadDeliveryID != did1 {
			t.Fatalf("head_delivery_id = %v, want %s", item.HeadDeliveryID, did1)
		}
		if item.BlockedSince == nil {
			t.Fatal("blocked_since is nil, want non-nil")
		}
		if item.BacklogCount < 2 {
			t.Fatalf("backlog_count = %d, want >= 2", item.BacklogCount)
		}
		if item.OldestSuccessorAgeSec == nil {
			t.Fatal("oldest_successor_age_sec is nil")
		}
		if *item.OldestSuccessorAgeSec < 0 {
			t.Fatalf("oldest_successor_age_sec = %f, want >= 0", *item.OldestSuccessorAgeSec)
		}
	})

	// Sub-test D: skip did1 → key no longer in blocked list
	t.Run("skip clears block", func(t *testing.T) {
		w := do(t, srv, "POST", "/v1/deliveries/"+did1+"/skip", nil)
		if w.Code != http.StatusOK {
			t.Fatalf("skip = %d, want 200: %s", w.Code, w.Body.String())
		}
		w2 := do(t, srv, "GET", "/v1/ordered-keys?blocked=true", nil)
		if w2.Code != http.StatusOK {
			t.Fatalf("block list after skip = %d, want 200: %s", w2.Code, w2.Body.String())
		}
		var page struct {
			Items []store.BlockedKeyRow `json:"items"`
		}
		if err := json.Unmarshal(w2.Body.Bytes(), &page); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if len(page.Items) != 0 {
			t.Fatalf("items after skip = %d, want 0", len(page.Items))
		}
	})
}

func TestOrderedKeysBlockedListPagination(t *testing.T) {
	srv, st := newServer(t)
	ctx := context.Background()

	keyID := seedPipeline(t, st, "orders.*")

	var subID string
	if err := st.Pool.QueryRow(ctx, `SELECT id FROM subscriptions LIMIT 1`).Scan(&subID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Pool.Exec(ctx, `UPDATE subscriptions SET ordered=true WHERE id=$1`, subID); err != nil {
		t.Fatal(err)
	}

	// Create 5 blocked keys
	for i := 0; i < 5; i++ {
		key := fmt.Sprintf("cust-%d", i)
		res, err := st.IngestEvent(ctx, store.IngestParams{
			ProducerKeyID:       keyID,
			Topic:               "orders.x",
			Payload:             []byte(fmt.Sprintf(`{"i":%d}`, i)),
			OrderingKey:         key,
			OrderedKeyBacklogMax: 10000,
		})
		if err != nil {
			t.Fatalf("ingest key %s: %v", key, err)
		}
		did := res.DeliveryIDs[0]
		if _, err := st.Pool.Exec(ctx, `UPDATE deliveries SET state='dead_lettered', lease_until=NULL WHERE id=$1`, did); err != nil {
			t.Fatal(err)
		}
		// RecomputeCursor to set head_delivery_id + blocked_reason
		tx, err := st.Pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(ctx,
			`SELECT true FROM ordered_key_state WHERE subscription_id=$1 AND ordering_key=$2 FOR UPDATE`,
			subID, key); err != nil {
			_ = tx.Rollback(ctx)
			t.Fatal(err)
		}
		if err := st.RecomputeCursor(ctx, tx, subID, key); err != nil {
			_ = tx.Rollback(ctx)
			t.Fatal(err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
	}

	// Page 1: limit=3 → expect 3 items + next_cursor
	w1 := do(t, srv, "GET", "/v1/ordered-keys?blocked=true&limit=3", nil)
	if w1.Code != http.StatusOK {
		t.Fatalf("page1 = %d, want 200: %s", w1.Code, w1.Body.String())
	}
	var page1 struct {
		Items      []store.BlockedKeyRow `json:"items"`
		NextCursor string               `json:"next_cursor"`
	}
	if err := json.Unmarshal(w1.Body.Bytes(), &page1); err != nil {
		t.Fatalf("unmarshal page1: %v", err)
	}
	if len(page1.Items) != 3 {
		t.Fatalf("page1 items = %d, want 3", len(page1.Items))
	}
	if page1.NextCursor == "" {
		t.Fatal("page1 next_cursor is empty, want non-empty")
	}
	// Verify ascending order
	for i := 1; i < len(page1.Items); i++ {
		a := page1.Items[i-1]
		b := page1.Items[i]
		if a.SubscriptionID > b.SubscriptionID || (a.SubscriptionID == b.SubscriptionID && a.OrderingKey >= b.OrderingKey) {
			t.Fatalf("page1 not ordered: (%s,%s) >= (%s,%s)", a.SubscriptionID, a.OrderingKey, b.SubscriptionID, b.OrderingKey)
		}
	}

	// Page 2: use cursor → expect 2 items, next_cursor empty
	w2 := do(t, srv, "GET", "/v1/ordered-keys?blocked=true&limit=3&cursor="+page1.NextCursor, nil)
	if w2.Code != http.StatusOK {
		t.Fatalf("page2 = %d, want 200: %s", w2.Code, w2.Body.String())
	}
	var page2 struct {
		Items      []store.BlockedKeyRow `json:"items"`
		NextCursor string               `json:"next_cursor"`
	}
	if err := json.Unmarshal(w2.Body.Bytes(), &page2); err != nil {
		t.Fatalf("unmarshal page2: %v", err)
	}
	if len(page2.Items) != 2 {
		t.Fatalf("page2 items = %d, want 2", len(page2.Items))
	}
	if page2.NextCursor != "" {
		t.Fatalf("page2 next_cursor = %q, want empty", page2.NextCursor)
	}
	// No overlap between pages
	seen := map[string]bool{}
	for _, item := range page1.Items {
		seen[item.SubscriptionID+"\x00"+item.OrderingKey] = true
	}
	for _, item := range page2.Items {
		k := item.SubscriptionID + "\x00" + item.OrderingKey
		if seen[k] {
			t.Fatalf("duplicate key %s in page2", k)
		}
		seen[k] = true
	}
	if len(seen) != 5 {
		t.Fatalf("total unique keys = %d, want 5", len(seen))
	}
}

func TestOrderedKeysBlockedListBadCursor(t *testing.T) {
	srv, _ := newServer(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/v1/ordered-keys?blocked=true&cursor=!!!not-base64!!!", nil)
	r.Header.Set("Authorization", "Bearer "+testToken)
	srv.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("bad cursor = %d, want 400: %s", w.Code, w.Body.String())
	}
}
