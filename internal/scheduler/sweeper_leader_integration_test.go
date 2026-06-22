//go:build integration

package scheduler_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mit112/hookrail/internal/leader"
	"github.com/mit112/hookrail/internal/scheduler"
	"github.com/mit112/hookrail/internal/store"
)

type fakePublish struct {
	count atomic.Int64
}

func (f *fakePublish) Publish(_ context.Context, _ string) error {
	f.count.Add(1)
	return nil
}

func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("condition not met within %v", timeout)
}

func TestTwoSchedulerElectorsIntegration(t *testing.T) {
	s := schedTestStore(t)
	keyID := schedSeed(t, s, "test")
	ctx := context.Background()

	epID, _, err := s.CreateEndpoint(ctx, [32]byte{}, "https://example.com/h", "int")
	if err != nil {
		t.Fatal(err)
	}
	subID, err := s.CreateSubscriptionFull(ctx, store.SubInput{TopicPattern: "test", EndpointID: epID, MaxAttempts: 3})
	if err != nil {
		t.Fatal(err)
	}
	_ = keyID

	for i := 0; i < 5; i++ {
		eid := uuid.NewString()
		did := uuid.NewString()
		if _, err := s.Pool.Exec(ctx,
			`INSERT INTO events (id, producer_key_id, topic, payload, payload_size)
			 VALUES ($1, $2, 'test', '{}'::jsonb, 2)`, eid, keyID); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Pool.Exec(ctx,
			`INSERT INTO deliveries (id, event_id, subscription_id, endpoint_id, state, next_attempt_at)
			 VALUES ($1, $2, $3, $4, 'pending', now())`, did, eid, subID, epID); err != nil {
			t.Fatal(err)
		}
	}

	pubA := &fakePublish{}
	pubB := &fakePublish{}

	swA := &scheduler.Sweeper{Source: s, Publisher: pubA, BatchSize: 100}
	swB := &scheduler.Sweeper{Source: s, Publisher: pubB, BatchSize: 100}

	var aLeader, bLeader atomic.Bool
	elA := leader.New(schedDSN, leader.SchedulerLeaderLockKey, 100*time.Millisecond,
		func(v bool) { aLeader.Store(v) })
	elB := leader.New(schedDSN, leader.SchedulerLeaderLockKey, 100*time.Millisecond,
		func(v bool) { bLeader.Store(v) })

	aCtx, aCancel := context.WithCancel(context.Background())
	defer aCancel()
	bCtx, bCancel := context.WithCancel(context.Background())
	defer bCancel()

	go func() { _ = elA.Run(aCtx, 50*time.Millisecond, swA.Startup, swA.Cycle) }()
	go func() { _ = elB.Run(bCtx, 50*time.Millisecond, swB.Startup, swB.Cycle) }()

	waitFor(t, 3*time.Second, func() bool {
		return aLeader.Load() != bLeader.Load()
	})

	var standbyFlag *atomic.Bool
	var leaderPub, standbyPub *fakePublish
	var leaderCancel, standbyCancel context.CancelFunc
	if aLeader.Load() {
		standbyFlag = &bLeader
		leaderPub, standbyPub = pubA, pubB
		leaderCancel, standbyCancel = aCancel, bCancel
	} else {
		standbyFlag = &aLeader
		leaderPub, standbyPub = pubB, pubA
		leaderCancel, standbyCancel = bCancel, aCancel
	}

	if leaderPub.count.Load() < 5 {
		t.Fatalf("leader published %d, want >= 5", leaderPub.count.Load())
	}
	if standbyPub.count.Load() != 0 {
		t.Fatalf("standby published %d, want 0", standbyPub.count.Load())
	}

	standbyBefore := standbyPub.count.Load()
	leaderCancel()

	waitFor(t, 5*time.Second, func() bool {
		return standbyFlag.Load() && standbyPub.count.Load() > standbyBefore
	})

	if standbyPub.count.Load() <= standbyBefore {
		t.Fatalf("standby did not publish after promotion")
	}

	standbyCancel()
	time.Sleep(200 * time.Millisecond)

	elC := leader.New(schedDSN, leader.SchedulerLeaderLockKey, 200*time.Millisecond, func(bool) {})
	ok, err := elC.TryAcquireForTest(ctx)
	if err != nil {
		t.Fatalf("third elector: %v", err)
	}
	if !ok {
		t.Fatal("lock was not released")
	}
	elC.ReleaseForTest(ctx)
}
