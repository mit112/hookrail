package worker

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/mit112/hookrail/internal/queue"
)

// fakeNoGroupQueue always fails Read with NOGROUP. EnsureGroup succeeds and, after
// `cancelAfter` calls, cancels the intake ctx so the loop exits — proving the read
// loop (a) re-EnsureGroups on NOGROUP, (b) fast-retries on success, and (c) honors
// intakeCtx (no infinite spin). Codex MAJOR-3 / MINOR-2.
type fakeNoGroupQueue struct {
	mu          sync.Mutex
	ensureCalls int
	readCalls   int
	cancelAfter int   // >0: cancel inside EnsureGroup after this many calls (success path)
	ensureErr   error // set: EnsureGroup fails (backoff path); test drives cancel
	cancel      context.CancelFunc
}

func (f *fakeNoGroupQueue) Read(ctx context.Context, _ string, _ int, _ time.Duration) ([]queue.Msg, error) {
	f.mu.Lock()
	f.readCalls++
	f.mu.Unlock()
	return nil, errors.New("NOGROUP No such key or consumer group")
}

func (f *fakeNoGroupQueue) EnsureGroup(ctx context.Context) error {
	f.mu.Lock()
	f.ensureCalls++
	n := f.ensureCalls
	f.mu.Unlock()
	if f.ensureErr != nil {
		return f.ensureErr
	}
	if f.cancelAfter > 0 && n >= f.cancelAfter {
		f.cancel()
	}
	return nil
}

func (f *fakeNoGroupQueue) Autoclaim(context.Context, string, time.Duration, int) ([]queue.Msg, error) {
	return nil, nil
}
func (f *fakeNoGroupQueue) Ack(context.Context, string) error     { return nil }
func (f *fakeNoGroupQueue) Publish(context.Context, string) error { return nil }

func TestRun_NoGroupRecovery_ReEnsuresAndHonorsCtx(t *testing.T) {
	intakeCtx, cancel := context.WithCancel(context.Background())
	fq := &fakeNoGroupQueue{cancelAfter: 3, cancel: cancel}
	w := &Worker{Queue: fq, Consumer: "c", Lease: time.Second}

	done := make(chan error, 1)
	go func() { done <- w.Run(intakeCtx, context.Background()) }()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatal("Run did not exit — NOGROUP path likely ignores intakeCtx or spins without re-ensuring")
	}

	fq.mu.Lock()
	defer fq.mu.Unlock()
	if fq.ensureCalls != 3 {
		t.Fatalf("EnsureGroup calls=%d want 3 (re-ensure on each NOGROUP, fast-retry on success)", fq.ensureCalls)
	}
	if fq.readCalls < 3 {
		t.Fatalf("Read calls=%d want >=3 (loop retried after each re-ensure)", fq.readCalls)
	}
}

// TestRun_NoGroupRecovery_BacksOffWhenEnsureFails proves the MAJOR fix: when the
// re-EnsureGroup itself fails (master still settling), the loop must NOT hot-spin
// XREADGROUP->NOGROUP->failed-CREATE; it falls through to the bounded ~1s backoff.
// Within a 200ms pre-cancel window only one ensure attempt can occur — a hot-spin
// would register thousands.
func TestRun_NoGroupRecovery_BacksOffWhenEnsureFails(t *testing.T) {
	intakeCtx, cancel := context.WithCancel(context.Background())
	fq := &fakeNoGroupQueue{ensureErr: errors.New("connection refused"), cancel: cancel}
	w := &Worker{Queue: fq, Consumer: "c", Lease: time.Second}

	done := make(chan error, 1)
	go func() { done <- w.Run(intakeCtx, context.Background()) }()

	time.Sleep(200 * time.Millisecond) // shorter than the 1s backoff
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not exit")
	}

	fq.mu.Lock()
	defer fq.mu.Unlock()
	if fq.ensureCalls != 1 {
		t.Fatalf("ensureCalls=%d want 1 within 200ms (proves bounded backoff on ensure failure, not hot-spin)", fq.ensureCalls)
	}
}
