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
	_, err := openWithRetry(context.Background(), 15*time.Millisecond, time.Millisecond, func(context.Context) (*Store, error) {
		calls++
		return nil, sentinel
	})
	if err == nil || !errors.Is(err, sentinel) {
		t.Fatalf("err=%v, want wraps sentinel", err)
	}
	if calls < 2 {
		t.Fatalf("calls=%d, want >=2 (retried before deadline)", calls)
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
	_, err := openWithRetry(ctx, time.Second, 10*time.Millisecond, func(context.Context) (*Store, error) {
		return nil, errors.New("down")
	})
	if err == nil {
		t.Fatal("expected cancellation error")
	}
}
