//go:build integration

package store_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/mit112/hookrail/internal/store"
)

// startDedicatedPG spins an isolated testcontainers Postgres for tests that
// create cluster-wide roles (the shared testStore container would leak them).
func startDedicatedPG(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	pgc, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("hookrail"),
		tcpostgres.WithUsername("hookrail"),
		tcpostgres.WithPassword("hookrail"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).WithStartupTimeout(60*time.Second)),
	)
	if err != nil {
		t.Fatalf("start dedicated postgres: %v", err)
	}
	t.Cleanup(func() { _ = pgc.Terminate(ctx) })
	dsn, err := pgc.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("dsn: %v", err)
	}
	return dsn
}

// connectAsRole takes an owner DSN and returns a pgx connection as the named
// role with the given password (DSN user/password replaced).
func connectAsRole(t *testing.T, ownerDSN, role, password string) *pgx.Conn {
	t.Helper()
	appDSN := strings.Replace(ownerDSN, "postgres://hookrail:hookrail@",
		"postgres://"+role+":"+password+"@", 1)
	conn, err := pgx.Connect(context.Background(), appDSN)
	if err != nil {
		t.Fatalf("connect as %s: %v", role, err)
	}
	t.Cleanup(func() { _ = conn.Close(context.Background()) })
	return conn
}

func mustNil(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestAppRoleCanDMLNotDDL(t *testing.T) {
	ctx := context.Background()
	ownerDSN := startDedicatedPG(t)

	st, err := store.Open(ctx, ownerDSN)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	mustNil(t, st.Migrate())

	// Owner sets the app role password so it can log in.
	_, err = st.Pool.Exec(ctx, `ALTER ROLE hookrail_app LOGIN PASSWORD 'testpw'`)
	mustNil(t, err)

	appConn := connectAsRole(t, ownerDSN, "hookrail_app", "testpw")
	defer func() { _ = appConn.Close(ctx) }()

	// DML: INSERT into endpoints (needs id, url, secret_ciphertext).
	_, err = appConn.Exec(ctx,
		`INSERT INTO endpoints (id, url, secret_ciphertext) VALUES ('ep-dml-1', 'https://x.test', '\xdeadbeef')`)
	mustNil(t, err)

	// DDL: CREATE TABLE must fail with permission denied.
	_, err = appConn.Exec(ctx, `CREATE TABLE z(i int)`)
	if err == nil {
		t.Fatal("app role must not DDL — CREATE TABLE should be denied")
	}
	t.Logf("DDL correctly denied: %v", err)
}
