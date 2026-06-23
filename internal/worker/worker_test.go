//go:build integration

package worker_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/mit112/hookrail/internal/backoff"
	"github.com/mit112/hookrail/internal/ratelimit"
	"github.com/mit112/hookrail/internal/signing"
	"github.com/mit112/hookrail/internal/ssrf"
	"github.com/mit112/hookrail/internal/store"
	"github.com/mit112/hookrail/internal/worker"
)

var (
	once     sync.Once
	adminDSN string
	dbSeq    atomic.Int64
)

// one shared container, one fresh DATABASE per test: container cost is paid
// once, but tests stay isolated and order-independent
func testStore(t *testing.T) *store.Store {
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
	admin, err := pgx.Connect(ctx, adminDSN)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	_ = admin.Close(ctx)
	s, err := store.Open(ctx, strings.Replace(adminDSN, "/hookrail?", "/"+name+"?", 1))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s
}

func masterKey() [32]byte { var k [32]byte; copy(k[:], "0123456789abcdef0123456789abcdef"); return k }

// seed creates key+endpoint+subscription pointing at url, ingests one event,
// returns (delivery id, endpoint secret).
func seed(t *testing.T, s *store.Store, url string) (string, string) {
	t.Helper()
	ctx := context.Background()
	keyID, _, err := s.CreateProducerKey(ctx, "worker-test", []string{"*"})
	if err != nil {
		t.Fatal(err)
	}
	epID, secret, err := s.CreateEndpoint(ctx, masterKey(), url, "t")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateSubscription(ctx, "wk.*", epID, 8); err != nil {
		t.Fatal(err)
	}
	res, err := s.IngestEvent(ctx, store.IngestParams{
		ProducerKeyID: keyID, Topic: "wk.test", Payload: []byte(`{"hello":"world"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	return res.DeliveryIDs[0], secret
}

// devPolicy allows http + loopback so httptest servers are dialable; this is
// exactly the documented self-host dev mode (§8).
func devPolicy() ssrf.Policy {
	return ssrf.Policy{AllowHTTP: true, AllowCIDRs: mustPrefixes("127.0.0.0/8", "::1/128")}
}

func newWorker(s *store.Store) *worker.Worker {
	return &worker.Worker{
		Store: s, MasterKey: masterKey(),
		Client: ssrf.NewHTTPClient(devPolicy()), Policy: devPolicy(),
		Backoff: backoff.Default(), Limits: ratelimit.NewRegistry(1000, 1000),
		Lease: 30 * time.Second,
	}
}

func state(t *testing.T, s *store.Store, id string) string {
	t.Helper()
	var st string
	if err := s.Pool.QueryRow(context.Background(),
		`SELECT state::text FROM deliveries WHERE id=$1`, id).Scan(&st); err != nil {
		t.Fatal(err)
	}
	return st
}

func TestProcessSuccess(t *testing.T) {
	s := testStore(t)
	var gotSig, gotDeliveryID string
	var gotBody []byte
	recv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSig = r.Header.Get("hookrail-signature")
		gotDeliveryID = r.Header.Get("hookrail-delivery-id")
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(200)
	}))
	defer recv.Close()
	id, secret := seed(t, s, recv.URL)

	newWorker(s).Process(context.Background(), id)

	if got := state(t, s, id); got != "succeeded" {
		t.Fatalf("state = %s, want succeeded", got)
	}
	// receiver-side verification proves the §8 signature contract end to end
	if err := signing.Verify([][]byte{[]byte(secret)}, gotSig, gotDeliveryID, gotBody,
		time.Now(), 5*time.Minute); err != nil {
		t.Fatalf("receiver could not verify signature: %v", err)
	}
}

func TestProcess5xxSchedulesRetry(t *testing.T) {
	s := testStore(t)
	recv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(503)
	}))
	defer recv.Close()
	id, _ := seed(t, s, recv.URL)
	newWorker(s).Process(context.Background(), id)
	if got := state(t, s, id); got != "retry_scheduled" {
		t.Fatalf("state = %s, want retry_scheduled", got)
	}
}

func TestProcess404DeadLetters(t *testing.T) {
	s := testStore(t)
	recv := httptest.NewServer(http.NotFoundHandler())
	defer recv.Close()
	id, _ := seed(t, s, recv.URL)
	newWorker(s).Process(context.Background(), id)
	if got := state(t, s, id); got != "dead_lettered" {
		t.Fatalf("state = %s, want dead_lettered", got)
	}
}

func TestProcessRedirectRejected(t *testing.T) {
	s := testStore(t)
	recv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://127.0.0.1:9/elsewhere", http.StatusFound)
	}))
	defer recv.Close()
	id, _ := seed(t, s, recv.URL)
	newWorker(s).Process(context.Background(), id)
	if got := state(t, s, id); got != "dead_lettered" {
		t.Fatalf("state = %s, want dead_lettered (redirects never followed, §7)", got)
	}
	var ec string
	if err := s.Pool.QueryRow(context.Background(),
		`SELECT error_class FROM delivery_attempts WHERE delivery_id=$1`, id).Scan(&ec); err != nil || ec != "redirect_rejected" {
		t.Fatalf("error_class = %q (err %v), want redirect_rejected", ec, err)
	}
}

func TestProcessSSRFBlockedURL(t *testing.T) {
	s := testStore(t)
	// metadata endpoint sneaks into the endpoints table (e.g. DNS changed
	// after registration) — the at-dial check must still kill it (§8)
	id, _ := seed(t, s, "http://169.254.169.254/latest/meta-data/")
	w := newWorker(s)
	w.Policy = ssrf.Policy{AllowHTTP: true} // no loopback allowlist here
	w.Client = ssrf.NewHTTPClient(w.Policy)
	w.Process(context.Background(), id)
	if got := state(t, s, id); got != "dead_lettered" {
		t.Fatalf("state = %s, want dead_lettered with policy class", got)
	}
	var ec string
	if err := s.Pool.QueryRow(context.Background(),
		`SELECT error_class FROM delivery_attempts WHERE delivery_id=$1`, id).Scan(&ec); err != nil || ec != "policy" {
		t.Fatalf("error_class = %q (err %v), want policy", ec, err)
	}
}

func TestProcessAlreadyClaimedDrops(t *testing.T) {
	s := testStore(t)
	recv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("POST happened for an already-claimed delivery")
	}))
	defer recv.Close()
	id, _ := seed(t, s, recv.URL)
	if ok, _, _ := s.ClaimDelivery(context.Background(), id, time.Minute); !ok {
		t.Fatal("pre-claim failed")
	}
	newWorker(s).Process(context.Background(), id) // must lose the CAS and drop
	if got := state(t, s, id); got != "in_flight" {
		t.Fatalf("state = %s, want untouched in_flight", got)
	}
}

func TestProcessExhaustedBudgetDeadLetters(t *testing.T) {
	s := testStore(t)
	recv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("no POST should happen once the budget is exhausted")
	}))
	defer recv.Close()
	id, _ := seed(t, s, recv.URL)
	// a hard-crash loop consumed the whole budget via claims (no completions)
	if _, err := s.Pool.Exec(context.Background(),
		`UPDATE deliveries SET attempt_count = 8 WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	newWorker(s).Process(context.Background(), id)
	if got := state(t, s, id); got != "dead_lettered" {
		t.Fatalf("state = %s, want dead_lettered (attempts_exhausted)", got)
	}
	// the attempt-history contract: no HTTP request happened in this claim,
	// so NO attempt row is written and the bookkeeping claim is un-counted
	var attempts, count int
	if err := s.Pool.QueryRow(context.Background(),
		`SELECT count(*) FROM delivery_attempts WHERE delivery_id=$1`, id).Scan(&attempts); err != nil || attempts != 0 {
		t.Fatalf("exhaustion wrote %d attempt rows (err %v), want 0", attempts, err)
	}
	if err := s.Pool.QueryRow(context.Background(),
		`SELECT attempt_count FROM deliveries WHERE id=$1`, id).Scan(&count); err != nil || count != 8 {
		t.Fatalf("attempt_count = %d after exhaustion (err %v), want 8", count, err)
	}
}

func TestProcessRateLimitedReleasesWithoutBurningAttempt(t *testing.T) {
	s := testStore(t)
	recv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("no POST should happen when rate limited")
	}))
	defer recv.Close()
	id, _ := seed(t, s, recv.URL)
	w := newWorker(s)
	w.Limits = ratelimit.NewRegistry(0.0001, 0) // bucket starts empty → deny
	w.Process(context.Background(), id)
	var count int
	var st string
	if err := s.Pool.QueryRow(context.Background(),
		`SELECT attempt_count, state::text FROM deliveries WHERE id=$1`, id).Scan(&count, &st); err != nil {
		t.Fatal(err)
	}
	if count != 0 || st != "retry_scheduled" {
		t.Fatalf("after rate-limited release: count=%d state=%s, want 0/retry_scheduled", count, st)
	}
	var attempts int
	if err := s.Pool.QueryRow(context.Background(),
		`SELECT count(*) FROM delivery_attempts WHERE delivery_id=$1`, id).Scan(&attempts); err != nil || attempts != 0 {
		t.Fatalf("rate limit recorded %d attempts, want 0", attempts)
	}
}

