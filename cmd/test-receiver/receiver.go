package main

import (
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
	mu       sync.Mutex
	failures map[string]int // delivery id → failures served so far
	received map[string]int // delivery id → 2xx receipts
}

func newReceiver() *receiver {
	return &receiver{failures: map[string]int{}, received: map[string]int{}}
}

func (rc *receiver) recordSuccess(id string) {
	rc.mu.Lock()
	rc.received[id]++
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
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
	})
	return mux
}
