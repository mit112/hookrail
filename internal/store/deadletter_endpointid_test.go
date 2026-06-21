//go:build integration

package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/mit112/hookrail/internal/backoff"
	"github.com/mit112/hookrail/internal/domain"
	"github.com/mit112/hookrail/internal/store"
)

func TestDeadLetterCarriesEndpointID(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	id := mkDelivery(t, s)
	ok, d, err := s.ClaimDelivery(ctx, id, 30*time.Second)
	if err != nil || !ok {
		t.Fatalf("claim: %v %v", ok, err)
	}
	// permanent failure → dead-lettered
	_, err = s.CompleteAttempt(ctx, store.AttemptResult{
		DeliveryID: d.ID, AttemptNo: d.AttemptCount, ClaimVersion: d.ClaimVersion,
		Outcome: domain.OutcomePermanent, ErrorClass: "permanent", RequestedAt: time.Now(), CompletedAt: time.Now(),
	}, backoff.Default(), d.MaxAttempts)
	if err != nil {
		t.Fatal(err)
	}
	var epID *string
	_ = s.Pool.QueryRow(ctx, `SELECT endpoint_id FROM dead_letters WHERE delivery_id=$1`, id).Scan(&epID)
	if epID == nil || *epID == "" {
		t.Fatal("dead_letters.endpoint_id not populated by worker dead-letter")
	}
}
