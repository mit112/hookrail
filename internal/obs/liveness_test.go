package obs

import (
	"net/http/httptest"
	"testing"
	"time"
)

func TestLivenessStaleAfterTTL(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	l := NewLiveness(30 * time.Second)
	l.now = func() time.Time { return now }
	l.Beat()

	if !l.Alive() {
		t.Fatal("should be alive immediately after Beat")
	}
	now = now.Add(29 * time.Second)
	if !l.Alive() {
		t.Fatal("should still be alive within ttl")
	}
	now = now.Add(2 * time.Second) // 31s since last beat
	if l.Alive() {
		t.Fatal("should be stale past ttl")
	}
	// A fresh beat revives it.
	l.Beat()
	if !l.Alive() {
		t.Fatal("should be alive again after a new Beat")
	}
}

func TestLivenessHandler(t *testing.T) {
	now := time.Unix(2_000_000, 0)
	l := NewLiveness(10 * time.Second)
	l.now = func() time.Time { return now }
	l.Beat()

	w := httptest.NewRecorder()
	l.Handler(w, httptest.NewRequest("GET", "/healthz", nil))
	if w.Code != 200 {
		t.Fatalf("alive: want 200, got %d", w.Code)
	}

	now = now.Add(11 * time.Second)
	w = httptest.NewRecorder()
	l.Handler(w, httptest.NewRequest("GET", "/healthz", nil))
	if w.Code != 503 {
		t.Fatalf("stale: want 503, got %d", w.Code)
	}
}
