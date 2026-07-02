// Package obs: liveness heartbeat. A long-running loop (the worker dispatch
// loop, the scheduler leader/sweeper loop) pumps Beat() each iteration; the
// /healthz handler reports 503 once no beat has landed within ttl. This makes a
// WEDGED loop detectable — unlike probing /metrics (or a bare 200 handler),
// which stays green as long as the HTTP goroutine is alive even if the work
// loop has exited or hung, so Kubernetes would never restart the pod.
package obs

import (
	"net/http"
	"sync/atomic"
	"time"
)

type Liveness struct {
	last atomic.Int64 // unix nanos of the most recent Beat
	ttl  time.Duration
	now  func() time.Time
}

// NewLiveness returns a heartbeat that reports live for ttl after each Beat.
// It starts "beating" so a process is live during startup before the first
// loop iteration lands. ttl should be comfortably larger than one loop
// iteration's worst case (intake block + a single Process).
func NewLiveness(ttl time.Duration) *Liveness {
	l := &Liveness{ttl: ttl, now: time.Now}
	l.Beat()
	return l
}

// Beat records a loop iteration. Safe for concurrent callers (worker pool).
func (l *Liveness) Beat() { l.last.Store(l.now().UnixNano()) }

// Alive reports whether a Beat landed within ttl.
func (l *Liveness) Alive() bool {
	return l.now().Sub(time.Unix(0, l.last.Load())) < l.ttl
}

// Handler is a /healthz endpoint: 200 while alive, 503 once the loop goes stale.
func (l *Liveness) Handler(w http.ResponseWriter, _ *http.Request) {
	if l.Alive() {
		w.WriteHeader(http.StatusOK)
		return
	}
	w.WriteHeader(http.StatusServiceUnavailable)
	_, _ = w.Write([]byte("stale: no loop heartbeat within ttl\n"))
}
