//go:build integration

package admin_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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

	"github.com/mit112/hookrail/internal/admin"
	"github.com/mit112/hookrail/internal/ratelimit"
	"github.com/mit112/hookrail/internal/ssrf"
	"github.com/mit112/hookrail/internal/store"
)

const testToken = "admin-test-token"

var (
	once     sync.Once
	adminDSN string
	dbSeq    atomic.Int64
)

// noQueue is a Publisher that records replay republish calls without Redis.
type noQueue struct{ published []string }

func (n *noQueue) Publish(_ context.Context, id string) error { n.published = append(n.published, id); return nil }
func (n *noQueue) Ping(context.Context) error                 { return nil }

func newServer(t *testing.T) (*admin.Server, *store.Store) {
	t.Helper()
	ctx := context.Background()
	once.Do(func() {
		pgc, err := tcpostgres.Run(ctx, "postgres:16-alpine",
			tcpostgres.WithDatabase("hookrail"), tcpostgres.WithUsername("hookrail"), tcpostgres.WithPassword("hookrail"),
			testcontainers.WithWaitStrategy(wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).WithStartupTimeout(60*time.Second)))
		if err != nil {
			t.Fatalf("pg container: %v", err)
		}
		adminDSN, err = pgc.ConnectionString(ctx, "sslmode=disable")
		if err != nil {
			t.Fatal(err)
		}
	})
	name := fmt.Sprintf("hookrail_t%d", dbSeq.Add(1))
	a, err := pgx.Connect(ctx, adminDSN)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	_ = a.Close(ctx)
	s, err := store.Open(ctx, strings.Replace(adminDSN, "/hookrail?", "/"+name+"?", 1))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	// AllowHTTP so the test receiver URL passes SSRF (dev policy).
	pol := ssrf.Policy{AllowHTTP: true}
	srv := admin.New(s, &noQueue{}, [32]byte{}, pol, ratelimit.NewRegistry(500, 1000), testToken)
	return srv, s
}

// do issues an authed request against the admin handler and returns the recorder.
func do(t *testing.T, srv *admin.Server, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var rdr *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	} else {
		rdr = bytes.NewReader(nil)
	}
	r := httptest.NewRequest(method, path, rdr)
	r.Header.Set("Authorization", "Bearer "+testToken)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)
	return w
}
