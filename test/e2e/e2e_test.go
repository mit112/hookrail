//go:build e2e

// Package e2e drives a running compose stack. Required env:
//   E2E_API_URL       (default http://localhost:8080)
//   E2E_RECEIVER_URL  (default http://localhost:9090)
//   E2E_PRODUCER_KEY  (from `make seed` — required)
package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"
)

func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

var (
	apiURL  = env("E2E_API_URL", "http://localhost:8080")
	recvURL = env("E2E_RECEIVER_URL", "http://localhost:9090")
	key     = os.Getenv("E2E_PRODUCER_KEY")
)

type ingestResp struct {
	EventID     string   `json:"event_id"`
	DeliveryIDs []string `json:"delivery_ids"`
}

func postEvent(t *testing.T, topic string, payload string) ingestResp {
	t.Helper()
	if key == "" {
		t.Fatal("E2E_PRODUCER_KEY not set — run `make seed` and export the key")
	}
	body := fmt.Sprintf(`{"topic":%q,"payload":%s}`, topic, payload)
	req, _ := http.NewRequest(http.MethodPost, apiURL+"/v1/events", bytes.NewBufferString(body))
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("ingest status = %d, want 202", resp.StatusCode)
	}
	var out ingestResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

// eventState polls GET /v1/events/{id} until every delivery reaches `want`
// or the deadline passes; returns the final state seen.
func waitForState(t *testing.T, eventID, want string, deadline time.Duration) string {
	t.Helper()
	var last string
	until := time.Now().Add(deadline)
	for time.Now().Before(until) {
		req, _ := http.NewRequest(http.MethodGet, apiURL+"/v1/events/"+eventID, nil)
		req.Header.Set("Authorization", "Bearer "+key)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		var st struct {
			Deliveries []struct {
				State string `json:"state"`
			} `json:"deliveries"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&st)
		_ = resp.Body.Close()
		if len(st.Deliveries) > 0 {
			last = st.Deliveries[0].State
			if last == want {
				return last
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	return last
}

func TestHappyPath(t *testing.T) {
	res := postEvent(t, "demo.e2e.happy", `{"n":1}`)
	if got := waitForState(t, res.EventID, "succeeded", 15*time.Second); got != "succeeded" {
		t.Fatalf("delivery state = %q, want succeeded", got)
	}
	// receiver actually got it exactly once (steady state: zero duplicates, §11)
	resp, err := http.Get(recvURL + "/received?delivery_id=" + res.DeliveryIDs[0])
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var n int
	if _, err := fmt.Fscan(resp.Body, &n); err != nil || n != 1 {
		t.Fatalf("receiver count = %d (err %v), want 1", n, err)
	}
}

// NOTE: the default seeded subscription points at /succeed. The retry and DLQ
// paths use dedicated subscriptions seeded by scripts/e2e.sh with topics
// demo-retry.* → /fail/2 and demo-dlq.* → /redirect (separate seed runs).
func TestRetryPathEventuallySucceeds(t *testing.T) {
	if os.Getenv("E2E_RETRY_KEY") == "" {
		t.Skip("retry pipeline not seeded (E2E_RETRY_KEY empty)")
	}
	key = os.Getenv("E2E_RETRY_KEY") // same producer auth, retry-topic subscription
	res := postEvent(t, "demo-retry.e2e", `{"n":2}`)
	// 2x500 then 200. Each retry waits for next_attempt_at (jitter ≤5s, ≤10s)
	// PLUS up to one 30s sweep interval before republication — budget ~3min.
	if got := waitForState(t, res.EventID, "succeeded", 180*time.Second); got != "succeeded" {
		t.Fatalf("retry path final state = %q, want succeeded", got)
	}
}

func TestPermanentFailureDeadLetters(t *testing.T) {
	if os.Getenv("E2E_DLQ_KEY") == "" {
		t.Skip("dlq pipeline not seeded (E2E_DLQ_KEY empty)")
	}
	key = os.Getenv("E2E_DLQ_KEY")
	res := postEvent(t, "demo-dlq.e2e", `{"n":3}`)
	if got := waitForState(t, res.EventID, "dead_lettered", 30*time.Second); got != "dead_lettered" {
		t.Fatalf("dlq path final state = %q, want dead_lettered", got)
	}
}
