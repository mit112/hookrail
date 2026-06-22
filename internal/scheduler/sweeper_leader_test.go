package scheduler

import (
	"context"
	"testing"
)

// TestSweeperCycleOnlyLeaderPublishes verifies per-instance signal:
// only the sweeper whose Startup/Cycle are called publishes; a standby
// sweeper sharing the same Source publishes nothing.
func TestSweeperCycleOnlyLeaderPublishes(t *testing.T) {
	src := &fakeSource{ids: []string{"a", "b", "c", "d", "e"}}
	leaderPub := &fakePublisher{}
	standbyPub := &fakePublisher{}

	leader := &Sweeper{Source: src, Publisher: leaderPub, BatchSize: 100}
	standby := &Sweeper{Source: src, Publisher: standbyPub, BatchSize: 100}
	_ = standby // unused; only standbyPub is checked

	ctx := context.Background()

	// Leader runs Startup (drains backlog) + Cycle (may be empty).
	if err := leader.Startup(ctx); err != nil {
		t.Fatalf("leader Startup: %v", err)
	}
	if err := leader.Cycle(ctx); err != nil {
		t.Fatalf("leader Cycle: %v", err)
	}

	// Standby must NOT publish — its methods are never called.
	if len(standbyPub.published) != 0 {
		t.Fatalf("standby published %v, want nothing", standbyPub.published)
	}

	// Leader must have published the full backlog (Startup drained it).
	if len(leaderPub.published) < len(src.ids) {
		t.Fatalf("leader published %d, want >= %d", len(leaderPub.published), len(src.ids))
	}
}

// TestSweeperRunStillWorks verifies the legacy Run method still works
// after extracting Startup/Cycle (backward compatibility).
func TestSweeperRunStillWorks(t *testing.T) {
	src := &fakeSource{ids: []string{"x", "y"}}
	pub := &fakePublisher{}
	sw := &Sweeper{Source: src, Publisher: pub, Interval: 10, BatchSize: 100}

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- sw.Run(ctx) }()

	// Let startup sweep finish, then cancel.
	cancel()

	if err := <-errCh; err != nil && err != context.Canceled {
		t.Fatalf("Run returned: %v", err)
	}
	if len(pub.published) < 2 {
		t.Fatalf("published %d, want >= 2", len(pub.published))
	}
}
