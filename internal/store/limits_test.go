//go:build integration

package store_test

import (
	"context"
	"testing"

	"github.com/mit112/hookrail/internal/store"
)

func TestEndpointRateLimits(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	epID, _, _ := s.CreateEndpoint(ctx, [32]byte{}, "https://example.com/h", "")
	r1, r2 := 10.0, 4.0
	_, _ = s.CreateSubscriptionFull(ctx, store.SubInput{TopicPattern: "a.*", EndpointID: epID, MaxAttempts: 3, RateLimitRPS: &r1})
	subLow, _ := s.CreateSubscriptionFull(ctx, store.SubInput{TopicPattern: "b.*", EndpointID: epID, MaxAttempts: 3, RateLimitRPS: &r2})

	m, err := s.EndpointRateLimits(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if m[epID] != 4.0 {
		t.Fatalf("MIN rps = %v, want 4 (min of 10,4)", m[epID])
	}
	// soft-delete the lower sub → MIN should rise to 10
	if err := s.SoftDeleteSubscription(ctx, subLow); err != nil {
		t.Fatal(err)
	}
	m, _ = s.EndpointRateLimits(ctx)
	if m[epID] != 10.0 {
		t.Fatalf("MIN rps after delete = %v, want 10", m[epID])
	}
}
