//go:build integration

package store_test

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/mit112/hookrail/internal/store"
)

func TestAssignOrderingSeqStrictUnderConcurrency(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	const N = 50
	seqs := make(chan int64, N)
	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tx, err := st.Pool.Begin(ctx)
			if err != nil {
				t.Errorf("begin tx: %v", err)
				return
			}
			seq, _, err := st.AssignOrderingSeq(ctx, tx, "sub1", "k1")
			if err != nil {
				t.Errorf("AssignOrderingSeq: %v", err)
				if rerr := tx.Rollback(ctx); rerr != nil {
					t.Errorf("rollback on error: %v", rerr)
				}
				return
			}
			if err := tx.Commit(ctx); err != nil {
				t.Errorf("commit tx: %v", err)
				return
			}
			seqs <- seq
		}()
	}
	wg.Wait()
	close(seqs)

	got := map[int64]bool{}
	for s := range seqs {
		got[s] = true
	}
	for i := int64(1); i <= N; i++ {
		if !got[i] {
			t.Fatalf("missing seq %d (gap/dupe)", i)
		}
	}
}

func TestRecomputeCursor(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()

	keyID, subID := seedOrderedPipeline(t, st, "orders.*")

	// Look up the subscription's endpoint_id
	var endpointID string
	if err := st.Pool.QueryRow(ctx, `SELECT endpoint_id FROM subscriptions WHERE id=$1`, subID).Scan(&endpointID); err != nil {
		t.Fatal(err)
	}

	insertDelivery := func(tx pgx.Tx, oseq int64, state, payload, okey string) string {
		t.Helper()
		eid := store.NewID()
		_, err := tx.Exec(ctx, `INSERT INTO events (id, producer_key_id, topic, payload, payload_size)
			VALUES ($1, $2, 'orders.created', $3, $4)`, eid, keyID, []byte(payload), len(payload))
		if err != nil {
			t.Fatalf("insert event: %v", err)
		}
		did := store.NewID()
		_, err = tx.Exec(ctx,
			`INSERT INTO deliveries (id, event_id, subscription_id, ordering_key, ordering_seq, state, next_attempt_at, endpoint_id)
			 VALUES ($1, $2, $3, $4, $5, $6::delivery_state, now(), $7)`,
			did, eid, subID, okey, oseq, state, endpointID)
		if err != nil {
			t.Fatalf("insert delivery: %v", err)
		}
		return did
	}

	// Seed ordered_key_state for key "k1"
	_, err := st.Pool.Exec(ctx,
		`INSERT INTO ordered_key_state (subscription_id, ordering_key, seq_counter, cursor_seq, backlog_count)
		 VALUES ($1, $2, 0, 1, 0)
		 ON CONFLICT (subscription_id, ordering_key) DO NOTHING`, subID, "k1")
	if err != nil {
		t.Fatal(err)
	}

	// --- Case (a): one active seq=1 → cursor=1, head set, not blocked ---
	t.Run("active-head", func(t *testing.T) {
		tx, err := st.Pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx) //nolint:errcheck
		did1 := insertDelivery(tx, 1, "pending", `{"n":1}`, "k1")
		_, err = tx.Exec(ctx,
			`UPDATE ordered_key_state SET seq_counter=1 WHERE subscription_id=$1 AND ordering_key=$2`,
			subID, "k1")
		if err != nil {
			t.Fatal(err)
		}
		if err := st.RecomputeCursor(ctx, tx, subID, "k1"); err != nil {
			t.Fatal(err)
		}
		var cursorSeq int64
		var headID, blockedReason *string
		var blockedSince interface{}
		var backlog int
		err = tx.QueryRow(ctx,
			`SELECT cursor_seq, head_delivery_id, blocked_reason, blocked_since, backlog_count
			 FROM ordered_key_state WHERE subscription_id=$1 AND ordering_key=$2`,
			subID, "k1").Scan(&cursorSeq, &headID, &blockedReason, &blockedSince, &backlog)
		if err != nil {
			t.Fatal(err)
		}
		if cursorSeq != 1 {
			t.Errorf("cursor_seq = %d, want 1", cursorSeq)
		}
		if headID == nil || *headID != did1 {
			t.Errorf("head_delivery_id = %v, want %s", headID, did1)
		}
		if blockedReason != nil {
			t.Errorf("blocked_reason = %v, want nil", blockedReason)
		}
		if backlog != 1 {
			t.Errorf("backlog_count = %d, want 1", backlog)
		}
	})

	// --- Case (b): seq=1 succeeded only → cursor=2 (sentinel seq_counter+1), head NULL, backlog 0 ---
	t.Run("drain-to-sentinel", func(t *testing.T) {
		tx, err := st.Pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx) //nolint:errcheck
		insertDelivery(tx, 1, "succeeded", `{"n":1}`, "k1b")
		_, err = tx.Exec(ctx,
			`UPDATE ordered_key_state SET seq_counter=1 WHERE subscription_id=$1 AND ordering_key=$2`,
			subID, "k1b")
		if err != nil {
			t.Fatal(err)
		}
		// Ensure ordered_key_state row exists for k1b
		_, err = tx.Exec(ctx,
			`INSERT INTO ordered_key_state (subscription_id, ordering_key, seq_counter, cursor_seq, backlog_count)
			 VALUES ($1, $2, 1, 1, 0)
			 ON CONFLICT (subscription_id, ordering_key) DO NOTHING`, subID, "k1b")
		if err != nil {
			t.Fatal(err)
		}
		if err := st.RecomputeCursor(ctx, tx, subID, "k1b"); err != nil {
			t.Fatal(err)
		}
		var cursorSeq int64
		var headID, blockedReason *string
		var backlog int
		err = tx.QueryRow(ctx,
			`SELECT cursor_seq, head_delivery_id, blocked_reason, backlog_count
			 FROM ordered_key_state WHERE subscription_id=$1 AND ordering_key=$2`,
			subID, "k1b").Scan(&cursorSeq, &headID, &blockedReason, &backlog)
		if err != nil {
			t.Fatal(err)
		}
		if cursorSeq != 2 { // seq_counter(1) + 1 = 2
			t.Errorf("cursor_seq = %d, want 2 (sentinel)", cursorSeq)
		}
		if headID != nil {
			t.Errorf("head_delivery_id = %v, want nil", headID)
		}
		if blockedReason != nil {
			t.Errorf("blocked_reason = %v, want nil", blockedReason)
		}
		if backlog != 0 {
			t.Errorf("backlog_count = %d, want 0", backlog)
		}
	})

	// --- Case (c): seq=1 dead_lettered, seq=2 pending → cursor=1, blocked_reason='dead_lettered' ---
	t.Run("dead-lettered-head-blocks", func(t *testing.T) {
		tx, err := st.Pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx) //nolint:errcheck
		insertDelivery(tx, 1, "dead_lettered", `{"n":1}`, "k1c")
		insertDelivery(tx, 2, "pending", `{"n":2}`, "k1c")
		_, err = tx.Exec(ctx,
			`UPDATE ordered_key_state SET seq_counter=2 WHERE subscription_id=$1 AND ordering_key=$2`,
			subID, "k1c")
		if err != nil {
			t.Fatal(err)
		}
		_, err = tx.Exec(ctx,
			`INSERT INTO ordered_key_state (subscription_id, ordering_key, seq_counter, cursor_seq, backlog_count)
			 VALUES ($1, $2, 2, 1, 0)
			 ON CONFLICT (subscription_id, ordering_key) DO NOTHING`, subID, "k1c")
		if err != nil {
			t.Fatal(err)
		}
		if err := st.RecomputeCursor(ctx, tx, subID, "k1c"); err != nil {
			t.Fatal(err)
		}
		var cursorSeq int64
		var headID, blockedReason *string
		var blockedSince interface{}
		var backlog int
		err = tx.QueryRow(ctx,
			`SELECT cursor_seq, head_delivery_id, blocked_reason, blocked_since, backlog_count
			 FROM ordered_key_state WHERE subscription_id=$1 AND ordering_key=$2`,
			subID, "k1c").Scan(&cursorSeq, &headID, &blockedReason, &blockedSince, &backlog)
		if err != nil {
			t.Fatal(err)
		}
		if cursorSeq != 1 {
			t.Errorf("cursor_seq = %d, want 1", cursorSeq)
		}
		if headID == nil {
			t.Error("head_delivery_id is nil, want non-nil")
		}
		if blockedReason == nil || *blockedReason != "dead_lettered" {
			t.Errorf("blocked_reason = %v, want 'dead_lettered'", blockedReason)
		}
		if blockedSince == nil {
			t.Error("blocked_since is nil, want non-nil")
		}
		if backlog != 2 {
			t.Errorf("backlog_count = %d, want 2", backlog)
		}
	})

	// --- Case (d): seq=1 skipped, seq=2 pending → cursor=2, not blocked ---
	t.Run("skipped-head-advances", func(t *testing.T) {
		tx, err := st.Pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx) //nolint:errcheck
		did2 := insertDelivery(tx, 2, "pending", `{"n":2}`, "k1d")
		insertDelivery(tx, 1, "skipped", `{"n":1}`, "k1d")
		_, err = tx.Exec(ctx,
			`UPDATE ordered_key_state SET seq_counter=2 WHERE subscription_id=$1 AND ordering_key=$2`,
			subID, "k1d")
		if err != nil {
			t.Fatal(err)
		}
		_, err = tx.Exec(ctx,
			`INSERT INTO ordered_key_state (subscription_id, ordering_key, seq_counter, cursor_seq, backlog_count)
			 VALUES ($1, $2, 2, 1, 0)
			 ON CONFLICT (subscription_id, ordering_key) DO NOTHING`, subID, "k1d")
		if err != nil {
			t.Fatal(err)
		}
		if err := st.RecomputeCursor(ctx, tx, subID, "k1d"); err != nil {
			t.Fatal(err)
		}
		var cursorSeq int64
		var headID, blockedReason *string
		var backlog int
		err = tx.QueryRow(ctx,
			`SELECT cursor_seq, head_delivery_id, blocked_reason, backlog_count
			 FROM ordered_key_state WHERE subscription_id=$1 AND ordering_key=$2`,
			subID, "k1d").Scan(&cursorSeq, &headID, &blockedReason, &backlog)
		if err != nil {
			t.Fatal(err)
		}
		if cursorSeq != 2 {
			t.Errorf("cursor_seq = %d, want 2", cursorSeq)
		}
		if headID == nil || *headID != did2 {
			t.Errorf("head_delivery_id = %v, want %s", headID, did2)
		}
		if blockedReason != nil {
			t.Errorf("blocked_reason = %v, want nil", blockedReason)
		}
		if backlog != 1 {
			t.Errorf("backlog_count = %d, want 1", backlog)
		}
	})
}