// fakeLimiter always returns (allowed, err) as configured.
type fakeLimiter struct {
	allowed bool
	err     error
}

func (f *fakeLimiter) Allow(_ context.Context, _ string, _, _ float64, _ time.Time) (bool, error) {
	return f.allowed, f.err
}

func TestAllowDelivery_GlobalDenyReleasesNoAttempt(t *testing.T) {
	s := testStore(t)
	recv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("no POST should happen when globally rate limited")
	}))
	defer recv.Close()
	id, _ := seed(t, s, recv.URL)

	// Get the actual endpoint ID from the delivery row
	var epID string
	if err := s.Pool.QueryRow(context.Background(),
		`SELECT endpoint_id FROM deliveries WHERE id=$1`, id).Scan(&epID); err != nil {
		t.Fatal(err)
	}

	w := newWorker(s)
	// Build a GlobalLimiter that Has the endpoint but denies it
	fb := ratelimit.NewRegistry(1000, 2000)
	g := ratelimit.NewGlobalLimiterWith(&fakeLimiter{allowed: false}, fb, 50*time.Millisecond)
	// Put the real endpoint ID in the snapshot so Has returns true
	g.SetSnapshot(map[string]ratelimit.Limit{epID: {Rate: 1, Burst: 2}})
	w.Global = g

	w.Process(context.Background(), id)

	var count int
	var st string
	if err := s.Pool.QueryRow(context.Background(),
		`SELECT attempt_count, state::text FROM deliveries WHERE id=$1`, id).Scan(&count, &st); err != nil {
		t.Fatal(err)
	}
	if count != 0 || st != "retry_scheduled" {
		t.Fatalf("after global rate-limited release: count=%d state=%s, want 0/retry_scheduled", count, st)
	}
	var attempts int
	if err := s.Pool.QueryRow(context.Background(),
		`SELECT count(*) FROM delivery_attempts WHERE delivery_id=$1`, id).Scan(&attempts); err != nil || attempts != 0 {
		t.Fatalf("global rate limit recorded %d attempts, want 0", attempts)
	}
}

