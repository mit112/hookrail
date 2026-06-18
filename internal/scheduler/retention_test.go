//go:build integration

package scheduler_test

import (
	"context"
	"testing"
	"time"

	"github.com/mit112/hookrail/internal/scheduler"
	"github.com/mit112/hookrail/internal/store"
)

// uses the scheduler package's own testStore harness (mirror the store one).
func TestJanitorRunOnce(t *testing.T) {
	s := schedTestStore(t)
	ctx := context.Background()
	// seed one old settled event so tombstone has work
	keyID := schedSeed(t, s, "jan.*")
	res, _ := s.IngestEvent(ctx, store.IngestParams{ProducerKeyID: keyID, Topic: "jan.x", Payload: []byte(`{"x":"y"}`)})
	did := res.DeliveryIDs[0]
	var eid string
	_ = s.Pool.QueryRow(ctx, `SELECT event_id FROM deliveries WHERE id=$1`, did).Scan(&eid)
	_, _ = s.Pool.Exec(ctx, `UPDATE events SET created_at=now()-interval '60 days' WHERE id=$1`, eid)
	_, _ = s.Pool.Exec(ctx, `UPDATE deliveries SET state='succeeded' WHERE id=$1`, did)

	j := &scheduler.Janitor{
		Store: s, PayloadAge: 30 * 24 * time.Hour, AttemptAge: 30 * 24 * time.Hour,
		Batch: 1000, TickBudget: 30 * time.Second,
	}
	if err := j.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	var size int
	_ = s.Pool.QueryRow(ctx, `SELECT payload_size FROM events WHERE id=$1`, eid).Scan(&size)
	if size != 0 {
		t.Fatalf("janitor did not tombstone old payload: size=%d", size)
	}
}