func TestApplyOrderedTerminalConcurrent(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	keyID, subID := seedOrderedPipeline(t, st, "orders.*")

	var endpointID string
	if err := st.Pool.QueryRow(ctx,
		`SELECT endpoint_id FROM subscriptions WHERE id=$1`, subID).Scan(&endpointID); err != nil {
		t.Fatal(err)
	}

	// Seed ordered_key_state for key "k1": seq_counter=2, cursor=1, backlog=2
	_, err := st.Pool.Exec(ctx,
		`INSERT INTO ordered_key_state (subscription_id, ordering_key, seq_counter, cursor_seq, backlog_count)
		 VALUES ($1, $2, 2, 1, 2)
		 ON CONFLICT (subscription_id, ordering_key) DO NOTHING`,
		subID, "k1")
	if err != nil {
		t.Fatal(err)
	}

	// Create two deliveries: seq=1 (pending), seq=2 (pending)
	insertDelivery := func(seq int64, state string) string {
		t.Helper()
		eid := store.NewID()
		did := store.NewID()
		if _, err := st.Pool.Exec(ctx,
			`INSERT INTO events (id, producer_key_id, topic, payload, payload_size)
			 VALUES ($1, $2, 'orders.created', $3, $4)`, eid, keyID, []byte(`{}`), 2); err != nil {
			t.Fatal(err)
		}
		if _, err := st.Pool.Exec(ctx,
			`INSERT INTO deliveries (id, event_id, subscription_id, ordering_key, ordering_seq, state, next_attempt_at, endpoint_id)
			 VALUES ($1, $2, $3, $4, $5, $6::delivery_state, now(), $7)`,
			did, eid, subID, "k1", seq, state, endpointID); err != nil {
			t.Fatal(err)
		}
		return did
	}
	did1 := insertDelivery(1, "pending")
	did2 := insertDelivery(2, "pending")

	// Two goroutines: both transition seq=1 → succeeded, then call ApplyOrderedTerminal.
	// FOR UPDATE serializes them; cursor must never regress.
	var wg sync.WaitGroup
	results := make(chan *string, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tx, err := st.Pool.Begin(ctx)
			if err != nil {
				t.Errorf("begin tx: %v", err)
				return
			}
			// Transition seq=1 to succeeded (only first goroutine affects a row)
			if _, err := tx.Exec(ctx,
				`UPDATE deliveries SET state='succeeded', lease_until=NULL
				 WHERE id=$1 AND state='pending'`, did1); err != nil {
				t.Errorf("update seq1: %v", err)
				if rerr := tx.Rollback(ctx); rerr != nil {
					t.Errorf("rollback: %v", rerr)
				}
				return
			}
			head, err := st.ApplyOrderedTerminal(ctx, tx, subID, "k1")
			if err != nil {
				t.Errorf("ApplyOrderedTerminal: %v", err)
				if rerr := tx.Rollback(ctx); rerr != nil {
					t.Errorf("rollback: %v", rerr)
				}
				return
			}
			if err := tx.Commit(ctx); err != nil {
				t.Errorf("commit: %v", err)
				return
			}
			results <- head
		}()
	}
	wg.Wait()
	close(results)

	// Collect unique non-nil heads
	heads := map[string]bool{}
	for h := range results {
		if h != nil {
			heads[*h] = true
		}
	}

	// Assert final state: cursor=2, head=did2
	var cursor int64
	var headID *string
	if err := st.Pool.QueryRow(ctx,
		`SELECT cursor_seq, head_delivery_id FROM ordered_key_state
		 WHERE subscription_id=$1 AND ordering_key=$2`,
		subID, "k1").Scan(&cursor, &headID); err != nil {
		t.Fatal(err)
	}
	if cursor != 2 {
		t.Errorf("cursor_seq = %d, want 2 (must never regress)", cursor)
	}
	if headID == nil || *headID != did2 {
		t.Errorf("head_delivery_id = %v, want %s", headID, did2)
	}
	if len(heads) > 1 {
		t.Errorf("divergent heads returned: %v — FOR UPDATE serialization broken", heads)
	}
}

