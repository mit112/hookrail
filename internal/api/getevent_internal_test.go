//go:build integration

package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
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
	ointOnce sync.Once
	ointDSN  string
	ointSeq  atomic.Int64
)

// testStoreOpen gives the white-box (package api) tests a real *store.Store.
// Same pattern as testStore in server_test.go (package api_test).
func testStoreOpen(t *testing.T) *store.Store {
	t.Helper()
	ctx := context.Background()
	ointOnce.Do(func() {
		pgc, err := tcpostgres.Run(ctx, "postgres:16-alpine",
			tcpostgres.WithDatabase("hookrail"), tcpostgres.WithUsername("hookrail"), tcpostgres.WithPassword("hookrail"),
			testcontainers.WithWaitStrategy(wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).WithStartupTimeout(60*time.Second)))
		if err != nil {
			t.Fatalf("pg container: %v", err)
		}
		ointDSN, err = pgc.ConnectionString(ctx, "sslmode=disable")
		if err != nil {
			t.Fatal(err)
		}
	})
	name := fmt.Sprintf("hookrail_t%d", ointSeq.Add(1))
	admin, err := pgx.Connect(ctx, ointDSN)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	_ = admin.Close(ctx)
	s, err := store.Open(ctx, strings.Replace(ointDSN, "/hookrail?", "/"+name+"?", 1))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s
}

func TestGetEvent_DBError_Returns500(t *testing.T) {
	st := testStoreOpen(t)
	st.Pool.Close() // force a non-ErrNotFound error from GetEventStatus
	s := &Server{store: st}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/v1/events/x", nil)
	req.SetPathValue("id", "x")
	s.getEvent(rr, req)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("want 500, got %d", rr.Code)
	}
}
