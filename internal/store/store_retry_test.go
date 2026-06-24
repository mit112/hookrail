package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestOpenWithRetry_SucceedsImmediately(t *testing.T) {
	want := &Store{}
	calls := 0
	got, err := openWithRetry(context.Background(), time.Second, time.Millisecond, func(context.Context) (*Store, error) {
		calls++
		return want, nil
	})
	if err != nil || got != want || calls != 1 {
		t.Fatalf("got=%v err=%v calls=%d, want store,nil,1", got, err, calls)
	}
}

func TestOpenWithRetry_RetriesThenSucceeds(t *testing.T) {
	want := &Store{}
	calls := 0
	got, err := openWithRetry(context.Background(), time.Second, time.Millisecond, func(context.Context) (*Store, error) {
		calls++
		if calls < 3 {
			return nil, errors.New("primary unavailable")
		}
		return want, nil
	})
	if err != nil || got != want || calls != 3 {
		t.Fatalf("got=%v err=%v calls=%d, want store,nil,3", got, err, calls)
	}
}

func TestOpenWithRetry_DeadlineReturnsLastErr(t *testing.T) {
	sentinel := errors.New("still down")
	calls := 0
	const maxWait = 40 * time.Millisecond
	start := time.Now()
	_, err := openWithRetry(context.Background(), maxWait, time.Millisecond, func(context.Context) (*Store, error) {
		calls++
		return nil, sentinel
	})
	elapsed := time.Since(start)
	if err == nil || !errors.Is(err, sentinel) {
		t.Fatalf("err=%v, want wraps sentinel", err)
	}
	if calls < 2 {
		t.Fatalf("calls=%d, want >=2 (retried before deadline)", calls)
	}
	// Must not sleep meaningfully past the deadline (Codex MAJOR: hard bound).
	if elapsed > maxWait+50*time.Millisecond {
		t.Fatalf("elapsed=%v, want <= maxWait(%v)+slack (no sleep past deadline)", elapsed, maxWait)
	}
}

func TestOpenWithRetry_SucceedsJustBeforeDeadline(t *testing.T) {
	want := &Store{}
	calls := 0
	got, err := openWithRetry(context.Background(), 200*time.Millisecond, 5*time.Millisecond, func(context.Context) (*Store, error) {
		calls++
		if calls < 4 { // fail a few times, then succeed well within the window
			return nil, errors.New("primary mid-promotion")
		}
		return want, nil
	})
	if err != nil || got != want {
		t.Fatalf("got=%v err=%v, want store,nil (recover before deadline)", got, err)
	}
}

func TestOpenWithRetry_NonPositiveBackoffDoesNotBusySpin(t *testing.T) {
	// initialBackoff=0 must be normalized, not busy-spin; with a short maxWait
	// an always-failing attempt should make only a handful of calls.
	calls := 0
	_, _ = openWithRetry(context.Background(), 30*time.Millisecond, 0, func(context.Context) (*Store, error) {
		calls++
		return nil, errors.New("down")
	})
	if calls > 5 {
		t.Fatalf("calls=%d, want small (no busy-spin with normalized backoff)", calls)
	}
}

func TestOpenWithRetry_SingleAttemptWhenMaxWaitZero(t *testing.T) {
	sentinel := errors.New("down")
	calls := 0
	_, err := openWithRetry(context.Background(), 0, time.Millisecond, func(context.Context) (*Store, error) {
		calls++
		return nil, sentinel
	})
	if err == nil || !errors.Is(err, sentinel) || calls != 1 {
		t.Fatalf("err=%v calls=%d, want wraps sentinel, calls=1 (fail-fast)", err, calls)
	}
}

func TestOpenWithRetry_RespectsContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	_, err := openWithRetry(ctx, time.Second, 10*time.Millisecond, func(context.Context) (*Store, error) {
		return nil, errors.New("down")
	})
	if err == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v, want wraps context.Canceled", err)
	}
	if time.Since(start) > 200*time.Millisecond {
		t.Fatalf("cancel not prompt: elapsed=%v", time.Since(start))
	}
}
