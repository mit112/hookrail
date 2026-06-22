package main

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// receiver is the §11 test consumer. Mode is selected by path:
//   POST /succeed          → 200
//   POST /fail/{n}         → 500 for the first n attempts per delivery id, then 200
//   POST /timeout          → sleeps 30s (longer than the §8 total budget)
//   POST /flap             → 50% random 500
//   POST /redirect         → 302 (Hookrail must never follow)
//   POST /slow-body        → 200 header then a dribbled 1MB body (capped-read test)
//   POST /retry-after      → 429 with Retry-After: 120
//   GET  /received?delivery_id=X → count of 2xx receipts for X (e2e assertions)
//   GET  /stats → {"receipts","distinct_deliveries","duplicates"} — the §11
//                 duplicate ledger; HTTP-level duplicates exist ONLY here
type receiver struct {
	mu          sync.Mutex
	failures    map[string]int  // delivery id → failures served so far
	received    map[string]int  // delivery id → 2xx receipts
	ordered     map[string][]int // ordering_key → arrival-ordered seqs
	orderedSeen map[string]bool // delivery id → already recorded (dedup against re-delivery)
}

func newReceiver() *receiver {
	return &receiver{
		failures:    map[string]int{},
		received:    map[string]int{},
		ordered:     map[string][]int{},
		orderedSeen: map[string]bool{},
	}
}

func (rc *receiver) recordSuccess(id string) {
	rc.mu.Lock()
	rc.received[id]++
	rc.mu.Unlock()
}

func (rc *receiver) recordOrdered(deliveryID, key string, seq int) {
	rc.mu.Lock()
	if !rc.orderedSeen[deliveryID] {
		rc.orderedSeen[deliveryID] = true
		rc.ordered[key] = append(rc.ordered[key], seq)
	}
	rc.mu.Unlock()
}

func (rc *receiver) handler() http.Handler {
	mux := http.NewServeMux()
	did := func(r *http.Request) string { return r.Header.Get("hookrail-delivery-id") }

	mux.HandleFunc("POST /succeed", func(w http.ResponseWriter, r *http.Request) {
		rc.recordSuccess(did(r))
		w.WriteHeader(200)
	})
	mux.HandleFunc("POST /fail/{n}", func(w http.ResponseWriter, r *http.Request) {
		n, _ := strconv.Atoi(r.PathValue("n"))
		rc.mu.Lock()
		rc.failures[did(r)]++
		failed := rc.failures[did(r)]
		rc.mu.Unlock()
		if failed <= n {
			w.WriteHeader(500)
			return
		}
		rc.recordSuccess(did(r))
		w.WriteHeader(200)
	})
	mux.HandleFunc("POST /timeout", func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(30 * time.Second):
		case <-r.Context().Done():
		}
	})
	mux.HandleFunc("POST /flap", func(w http.ResponseWriter, r *http.Request) {
		if rand.Intn(2) == 0 { //nolint:gosec // test traffic
			w.WriteHeader(500)
			return
		}
		rc.recordSuccess(did(r))
		w.WriteHeader(200)
	})
	mux.HandleFunc("POST /redirect", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/succeed", http.StatusFound)
	})
	mux.HandleFunc("POST /retry-after", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "120")
		w.WriteHeader(429)
	})
	mux.HandleFunc("POST /slow-body", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		rc.recordSuccess(did(r))
		f, _ := w.(http.Flusher)
		chunk := strings.Repeat("x", 1024)
		for i := 0; i < 1024; i++ { // 1MB total, dribbled
			if _, err := w.Write([]byte(chunk)); err != nil {
				return
			}
			if f != nil {
				f.Flush()
			}
			time.Sleep(5 * time.Millisecond)
		}
	})
	mux.HandleFunc("POST /ordered", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Key string `json:"key"`
			Seq int    `json:"seq"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Key == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		rc.recordSuccess(did(r))
		rc.recordOrdered(did(r), body.Key, body.Seq)
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("POST /ordered-flap", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Key string `json:"key"`
			Seq int    `json:"seq"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Key == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if rand.Intn(10) == 0 { //nolint:gosec // test traffic — 10% flap (vs 50%)
			w.WriteHeader(500)
			return
		}
		rc.recordSuccess(did(r)) // only a real 200 counts toward the /stats ledger
		rc.recordOrdered(did(r), body.Key, body.Seq)
		w.WriteHeader(200)
	})
	mux.HandleFunc("POST /ordered-slow", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Key string `json:"key"`
			Seq int    `json:"seq"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Key == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		rc.recordSuccess(did(r))
		rc.recordOrdered(did(r), body.Key, body.Seq)
		w.WriteHeader(http.StatusOK)
		f, _ := w.(http.Flusher)
		chunk := strings.Repeat("x", 1024)
		// dribble the body; the worker reads ≤64KB then closes the connection, holding the
		// delivery in-flight ~0.3s (mirrors /slow-body) — enough for fault injection mid-drain.
		for i := 0; i < 400; i++ {
			if _, err := w.Write([]byte(chunk)); err != nil {
				return
			}
			if f != nil {
				f.Flush()
			}
			time.Sleep(5 * time.Millisecond)
		}
	})
	mux.HandleFunc("GET /received", func(w http.ResponseWriter, r *http.Request) {
		rc.mu.Lock()
		n := rc.received[r.URL.Query().Get("delivery_id")]
		rc.mu.Unlock()
		_, _ = fmt.Fprintf(w, "%d", n)
	})
	mux.HandleFunc("GET /stats", func(w http.ResponseWriter, _ *http.Request) {
		rc.mu.Lock()
		total := 0
		for _, c := range rc.received {
			total += c
		}
		distinct := len(rc.received)
		rc.mu.Unlock()
		_, _ = fmt.Fprintf(w, `{"receipts":%d,"distinct_deliveries":%d,"duplicates":%d}`,
			total, distinct, total-distinct)
	})
	mux.HandleFunc("GET /ordered-stats", func(w http.ResponseWriter, _ *http.Request) {
		rc.mu.Lock()
		out := make(map[string][]int, len(rc.ordered))
		for k, v := range rc.ordered {
			c := make([]int, len(v))
			copy(c, v)
			out[k] = c
		}
		rc.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	})
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
	})
	return mux
}
