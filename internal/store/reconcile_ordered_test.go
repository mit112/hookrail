//go:build integration

package store_test

import (
	"context"
	"testing"

	"github.com/mit112/hookrail/internal/store"
)

// recordingPublish records republished delivery ids in order (a stand-in for
// the Redis XADD the scheduler does post-reconcile).
type recordingPublish struct{ ids []string }

func (p *recordingPublish) fn(_ context.Context, id string) error {
	p.ids = append(p.ids, id)
	return nil
}

func setupOrderedKey(t *testing.T, st *store.Store, key string, n int) (subID string, dids []string) {
	t.Helper()
	ctx := context.Background()
	keyID, _, err := st.CreateProducerKey(ctx, "reconcile-test", []string{"*"})
	if err != nil {
		t.Fatal(err)
	}
	epID, _, err := st.CreateEndpoint(ctx, masterKey(), "https://consumer.example/h", "t")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateSubscriptionFull(ctx, store.SubInput{
		TopicPattern: "orders.*", EndpointID: epID, MaxAttempts: 3, Ordered: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.Pool.QueryRow(ctx, `SELECT id FROM subscriptions LIMIT 1`).Scan(&subID); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < n; i++ {
		res, err := st.IngestEvent(ctx, store.IngestParams{
			ProducerKeyID: keyID, Topic: "orders.created", Payload: []byte(`{}`),
			OrderingKey: key, OrderedKeyBacklogMax: 10000,
		})
		if err != nil || len(res.DeliveryIDs) != 1 {
			t.Fatalf("ingest %d: %v", i, err)
		}
		dids = append(dids, res.DeliveryIDs[0])
	}
	return subID, dids
}

// TestReconcileRepublishesAndAdvances proves the reconcile pass (a) republishes
// the deliverable head that Redis may have lost, (b) self-heals the cursor
// projection so a succeeded head advances to its successor, and (c) republishes
// in strict per-key order — never a reorder, never a stranded key (Task 19).
func TestReconcileRepublishesAndAdvances(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()

	_, dids := setupOrderedKey(t, st, "k1", 2) // seq1=dids[0], seq2=dids[1]

	// Pass 1: simulate Redis loss — nothing has been delivered yet. Reconcile
	// must republish the head (seq1) and report backlog 2, 0 blocked.
	pub := &recordingPublish{}
	blocked, backlog, err := st.ReconcileOrderedKeys(ctx, 100, pub.fn)
	if err != nil {
		t.Fatal(err)
	}
	if blocked != 0 || backlog != 2 {
		t.Fatalf("pass1 stats = (blocked %d, backlog %d), want (0, 2)", blocked, backlog)
	}
	if len(pub.ids) != 1 || pub.ids[0] != dids[0] {
		t.Fatalf("pass1 republished %v, want [%s] (head only)", pub.ids, dids[0])
	}

	// Drive seq1 → succeeded; the cursor must advance to seq2 on reconcile.
	if _, err := st.Pool.Exec(ctx, `UPDATE deliveries SET state='succeeded' WHERE id=$1`, dids[0]); err != nil {
		t.Fatal(err)
	}

	// Pass 2: reconcile self-heals the projection (cursor → seq2), republishes
	// seq2, reports backlog 1. The republish order across both passes is
	// 1 then 2 — strictly monotonic per key (no reorder).
	pub2 := &recordingPublish{}
	blocked, backlog, err = st.ReconcileOrderedKeys(ctx, 100, pub2.fn)
	if err != nil {
		t.Fatal(err)
	}
	if blocked != 0 || backlog != 1 {
		t.Fatalf("pass2 stats = (blocked %d, backlog %d), want (0, 1)", blocked, backlog)
	}
	if len(pub2.ids) != 1 || pub2.ids[0] != dids[1] {
		t.Fatalf("pass2 republished %v, want [%s] (advanced head)", pub2.ids, dids[1])
	}

	// Drive seq2 → succeeded; the key drains. Reconcile republishes nothing and
	// reports zero backlog — no stranded key.
	if _, err := st.Pool.Exec(ctx, `UPDATE deliveries SET state='succeeded' WHERE id=$1`, dids[1]); err != nil {
		t.Fatal(err)
	}
	pub3 := &recordingPublish{}
	blocked, backlog, err = st.ReconcileOrderedKeys(ctx, 100, pub3.fn)
	if err != nil {
		t.Fatal(err)
	}
	if blocked != 0 || backlog != 0 || len(pub3.ids) != 0 {
		t.Fatalf("drained pass = (blocked %d, backlog %d, republished %v), want (0,0,[])", blocked, backlog, pub3.ids)
	}
}

// TestReconcileBlockedKeyNotRepublished proves a key blocked on a dead_lettered
// head is counted as blocked, its head is NOT republished (it must not be
// re-delivered while blocked), and ReconcileKey returns nil for it (Task 19).
func TestReconcileBlockedKeyNotRepublished(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()

	subID, dids := setupOrderedKey(t, st, "k1", 2)

	// Dead-letter the head (seq1).
	if _, err := st.Pool.Exec(ctx, `UPDATE deliveries SET state='dead_lettered', lease_until=NULL WHERE id=$1`, dids[0]); err != nil {
		t.Fatal(err)
	}

	pub := &recordingPublish{}
	blocked, backlog, err := st.ReconcileOrderedKeys(ctx, 100, pub.fn)
	if err != nil {
		t.Fatal(err)
	}
	if blocked != 1 {
		t.Fatalf("blocked = %d, want 1", blocked)
	}
	if backlog != 2 {
		t.Fatalf("backlog = %d, want 2 (dead_lettered head still counts)", backlog)
	}
	if len(pub.ids) != 0 {
		t.Fatalf("blocked key republished %v, want nothing (must not re-deliver while blocked)", pub.ids)
	}

	// ReconcileKey returns nil for the blocked key.
	head, err := st.ReconcileKey(ctx, subID, "k1")
	if err != nil {
		t.Fatal(err)
	}
	if head != nil {
		t.Fatalf("ReconcileKey on blocked key = %v, want nil", head)
	}
}
