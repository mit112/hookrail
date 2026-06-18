//go:build integration

package store_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/mit112/hookrail/internal/store"
)

// seedDead drives one delivery to dead_lettered and inserts its DLQ row.
func seedDead(t *testing.T, s *store.Store, pattern string) string {
	t.Helper()
	ctx := context.Background()
	keyID := seedPipeline(t, s, pattern)
	res, _ := s.IngestEvent(ctx, store.IngestParams{ProducerKeyID: keyID, Topic: pattern[:len(pattern)-1] + "x", Payload: []byte(`{}`)})
	id := res.DeliveryIDs[0]
	_, _ = s.Pool.Exec(ctx, `UPDATE deliveries SET state='dead_lettered', lease_until=NULL WHERE id=$1`, id)
	_, _ = s.Pool.Exec(ctx, `INSERT INTO dead_letters (delivery_id, final_error, endpoint_id) SELECT id,'x',endpoint_id FROM deliveries WHERE id=$1`, id)
	return id
}

func TestReplayConcurrentExactlyOneWinner(t *testing.T) {
	s := testStore(t)
	id := seedDead(t, s, "rp1.*")
	const n = 8
	results := make([]store.ReplayOutcome, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			o, err := s.ReplayDeadLetter(context.Background(), id, time.Hour)
			if err != nil {
				t.Errorf("replay %d: %v", i, err)
			}
			results[i] = o
		}(i)
	}
	wg.Wait()
	ok := 0
	for _, o := range results {
		switch o {
		case store.ReplayOK:
			ok++
		case store.ReplayConflict: // every loser must be a clean 409, not 404/410
		default:
			t.Fatalf("loser outcome = %v, want ReplayConflict", o)
		}
	}
	if ok != 1 {
		t.Fatalf("%d winners, want exactly 1 (rest 409)", ok)
	}
	// the winner re-armed the delivery to pending; claim_version is UNCHANGED
	// by replay (design §4.1 step 4 — the fence keeps counting across replay)
	var state string
	var cv int64
	_ = s.Pool.QueryRow(context.Background(),
		`SELECT state::text, claim_version FROM deliveries WHERE id=$1`, id).Scan(&state, &cv)
	if state != "pending" {
		t.Fatalf("post-replay state = %q, want pending", state)
	}
	if cv != 0 {
		t.Fatalf("claim_version = %d after replay; replay must NOT reset/bump it (was 0 pre-claim)", cv)
	}
}

