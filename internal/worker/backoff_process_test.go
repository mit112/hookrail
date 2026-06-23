//go:build integration

package worker_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mit112/hookrail/internal/store"
)

// seedWithBackoff builds a pipeline whose subscription carries a 200ms cap,
// delivering to url; returns the delivery id. (Mirrors the package `seed`
// helper but sets backoff_policy and uses masterKey() so the worker can decrypt.)
func seedWithBackoff(t *testing.T, s *store.Store, url string) string {
	t.Helper()
	ctx := context.Background()
	keyID, _, err := s.CreateProducerKey(ctx, "bo", []string{"*"})
	if err != nil {
		t.Fatal(err)
	}
	epID, _, err := s.CreateEndpoint(ctx, masterKey(), url, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateSubscriptionFull(ctx, store.SubInput{
		TopicPattern: "bo.*", EndpointID: epID, MaxAttempts: 8,
		BackoffPolicy: []byte(`{"base_ms":200,"cap_ms":200}`),
	}); err != nil {
		t.Fatal(err)
	}
	res, err := s.IngestEvent(ctx, store.IngestParams{ProducerKeyID: keyID, Topic: "bo.x", Payload: []byte(`{}`)})
	if err != nil || len(res.DeliveryIDs) != 1 {
		t.Fatalf("ingest: %v", err)
	}
	return res.DeliveryIDs[0]
}

func TestProcessUsesPerSubBackoff(t *testing.T) {
	s := testStore(t)
	recv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "3600") // 1 hour
		w.WriteHeader(503)
	}))
	defer recv.Close()
	id := seedWithBackoff(t, s, recv.URL)

	newWorker(s).Process(context.Background(), id)

	if got := state(t, s, id); got != "retry_scheduled" {
		t.Fatalf("state = %s, want retry_scheduled", got)
	}
	var next time.Time
	_ = s.Pool.QueryRow(context.Background(), `SELECT next_attempt_at FROM deliveries WHERE id=$1`, id).Scan(&next)
	if until := time.Until(next); until > time.Second {
		t.Fatalf("next_attempt_at %v out; per-sub 200ms cap not applied by the worker (default would honor the 1h Retry-After)", until)
	}
}