func TestAllowDelivery_DefaultEndpointUsesLocalRegistry(t *testing.T) {
	s := testStore(t)
	recv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("no POST should happen when locally rate limited")
	}))
	defer recv.Close()
	id, _ := seed(t, s, recv.URL)

	w := newWorker(s)
	w.Limits = ratelimit.NewRegistry(0.0001, 0) // local bucket starts empty → deny on Allow
	// Global exists but endpoint is NOT in its snapshot → Has returns false → routes to local
	g := ratelimit.NewGlobalLimiterWith(&fakeLimiter{allowed: true}, ratelimit.NewRegistry(1000, 2000), 50*time.Millisecond)
	w.Global = g

	w.Process(context.Background(), id)

	var count int
	var st string
	if err := s.Pool.QueryRow(context.Background(),
		`SELECT attempt_count, state::text FROM deliveries WHERE id=$1`, id).Scan(&count, &st); err != nil {
		t.Fatal(err)
	}
	if count != 0 || st != "retry_scheduled" {
		t.Fatalf("local default endpoint should be rate-limited: count=%d state=%s, want 0/retry_scheduled", count, st)
	}
}

func mustPrefixes(ps ...string) []netip.Prefix {
	out := make([]netip.Prefix, len(ps))
	for i, p := range ps {
		out[i] = netip.MustParsePrefix(p)
	}
	return out
}