// TestReplayStepTwoRollback: the delivery is no longer dead_lettered when
// step-2 runs (it moved to in_flight). Step-1's CAS may claim the replay, but
// step-2's `WHERE state='dead_lettered'` affects 0 rows → the WHOLE tx rolls
// back → ReplayConflict, and dead_letters.replayed_at must NOT be left stamped.
// (ClaimDelivery cannot select a dead_lettered row — claim.go's predicate only
// accepts due pending/retry or expired in_flight — so we set in_flight directly
// to create the exact race the rollback guard exists for.)
func TestReplayStepTwoRollback(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	id := seedDead(t, s, "rvc.*")
	_, _ = s.Pool.Exec(ctx, `UPDATE deliveries SET state='in_flight', lease_until=now()+interval '30s' WHERE id=$1`, id)
	out, err := s.ReplayDeadLetter(ctx, id, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if out != store.ReplayConflict {
		t.Fatalf("step-2 0-rows = %v, want ReplayConflict (rollback)", out)
	}
	var replayedAt *time.Time
	_ = s.Pool.QueryRow(ctx, `SELECT replayed_at FROM dead_letters WHERE delivery_id=$1`, id).Scan(&replayedAt)
	if replayedAt != nil {
		t.Fatal("rolled-back replay must NOT leave replayed_at stamped (atomicity)")
	}
}

// TestReplayThenClaimBumpsFence proves the replay↔claim interplay: a successful
// replay re-arms to pending WITHOUT touching claim_version (design §4.1 step 4),
// and a subsequent worker claim then bumps the fence monotonically — so a stale
// pre-replay completion can never overwrite the re-armed delivery.
func TestReplayThenClaimBumpsFence(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	id := seedDead(t, s, "rtc.*")
	var cvBefore int64
	_ = s.Pool.QueryRow(ctx, `SELECT claim_version FROM deliveries WHERE id=$1`, id).Scan(&cvBefore)

	if out, err := s.ReplayDeadLetter(ctx, id, time.Hour); err != nil || out != store.ReplayOK {
		t.Fatalf("replay = %v %v, want ReplayOK", out, err)
	}
	var cvAfterReplay int64
	_ = s.Pool.QueryRow(ctx, `SELECT claim_version FROM deliveries WHERE id=$1`, id).Scan(&cvAfterReplay)
	if cvAfterReplay != cvBefore {
		t.Fatalf("claim_version changed by replay: %d -> %d (must be untouched)", cvBefore, cvAfterReplay)
	}
	ok, d, err := s.ClaimDelivery(ctx, id, 30*time.Second)
	if err != nil || !ok {
		t.Fatalf("claim after replay: ok=%v err=%v", ok, err)
	}
	if d.ClaimVersion <= cvBefore {
		t.Fatalf("claim_version = %d after claim, want > %d (fence keeps counting across replay)", d.ClaimVersion, cvBefore)
	}
}

func TestReplayClassification(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	// unknown id → NotFound
	if o, _ := s.ReplayDeadLetter(ctx, "does-not-exist", time.Hour); o != store.ReplayNotFound {
		t.Fatalf("unknown = %v, want NotFound", o)
	}
	// live (not dead) delivery → Conflict
	live := mkDelivery(t, s)
	if o, _ := s.ReplayDeadLetter(ctx, live, time.Hour); o != store.ReplayConflict {
		t.Fatalf("live = %v, want Conflict", o)
	}
	// dead but past expiry → Gone
	old := seedDead(t, s, "rp2.*")
	_, _ = s.Pool.Exec(ctx, `UPDATE dead_letters SET dead_at = now() - interval '48 hours' WHERE delivery_id=$1`, old)
	if o, _ := s.ReplayDeadLetter(ctx, old, time.Hour); o != store.ReplayGone {
		t.Fatalf("expired = %v, want Gone", o)
	}
	// deleted target → Conflict
	delTgt := seedDead(t, s, "rp3.*")
	_, _ = s.Pool.Exec(ctx, `UPDATE subscriptions SET deleted_at=now()`)
	if o, _ := s.ReplayDeadLetter(ctx, delTgt, time.Hour); o != store.ReplayConflict {
		t.Fatalf("deleted-target = %v, want Conflict", o)
	}
}

// TestReplayAfterTombstoneSerializesCorrectly: the tombstone guard in
// ReplayDeadLetter takes FOR UPDATE on the events row that TombstoneEventPayloads
// also locks. Run sequentially: tombstone empties the payload first; a subsequent
// replay whose CAS would otherwise succeed sees payload_size==0 and returns
// ReplayGone — the delivery stays dead_lettered.
func TestReplayAfterTombstoneSerializesCorrectly(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	// Seed an event + dead-lettered delivery (seedDead does: pending → dead_lettered + DL insert).
	id := seedDead(t, s, "tsr.*")

	// Age the event created_at so tombstone considers it (age=30d).
	_, _ = s.Pool.Exec(ctx, `UPDATE events SET created_at = now() - interval '31 days' WHERE id = (SELECT event_id FROM deliveries WHERE id=$1)`, id)
	// dead_at = 35d ago: EXPIRED for tombstone (age=30d), but within replay window (replayAge=40d).
	_, _ = s.Pool.Exec(ctx, `UPDATE dead_letters SET dead_at = now() - interval '35 days' WHERE delivery_id=$1`, id)

	// 1. Tombstone first — empties the payload (dead_at is expired from its 30d perspective).
	n, err := s.TombstoneEventPayloads(ctx, 30*24*time.Hour, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Fatal("tombstone must empty at least one payload")
	}

	// Verify payload_size is 0.
	var psize int
	_ = s.Pool.QueryRow(ctx,
		`SELECT e.payload_size FROM events e JOIN deliveries d ON d.event_id = e.id WHERE d.id=$1`, id).Scan(&psize)
	if psize != 0 {
		t.Fatalf("payload_size = %d, want 0 after tombstone", psize)
	}

	// 2. Replay with larger replayAge (40d) — CAS wins (dead_at >= now()-40d), but guard sees payload_size==0.
	out, err := s.ReplayDeadLetter(ctx, id, 40*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if out != store.ReplayGone {
		t.Fatalf("replay after tombstone = %v, want ReplayGone", out)
	}

	// Delivery must NOT be re-armed to pending.
	var state string
	_ = s.Pool.QueryRow(ctx, `SELECT state::text FROM deliveries WHERE id=$1`, id).Scan(&state)
	if state != "dead_lettered" {
		t.Fatalf("state = %q, want dead_lettered (not re-armed)", state)
	}

	// replayed_at must be rolled back to NULL.
	var replayedAt *time.Time
	_ = s.Pool.QueryRow(ctx, `SELECT replayed_at FROM dead_letters WHERE delivery_id=$1`, id).Scan(&replayedAt)
	if replayedAt != nil {
		t.Fatal("replayed_at must be NULL (tx rolled back)")
	}
}

// TestReplayOKWhenPayloadIntact: a non-tombstoned event replays to ReplayOK
// and re-arms to pending (negative control for the tombstone guard).
func TestReplayOKWhenPayloadIntact(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	id := seedDead(t, s, "rpi.*")

	// Payload is intact (not tombstoned) — replay must succeed.
	out, err := s.ReplayDeadLetter(ctx, id, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if out != store.ReplayOK {
		t.Fatalf("replay = %v, want ReplayOK", out)
	}

	var state string
	_ = s.Pool.QueryRow(ctx, `SELECT state::text FROM deliveries WHERE id=$1`, id).Scan(&state)
	if state != "pending" {
		t.Fatalf("state = %q, want pending (re-armed)", state)
	}
}
