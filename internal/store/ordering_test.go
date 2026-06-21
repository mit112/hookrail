//go:build integration

package store_test

import (
	"context"
	"sync"
	"testing"
)

func TestAssignOrderingSeqStrictUnderConcurrency(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	const N = 50
	seqs := make(chan int64, N)
	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tx, err := st.Pool.Begin(ctx)
			if err != nil {
				t.Errorf("begin tx: %v", err)
				return
			}
			seq, _, err := st.AssignOrderingSeq(ctx, tx, "sub1", "k1")
			if err != nil {
				t.Errorf("AssignOrderingSeq: %v", err)
				if rerr := tx.Rollback(ctx); rerr != nil {
					t.Errorf("rollback on error: %v", rerr)
				}
				return
			}
			if err := tx.Commit(ctx); err != nil {
				t.Errorf("commit tx: %v", err)
				return
			}
			seqs <- seq
		}()
	}
	wg.Wait()
	close(seqs)

	got := map[int64]bool{}
	for s := range seqs {
		got[s] = true
	}
	for i := int64(1); i <= N; i++ {
		if !got[i] {
			t.Fatalf("missing seq %d (gap/dupe)", i)
		}
	}
}
