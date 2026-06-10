//go:build integration

package queue_test

import (
	"context"
	"sync"
	"testing"
	"time"

	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"

	"github.com/mit112/hookrail/internal/queue"
)

var (
	once sync.Once
	addr string
)

func testQueue(t *testing.T, group string) *queue.Queue {
	t.Helper()
	once.Do(func() {
		ctx := context.Background()
		rc, err := tcredis.Run(ctx, "redis:7-alpine")
		if err != nil {
			t.Fatalf("redis container: %v", err)
		}
		ep, err := rc.ConnectionString(ctx) // "redis://host:port"
		if err != nil {
			t.Fatal(err)
		}
		addr = ep
	})
	q, err := queue.New(addr, "deliveries:"+t.Name(), group)
	if err != nil {
		t.Fatal(err)
	}
	if err := q.EnsureGroup(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(q.Close)
	return q
}

func TestPublishReadAck(t *testing.T) {
	q := testQueue(t, "deliverers")
	ctx := context.Background()
	if err := q.Publish(ctx, "01JDELIVERY000000000000001"); err != nil {
		t.Fatal(err)
	}
	msgs, err := q.Read(ctx, "w1", 10, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 || msgs[0].DeliveryID != "01JDELIVERY000000000000001" {
		t.Fatalf("read %v, want one msg with the published delivery id", msgs)
	}
	if err := q.Ack(ctx, msgs[0].ID); err != nil {
		t.Fatal(err)
	}
	pending, err := q.PendingCount(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if pending != 0 {
		t.Fatalf("PEL count = %d after ack, want 0", pending)
	}
}

func TestEnsureGroupIdempotent(t *testing.T) {
	q := testQueue(t, "deliverers")
	// second call must swallow BUSYGROUP
	if err := q.EnsureGroup(context.Background()); err != nil {
		t.Fatalf("EnsureGroup twice: %v", err)
	}
}

func TestAutoclaimRecoversAbandonedMessage(t *testing.T) {
	q := testQueue(t, "deliverers")
	ctx := context.Background()
	if err := q.Publish(ctx, "01JDELIVERY000000000000002"); err != nil {
		t.Fatal(err)
	}
	// w1 reads but never acks (crashed worker — §10 row 1)
	if msgs, err := q.Read(ctx, "w1", 10, time.Second); err != nil || len(msgs) != 1 {
		t.Fatalf("w1 read: %v %v", msgs, err)
	}
	time.Sleep(50 * time.Millisecond)
	// w2 autoclaims messages idle > 10ms
	claimed, err := q.Autoclaim(ctx, "w2", 10*time.Millisecond, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(claimed) != 1 || claimed[0].DeliveryID != "01JDELIVERY000000000000002" {
		t.Fatalf("autoclaim got %v, want the abandoned message", claimed)
	}
}
