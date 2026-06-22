//go:build e2e

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

// postOrderedEvent sends one event with an ordering key and a sequence payload.
func postOrderedEvent(t *testing.T, topic, orderingKey string, seq int) ingestResp {
	t.Helper()
	if key == "" {
		t.Fatal("E2E_PRODUCER_KEY not set — run `make seed` and export the key")
	}
	payload := fmt.Sprintf(`{"key":%q,"seq":%d}`, orderingKey, seq)
	body := fmt.Sprintf(`{"topic":%q,"payload":%s}`, topic, payload)
	req, _ := http.NewRequest(http.MethodPost, apiURL+"/v1/events", bytes.NewBufferString(body))
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Hookrail-Ordering-Key", orderingKey)
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

// fetchOrderedStats polls test-receiver /ordered-stats and returns per-key arrival seqs.
func fetchOrderedStats(t *testing.T) map[string][]int {
	t.Helper()
	resp, err := http.Get(recvURL + "/ordered-stats")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var out map[string][]int
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

// TestOrderedFIFOUnderFlapping verifies an ordered subscription maintains strict
// FIFO through a flapping (500-N-times) consumer; the unordered path is
// exercised by TestHappyPath on the same compose stack.
func TestOrderedFIFOUnderFlapping(t *testing.T) {
	if os.Getenv("E2E_ORDERED_KEY") == "" {
		t.Skip("ordered pipeline not seeded (E2E_ORDERED_KEY empty)")
	}
	key = os.Getenv("E2E_ORDERED_KEY")

	const K = 3  // distinct ordering keys
	const M = 10 // events per key

	// Post K*M events, sequence 1..M per key, interleaved.
	for seq := 1; seq <= M; seq++ {
		for ki := 0; ki < K; ki++ {
			orderingKey := fmt.Sprintf("key-%d", ki)
			postOrderedEvent(t, "demo-ordered.e2e", orderingKey, seq)
		}
	}

	// Wait for all ordered deliveries to drain — poll /ordered-stats.
	deadline := time.Now().Add(5 * time.Minute)
	var stats map[string][]int
	for time.Now().Before(deadline) {
		stats = fetchOrderedStats(t)
		complete := len(stats) == K
		for ki := 0; ki < K && complete; ki++ {
			if len(stats[fmt.Sprintf("key-%d", ki)]) < M {
				complete = false
			}
		}
		if complete {
			break
		}
		time.Sleep(2 * time.Second)
	}

	if len(stats) != K {
		t.Fatalf("ordered keys: got %d, want %d: %v", len(stats), K, stats)
	}

	// Oracle: per-key, the sequence of received events is strictly increasing
	// (no reorder, no gaps, no dupes).
	for ki := 0; ki < K; ki++ {
		keyName := fmt.Sprintf("key-%d", ki)
		seqs, ok := stats[keyName]
		if !ok {
			t.Fatalf("key %q missing from ordered-stats", keyName)
		}
		if len(seqs) != M {
			t.Fatalf("key %q: got %d seqs, want %d: %v", keyName, len(seqs), M, seqs)
		}
		seen := map[int]bool{}
		for i, s := range seqs {
			if s < 1 || s > M {
				t.Fatalf("key %q: seq %d out of range [1,%d] at position %d", keyName, s, M, i)
			}
			if seen[s] {
				t.Fatalf("key %q: duplicate seq %d at position %d", keyName, s, i)
			}
			seen[s] = true
			if i > 0 && s <= seqs[i-1] {
				t.Fatalf("key %q: reorder at position %d: %d after %d", keyName, i, s, seqs[i-1])
			}
		}
		for s := 1; s <= M; s++ {
			if !seen[s] {
				t.Fatalf("key %q: missing seq %d", keyName, s)
			}
		}
	}
	t.Logf("ordered FIFO: %d keys × %d seqs all strictly monotonic, no gaps, no dupes", K, M)
}
