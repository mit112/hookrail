//go:build integration

package api_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mit112/hookrail/internal/api"
	"github.com/mit112/hookrail/internal/ratelimit"
	"github.com/mit112/hookrail/internal/store"
)

// orderedServer builds an API server backed by ONE ordered subscription on
// orders.* with a caller-chosen per-key backlog cap, returning the producer key.
func orderedServer(t *testing.T, backlogMax int) (*httptest.Server, string) {
	t.Helper()
	s := testStore(t)
	ctx := context.Background()
	_, key, err := s.CreateProducerKey(ctx, "backlog-test", []string{"*"})
	if err != nil {
		t.Fatal(err)
	}
	epID, _, err := s.CreateEndpoint(ctx, masterKey(), "https://consumer.example/hook", "t")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateSubscriptionFull(ctx, store.SubInput{
		TopicPattern: "orders.*", EndpointID: epID, MaxAttempts: 8, Ordered: true,
	}); err != nil {
		t.Fatal(err)
	}
	srv := api.New(s, &nopQueue{}, ratelimit.NewRegistry(1000, 1000), 24*time.Hour, 256, backlogMax)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, key
}

func postOrdered(t *testing.T, ts *httptest.Server, key, orderingKey, body string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/v1/events", bytes.NewBufferString(body))
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	if orderingKey != "" {
		req.Header.Set("X-Hookrail-Ordering-Key", orderingKey)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

// TestBacklogCap429 proves the (cap+1)-th POST for ONE ordering key is rejected
// with 429 + Retry-After, while other keys and the keyless (unordered) path on
// the same subscription stay unaffected (§D8, Task 21).
func TestBacklogCap429(t *testing.T) {
	const cap = 3
	ts, key := orderedServer(t, cap)
	body := `{"topic":"orders.created","payload":{"n":1}}`

	// First `cap` POSTs for cust-1 are accepted (backlog 1..cap).
	for i := 0; i < cap; i++ {
		resp := postOrdered(t, ts, key, "cust-1", body)
		if resp.StatusCode != http.StatusAccepted {
			t.Fatalf("cust-1 POST %d = %d, want 202: ", i+1, resp.StatusCode)
		}
		_ = resp.Body.Close()
	}

	// The (cap+1)-th for cust-1 overflows the backlog → 429 + Retry-After.
	resp := postOrdered(t, ts, key, "cust-1", body)
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("overflow POST = %d, want 429", resp.StatusCode)
	}
	if ra := resp.Header.Get("Retry-After"); ra == "" {
		t.Fatal("429 missing Retry-After header")
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/problem+json" {
		t.Fatalf("Content-Type = %q, want application/problem+json", ct)
	}

	// A DIFFERENT key has its own independent backlog → still accepted.
	r2 := postOrdered(t, ts, key, "cust-2", body)
	if r2.StatusCode != http.StatusAccepted {
		t.Fatalf("cust-2 POST = %d, want 202 (other keys unaffected)", r2.StatusCode)
	}
	_ = r2.Body.Close()

	// The keyless (unordered) path assigns no seq and is never backlog-capped.
	r3 := postOrdered(t, ts, key, "", body)
	if r3.StatusCode != http.StatusAccepted {
		t.Fatalf("keyless POST = %d, want 202 (unordered unaffected)", r3.StatusCode)
	}
	_ = r3.Body.Close()
}
