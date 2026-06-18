//go:build integration

package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/mit112/hookrail/internal/backoff"
	"github.com/mit112/hookrail/internal/store"
)

func TestClaimReturnsBackoffPolicy(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	keyID, _, _ := s.CreateProducerKey(ctx, "bp")
	epID, _, _ := s.CreateEndpoint(ctx, [32]byte{}, "https://example.com/h", "")
	_, _ = s.CreateSubscriptionFull(ctx, store.SubInput{
		TopicPattern: "bp.*", EndpointID: epID, MaxAttempts: 8,
		BackoffPolicy: []byte(`{"base_ms":1234,"cap_ms":99999}`),
	})
	res, _ := s.IngestEvent(ctx, store.IngestParams{ProducerKeyID: keyID, Topic: "bp.x", Payload: []byte(`{}`)})
	ok, d, err := s.ClaimDelivery(ctx, res.DeliveryIDs[0], 30*time.Second)
	if err != nil || !ok {
		t.Fatalf("claim: %v %v", ok, err)
	}
	pol := backoff.FromJSON(d.BackoffPolicy, d.MaxAttempts)
	if pol.Base != 1234*time.Millisecond {
		t.Fatalf("per-sub base = %v, want 1.234s", pol.Base)
	}
}
