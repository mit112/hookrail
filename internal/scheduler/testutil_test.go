//go:build integration

package scheduler_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/mit112/hookrail/internal/store"
)

var (
	schedOnce sync.Once
	schedDSN  string
	schedSeq  atomic.Int64
)

func schedTestStore(t *testing.T) *store.Store {
	t.Helper()
	ctx := context.Background()
	schedOnce.Do(func() {
		pgc, err := tcpostgres.Run(ctx, "postgres:16-alpine",
			tcpostgres.WithDatabase("hookrail"), tcpostgres.WithUsername("hookrail"), tcpostgres.WithPassword("hookrail"),
			testcontainers.WithWaitStrategy(wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).WithStartupTimeout(60*time.Second)))
		if err != nil {
			t.Fatalf("pg container: %v", err)
		}
		schedDSN, err = pgc.ConnectionString(ctx, "sslmode=disable")
		if err != nil {
			t.Fatal(err)
		}
	})
	name := fmt.Sprintf("hookrail_s%d", schedSeq.Add(1))
	admin, err := pgx.Connect(ctx, schedDSN)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	_ = admin.Close(ctx)
	s, err := store.Open(ctx, strings.Replace(schedDSN, "/hookrail?", "/"+name+"?", 1))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s
}

// schedSeed creates a producer key + endpoint + one subscription on pattern.
func schedSeed(t *testing.T, s *store.Store, pattern string) string {
	t.Helper()
	ctx := context.Background()
	keyID, _, err := s.CreateProducerKey(ctx, "sched")
	if err != nil {
		t.Fatal(err)
	}
	epID, _, err := s.CreateEndpoint(ctx, [32]byte{}, "https://example.com/h", "seed")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateSubscriptionFull(ctx, store.SubInput{TopicPattern: pattern, EndpointID: epID, MaxAttempts: 3}); err != nil {
		t.Fatal(err)
	}
	return keyID
}
