package scheduler

import (
	"context"
	"errors"
	"testing"
)

type fakeSource struct{ ids []string } // sorted ascending, like the keyset query

func (f *fakeSource) DueDeliveryIDs(ctx context.Context, afterID string, limit int) ([]string, error) {
	var out []string
	for _, id := range f.ids {
		if id > afterID {
			out = append(out, id)
		}
		if len(out) == limit {
			break
		}
	}
	return out, nil
}

type fakePublisher struct {
	published []string
	failOn    map[string]bool
}

func (f *fakePublisher) Publish(ctx context.Context, id string) error {
	if f.failOn[id] {
		return errors.New("redis down")
	}
	f.published = append(f.published, id)
	return nil
}

func TestRunOncePublishesAllDue(t *testing.T) {
	src := &fakeSource{ids: []string{"a", "b", "c"}}
	pub := &fakePublisher{}
	sw := &Sweeper{Source: src, Publisher: pub, BatchSize: 100}
	n, err := sw.RunOnce(context.Background())
	if err != nil || n != 3 {
		t.Fatalf("RunOnce = (%d, %v), want (3, nil)", n, err)
	}
	if len(pub.published) != 3 {
		t.Fatalf("published %v, want a,b,c", pub.published)
	}
}

func TestRunOnceContinuesPastPublishFailure(t *testing.T) {
	// one bad publish must not strand the rest of the batch — the failed id
	// is still due next sweep, so skipping it now loses nothing (§3.3)
	src := &fakeSource{ids: []string{"a", "bad", "c"}}
	pub := &fakePublisher{failOn: map[string]bool{"bad": true}}
	sw := &Sweeper{Source: src, Publisher: pub, BatchSize: 100}
	n, err := sw.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce returned error: %v", err)
	}
	if n != 2 || len(pub.published) != 2 {
		t.Fatalf("published %d (%v), want 2 (a, c)", n, pub.published)
	}
}

func TestRunOncePaginatesPastBatchSize(t *testing.T) {
	src := &fakeSource{ids: []string{"a", "b", "c", "d", "e"}}
	pub := &fakePublisher{}
	sw := &Sweeper{Source: src, Publisher: pub, BatchSize: 2}
	n, err := sw.RunOnce(context.Background())
	if err != nil || n != 5 {
		t.Fatalf("RunOnce = (%d, %v), want (5, nil) — one sweep must drain ALL due rows", n, err)
	}
}