func TestBlockedKeys(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()

	// Set up: subscription with ordered=true
	keyID, _, err := st.CreateProducerKey(ctx, "test")
	if err != nil {
		t.Fatal(err)
	}
	epID, _, err := st.CreateEndpoint(ctx, [32]byte{}, "https://example.com/h", "seed")
	if err != nil {
		t.Fatal(err)
	}
	_, err = st.CreateSubscriptionFull(ctx, store.SubInput{
		TopicPattern: "orders.*", EndpointID: epID, MaxAttempts: 3, Ordered: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	var subID string
	if err := st.Pool.QueryRow(ctx, `SELECT id FROM subscriptions LIMIT 1`).Scan(&subID); err != nil {
		t.Fatal(err)
	}

	// Ingest 2 events for key "k1", 1 for key "k2"
	var did1 string
	for _, k := range []string{"k1", "k2"} {
		for i := 0; i < func() int { if k == "k1" { return 2 } else { return 1 } }(); i++ {
			res, err := st.IngestEvent(ctx, store.IngestParams{
				ProducerKeyID: keyID, Topic: "orders.x",
				Payload: []byte(fmt.Sprintf(`{"k":%q,"i":%d}`, k, i)),
				OrderingKey: k, OrderedKeyBacklogMax: 10000,
			})
			if err != nil {
				t.Fatal(err)
			}
			if k == "k1" && did1 == "" {
				did1 = res.DeliveryIDs[0]
			}
		}
	}

	// Block k1 only: set its head to dead_lettered + blocked_reason
	if _, err := st.Pool.Exec(ctx, `UPDATE deliveries SET state='dead_lettered', lease_until=NULL WHERE id=$1`, did1); err != nil {
		t.Fatal(err)
	}
	// RecomputeCursor will set blocked_reason='dead_lettered' and head_delivery_id
	tx, err := st.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err := tx.Exec(ctx,
		`SELECT true FROM ordered_key_state WHERE subscription_id=$1 AND ordering_key='k1' FOR UPDATE`,
		subID); err != nil {
		t.Fatal(err)
	}
	if err := st.RecomputeCursor(ctx, tx, subID, "k1"); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	// BlockedKeys should return only k1
	rows, err := st.BlockedKeys(ctx, store.BlockedKeyFilter{Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d blocked keys, want 1", len(rows))
	}
	r := rows[0]
	if r.SubscriptionID != subID {
		t.Fatalf("subscription_id = %q, want %q", r.SubscriptionID, subID)
	}
	if r.OrderingKey != "k1" {
		t.Fatalf("ordering_key = %q, want k1", r.OrderingKey)
	}
	if r.HeadDeliveryID == nil || *r.HeadDeliveryID != did1 {
		t.Fatalf("head_delivery_id = %v, want %s", r.HeadDeliveryID, did1)
	}
	if r.BlockedSince == nil {
		t.Fatal("blocked_since nil")
	}
	if r.BacklogCount < 1 {
		t.Fatalf("backlog_count = %d, want >= 1", r.BacklogCount)
	}
	if r.OldestSuccessorAgeSec == nil {
		t.Fatal("oldest_successor_age_sec nil")
	}
}
