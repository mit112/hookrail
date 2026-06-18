//go:build integration

package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/mit112/hookrail/internal/store"
)

func TestTombstoneSkipsReplayableDeadLetter(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	keyID := seedPipeline(t, s, "tomb.*")
	res, _ := s.IngestEvent(ctx, store.IngestParams{ProducerKeyID: keyID, Topic: "tomb.x", Payload: []byte(`{"big":"payload"}`)})
	eid := func() string {
		var e string
		_ = s.Pool.QueryRow(ctx, `SELECT event_id FROM deliveries WHERE id=$1`, res.DeliveryIDs[0]).Scan(&e)
		return e
	}()
	did := res.DeliveryIDs[0]
	// old event, but recently re-dead-lettered and NOT yet replayed (the re-dead-letter hole)
	_, _ = s.Pool.Exec(ctx, `UPDATE events SET created_at = now() - interval '60 days' WHERE id=$1`, eid)
	_, _ = s.Pool.Exec(ctx, `UPDATE deliveries SET state='dead_lettered' WHERE id=$1`, did)
	_, _ = s.Pool.Exec(ctx, `INSERT INTO dead_letters (delivery_id, final_error, endpoint_id, dead_at, replayed_at)
	                         SELECT id,'x',endpoint_id, now(), NULL FROM deliveries WHERE id=$1`, did)

	n, err := s.TombstoneEventPayloads(ctx, 30*24*time.Hour, 1000)
	if err != nil {
		t.Fatal(err)
	}
	var size int
	_ = s.Pool.QueryRow(ctx, `SELECT payload_size FROM events WHERE id=$1`, eid).Scan(&size)
	if size == 0 {
		t.Fatalf("tombstoned a replayable dead-letter's payload (n=%d) — F1 violation", n)
	}
}

func TestTombstoneEmptiesOldSettledPayload(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	keyID := seedPipeline(t, s, "set.*")
	res, _ := s.IngestEvent(ctx, store.IngestParams{ProducerKeyID: keyID, Topic: "set.x", Payload: []byte(`{"big":"payload"}`)})
	did := res.DeliveryIDs[0]
	var eid string
	_ = s.Pool.QueryRow(ctx, `SELECT event_id FROM deliveries WHERE id=$1`, did).Scan(&eid)
	_, _ = s.Pool.Exec(ctx, `UPDATE events SET created_at = now() - interval '60 days' WHERE id=$1`, eid)
	_, _ = s.Pool.Exec(ctx, `UPDATE deliveries SET state='succeeded' WHERE id=$1`, did)

	if _, err := s.TombstoneEventPayloads(ctx, 30*24*time.Hour, 1000); err != nil {
		t.Fatal(err)
	}
	var size int
	_ = s.Pool.QueryRow(ctx, `SELECT payload_size FROM events WHERE id=$1`, eid).Scan(&size)
	if size != 0 {
		t.Fatalf("old settled payload not tombstoned: size=%d", size)
	}
}

func TestTrimAttemptsSetsDurableMarker(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	id := mkDelivery(t, s)
	// one old attempt row
	_, _ = s.Pool.Exec(ctx,
		`INSERT INTO delivery_attempts (delivery_id, attempt_no, claim_version, status, requested_at, completed_at)
		 VALUES ($1, 1, 1, 'retryable_failure', now()-interval '60 days', now()-interval '60 days')`, id)
	n, err := s.TrimDeliveryAttempts(ctx, 30*24*time.Hour, 1000)
	if err != nil || n == 0 {
		t.Fatalf("trim = %d, %v", n, err)
	}
	var marked *time.Time
	_ = s.Pool.QueryRow(ctx, `SELECT attempts_truncated_at FROM deliveries WHERE id=$1`, id).Scan(&marked)
	if marked == nil {
		t.Fatal("attempts_truncated_at not set in the trim tx (F12)")
	}
}
