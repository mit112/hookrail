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
	cancelAfter int
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
	stop := f.ensureCalls >= f.cancelAfter
	f.mu.Unlock()
	if stop {
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
