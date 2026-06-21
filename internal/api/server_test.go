//go:build integration

package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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

	"github.com/mit112/hookrail/internal/api"
	"github.com/mit112/hookrail/internal/ratelimit"
	"github.com/mit112/hookrail/internal/store"
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

// nopQueue accepts publishes; failQueue simulates Redis down (§3.1 best-effort).
type nopQueue struct{ published []string }

func (q *nopQueue) Publish(ctx context.Context, id string) error { q.published = append(q.published, id); return nil }
func (q *nopQueue) Ping(ctx context.Context) error               { return nil }

type failQueue struct{}

func (failQueue) Publish(ctx context.Context, id string) error { return errors.New("redis down") }
func (failQueue) Ping(ctx context.Context) error               { return errors.New("redis down") }

func newServer(t *testing.T, q api.Publisher) (*httptest.Server, *store.Store, string) {
	t.Helper()
	s := testStore(t)
	ctx := context.Background()
	_, key, err := s.CreateProducerKey(ctx, "api-test")
	if err != nil {
		t.Fatal(err)
	}
	epID, _, err := s.CreateEndpoint(ctx, masterKey(), "https://consumer.example/hook", "t")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateSubscription(ctx, "orders.*", epID, 8); err != nil {
		t.Fatal(err)
	}
	srv := api.New(s, q, ratelimit.NewRegistry(1000, 1000), 24*time.Hour, 256, 10000)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, s, key
}

func post(t *testing.T, ts *httptest.Server, key, idemKey, body string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/v1/events",
		bytes.NewBufferString(body))
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	if idemKey != "" {
		req.Header.Set("Idempotency-Key", idemKey)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestPostEventAccepted(t *testing.T) {
	q := &nopQueue{}
	ts, _, key := newServer(t, q)
	resp := post(t, ts, key, "", `{"topic":"orders.created","payload":{"n":1}}`)
	//nolint:errcheck
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", resp.StatusCode)
	}
	var body struct {
		EventID     string   `json:"event_id"`
		DeliveryIDs []string `json:"delivery_ids"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.EventID == "" || len(body.DeliveryIDs) != 1 {
		t.Fatalf("body = %+v, want event id + 1 delivery", body)
	}
	if len(q.published) != 1 || q.published[0] != body.DeliveryIDs[0] {
		t.Fatalf("XADD published %v, want the delivery id", q.published)
	}
}

func TestPostEventBadKey401(t *testing.T) {
	ts, _, _ := newServer(t, &nopQueue{})
	resp := post(t, ts, "hk_wrong", "", `{"topic":"orders.created","payload":{}}`)
	//nolint:errcheck
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/problem+json" {
		t.Fatalf("Content-Type = %q, want application/problem+json (§9)", ct)
	}
}

func TestPostEventOversize413(t *testing.T) {
	ts, _, key := newServer(t, &nopQueue{})
	big := fmt.Sprintf(`{"topic":"orders.created","payload":{"blob":%q}}`,
		strings.Repeat("x", 257*1024))
	resp := post(t, ts, key, "", big)
	//nolint:errcheck
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413 (§5: 256KB cap)", resp.StatusCode)
	}
}

func TestPostEventIdempotentReplay(t *testing.T) {
	ts, _, key := newServer(t, &nopQueue{})
	body := `{"topic":"orders.created","payload":{"n":2}}`
	r1 := post(t, ts, key, "api-idem-1", body)
	//nolint:errcheck
	defer r1.Body.Close()
	var b1 struct{ EventID string `json:"event_id"` }
	_ = json.NewDecoder(r1.Body).Decode(&b1)
	r2 := post(t, ts, key, "api-idem-1", body)
	//nolint:errcheck
	defer r2.Body.Close()
	if r2.StatusCode != http.StatusAccepted {
		t.Fatalf("replay status = %d, want 202", r2.StatusCode)
	}
	if r2.Header.Get("Idempotent-Replay") != "true" {
		t.Fatal("replay missing Idempotent-Replay header")
	}
	var b2 struct{ EventID string `json:"event_id"` }
	_ = json.NewDecoder(r2.Body).Decode(&b2)
	if b2.EventID != b1.EventID {
		t.Fatalf("replay returned %s, want %s", b2.EventID, b1.EventID)
	}
}

func TestPostEventIdemConflict409(t *testing.T) {
	ts, _, key := newServer(t, &nopQueue{})
	r1 := post(t, ts, key, "api-idem-2", `{"topic":"orders.created","payload":{"n":3}}`)
	//nolint:errcheck,gosec
	r1.Body.Close()
	r2 := post(t, ts, key, "api-idem-2", `{"topic":"orders.created","payload":{"n":"other"}}`)
	//nolint:errcheck
	defer r2.Body.Close()
	if r2.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (§4)", r2.StatusCode)
	}
}

func TestPostEventRedisDownStill202(t *testing.T) {
	// §3.1: XADD is best-effort; the sweeper repairs. 202 must not depend on Redis.
	ts, _, key := newServer(t, failQueue{})
	resp := post(t, ts, key, "", `{"topic":"orders.created","payload":{"n":4}}`)
	//nolint:errcheck
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d with Redis down, want 202", resp.StatusCode)
	}
}

func TestGetEventStatus(t *testing.T) {
	ts, _, key := newServer(t, &nopQueue{})
	r1 := post(t, ts, key, "", `{"topic":"orders.created","payload":{"n":5}}`)
	var b struct{ EventID string `json:"event_id"` }
	_ = json.NewDecoder(r1.Body).Decode(&b)
	//nolint:errcheck,gosec
	r1.Body.Close()

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/v1/events/"+b.EventID, nil)
	req.Header.Set("Authorization", "Bearer "+key)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	//nolint:errcheck
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var status struct {
		EventID    string `json:"event_id"`
		Deliveries []struct {
			DeliveryID string `json:"delivery_id"`
			State      string `json:"state"`
			Attempts   []any  `json:"attempts"`
		} `json:"deliveries"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		t.Fatal(err)
	}
	if len(status.Deliveries) != 1 || status.Deliveries[0].State != "pending" {
		t.Fatalf("status = %+v, want 1 pending delivery", status)
	}
}

func TestPostEventOrderingKeyTooLong(t *testing.T) {
	ts, _, key := newServer(t, &nopQueue{})
	body := `{"topic":"orders.created","payload":{"n":1}}`
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/v1/events",
		bytes.NewBufferString(body))
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Hookrail-Ordering-Key", strings.Repeat("x", 257)) // > 256
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for long ordering key", resp.StatusCode)
	}
}

func TestHealthz(t *testing.T) {
	ts, _, _ := newServer(t, &nopQueue{})
	resp, err := http.Get(ts.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	//nolint:errcheck
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("healthz = %d, want 200", resp.StatusCode)
	}
}
