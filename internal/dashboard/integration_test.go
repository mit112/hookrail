//go:build integration

package dashboard

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
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

type noQueue struct{}

func (noQueue) Publish(context.Context, string) error { return nil }
func (noQueue) Ping(context.Context) error            { return nil }

// itAdminStack spins a real admin API (httptest) backed by a fresh Postgres DB
// and returns the admin base URL plus the live store.
func itAdminStack(t *testing.T) (string, *store.Store) {
	t.Helper()
	ctx := context.Background()
	pgc, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("hookrail"), tcpostgres.WithUsername("hookrail"), tcpostgres.WithPassword("hookrail"),
		testcontainers.WithWaitStrategy(wait.ForLog("database system is ready to accept connections").
			WithOccurrence(2).WithStartupTimeout(60*time.Second)))
	if err != nil {
		t.Fatalf("pg container: %v", err)
	}
	t.Cleanup(func() { _ = pgc.Terminate(ctx) })
	dsn, err := pgc.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	a, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Exec(ctx, "CREATE DATABASE dash"); err != nil {
		t.Fatal(err)
	}
	_ = a.Close(ctx)
	s, err := store.Open(ctx, strings.Replace(dsn, "/hookrail?", "/dash?", 1))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	adminSrv := admin.New(s, noQueue{}, [32]byte{}, ssrf.Policy{AllowHTTP: true},
		ratelimit.NewRegistry(500, 1000), "env-admin-token", time.Hour)
	ts := httptest.NewServer(adminSrv.Handler())
	t.Cleanup(ts.Close)
	return ts.URL, s
}

func mintToken(t *testing.T, s *store.Store, role string) string {
	t.Helper()
	_, plaintext, err := s.CreateAdminToken(context.Background(), role, role+"-dash")
	if err != nil {
		t.Fatalf("mint %s token: %v", role, err)
	}
	return plaintext
}

func itDashboard(t *testing.T, adminURL string, rt *RoleTokens, usersContent string) *Server {
	t.Helper()
	users, err := ParseUsers(strings.NewReader(usersContent))
	if err != nil {
		t.Fatal(err)
	}
	return NewServer(Config{
		SessionKey:     []byte("0123456789abcdef0123456789abcdef"),
		AdminURL:       adminURL,
		IngressURL:     "http://ingress.invalid",
		Users:          users,
		RoleTokens:     rt,
		SessionTTL:     time.Hour,
		AttestInterval: time.Minute,
		InsecureCookie: true,
	})
}

func itLogin(t *testing.T, srv *Server, user, pw string) *http.Cookie {
	t.Helper()
	r := httptest.NewRequest("POST", "/api/login", strings.NewReader(fmt.Sprintf(`{"username":%q,"password":%q}`, user, pw)))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("login %s: want 200 got %d (%s)", user, w.Code, w.Body.String())
	}
	for _, c := range w.Result().Cookies() {
		if c.Name == srv.cookieName() {
			return c
		}
	}
	t.Fatalf("login %s: no session cookie", user)
	return nil
}

func itReq(t *testing.T, srv *Server, cookie *http.Cookie, method, path, body string) int {
	t.Helper()
	var r *http.Request
	if body != "" {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	} else {
		r = httptest.NewRequest(method, path, nil)
	}
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)
	return w.Code
}

func TestR2EndToEndRoleEnforcement(t *testing.T) {
	adminURL, s := itAdminStack(t)
	rt, err := ParseRoleTokens(strings.NewReader(
		"viewer:" + mintToken(t, s, "viewer") + "\n" +
			"operator:" + mintToken(t, s, "operator") + "\n" +
			"admin:" + mintToken(t, s, "admin") + "\n"))
	if err != nil {
		t.Fatal(err)
	}
	users := "alice:" + mustHash(t, "pw-a") + ":admin\n" +
		"opbob:" + mustHash(t, "pw-o") + ":operator\n" +
		"carol:" + mustHash(t, "pw-c") + ":viewer\n"
	srv := itDashboard(t, adminURL, rt, users)
	if err := srv.InitialAttest(context.Background()); err != nil {
		t.Fatalf("attest against real admin: %v", err)
	}

	carol := itLogin(t, srv, "carol", "pw-c")
	if code := itReq(t, srv, carol, "GET", "/v1/endpoints", ""); code != http.StatusOK {
		t.Errorf("viewer GET endpoints: want 200 got %d", code)
	}
	if code := itReq(t, srv, carol, "POST", "/v1/endpoints", `{"url":"https://example.com/h"}`); code != http.StatusForbidden {
		t.Errorf("viewer POST endpoints: want 403 (local) got %d", code)
	}

	opbob := itLogin(t, srv, "opbob", "pw-o")
	if code := itReq(t, srv, opbob, "POST", "/v1/endpoints", `{"url":"https://example.com/h"}`); code != http.StatusForbidden {
		t.Errorf("operator POST endpoints: want 403 got %d", code)
	}
	if code := itReq(t, srv, opbob, "GET", "/v1/dlq", ""); code != http.StatusOK {
		t.Errorf("operator GET dlq: want 200 got %d", code)
	}

	alice := itLogin(t, srv, "alice", "pw-a")
	if code := itReq(t, srv, alice, "POST", "/v1/endpoints", `{"url":"https://example.com/h"}`); code != http.StatusCreated {
		t.Errorf("admin POST endpoints: want 201 got %d", code)
	}

	// Deleted user: drop alice from the live set; her cookie is rejected next request.
	srv.usersPtr.Store(mustParseUsersIT(t, "opbob:"+mustHash(t, "pw-o")+":operator\n"))
	if code := itReq(t, srv, alice, "GET", "/v1/endpoints", ""); code != http.StatusUnauthorized {
		t.Errorf("deleted admin: want 401 got %d", code)
	}
}

func mustParseUsersIT(t *testing.T, content string) *Users {
	t.Helper()
	u, err := ParseUsers(strings.NewReader(content))
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestR2MislabeledRoleTokenFailsClosed(t *testing.T) {
	adminURL, s := itAdminStack(t)
	adminTok := mintToken(t, s, "admin")
	// viewer line holds an ADMIN token → attestation must reject it.
	rt, err := ParseRoleTokens(strings.NewReader(
		"viewer:" + adminTok + "\n" +
			"operator:" + mintToken(t, s, "operator") + "\n" +
			"admin:" + adminTok + "\n"))
	if err != nil {
		t.Fatal(err)
	}
	srv := itDashboard(t, adminURL, rt, "carol:"+mustHash(t, "pw-c")+":viewer\n")
	if err := srv.InitialAttest(context.Background()); err == nil {
		t.Fatal("attestation must fail for a mislabeled viewer token")
	}
	carol := itLogin(t, srv, "carol", "pw-c")
	if code := itReq(t, srv, carol, "GET", "/v1/endpoints", ""); code != http.StatusServiceUnavailable {
		t.Errorf("unattested proxy: want 503 got %d", code)
	}
}
