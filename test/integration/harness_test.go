//go:build integration

package integration

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

const (
	composeFile   = "../../deploy/compose/docker-compose.yml"
	composeProj   = "hookrail"
	defaultMaster = "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f"
	dsn           = "postgres://hookrail:hookrail@localhost:5432/hookrail?sslmode=disable" //nolint:gosec // test-only, Docker compose default creds
	promURL       = "http://localhost:9091"
	apiURL        = "http://localhost:8080"
)

// ---- Options ----------------------------------------------------------------

type stackOpts struct {
	workerScale int
	env         map[string]string // extra env vars passed to compose
}

type StackOption func(*stackOpts)

func WithWorkerScale(n int) StackOption { return func(o *stackOpts) { o.workerScale = n } }
func WithEnv(k, v string) StackOption   { return func(o *stackOpts) { o.env[k] = v } }

// ---- Compose wrapper --------------------------------------------------------

type Compose struct {
	File, Project string
	opts          stackOpts
}

func (c *Compose) base(args ...string) []string {
	return append([]string{"compose", "-f", c.File, "-p", c.Project}, args...)
}

func (c *Compose) docker(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "docker", args...) //nolint:gosec
	env := os.Environ()
	if os.Getenv("HOOKRAIL_MASTER_KEY") == "" {
		env = append(env, "HOOKRAIL_MASTER_KEY="+defaultMaster)
	}
	// Inject extra env vars from options into the docker process.
	for k, v := range c.opts.env {
		env = append(env, k+"="+v)
	}
	if v := os.Getenv("BUILDX_CONFIG"); v != "" {
		env = append(env, "BUILDX_CONFIG="+v)
	}
	cmd.Env = env
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	return out.Bytes(), err
}

func (c *Compose) Up(ctx context.Context) error {
	args := []string{"up", "-d", "--build"}
	if c.opts.workerScale > 1 {
		args = append(args, "--scale", fmt.Sprintf("worker=%d", c.opts.workerScale))
	}
	if b, err := c.docker(ctx, c.base(args...)...); err != nil {
		return fmt.Errorf("compose up: %w\n%s", err, b)
	}
	return nil
}

func (c *Compose) Down(ctx context.Context) error {
	_, err := c.docker(ctx, c.base("down", "-v")...)
	return err
}

func (c *Compose) Run(ctx context.Context, svc string, args ...string) ([]byte, error) {
	return c.docker(ctx, c.base(append([]string{"run", "--rm", svc}, args...)...)...)
}

// ---- Seed result ------------------------------------------------------------

type SeedResult struct {
	ProducerKey    string
	EndpointID     string
	EndpointSecret string
	SubscriptionID string
}

// SeedWithRPS runs `hookrail-ctl seed --rps <rps>` and returns all IDs.
func SeedWithRPS(ctx context.Context, c *Compose, rps float64) (SeedResult, error) {
	args := []string{"seed", "-url", "http://test-receiver:9090/succeed", "-topic", "rl-test.*"}
	if rps > 0 {
		args = append(args, "-rps", fmt.Sprintf("%.2f", rps))
	}
	out, err := c.Run(ctx, "api", append([]string{"hookrail-ctl"}, args...)...)
	if err != nil {
		return SeedResult{}, fmt.Errorf("seed: %w\n%s", err, out)
	}
	var sr SeedResult
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		switch {
		case strings.HasPrefix(line, "producer_key="):
			sr.ProducerKey = strings.TrimPrefix(line, "producer_key=")
		case strings.HasPrefix(line, "endpoint_id="):
			sr.EndpointID = strings.TrimPrefix(line, "endpoint_id=")
		case strings.HasPrefix(line, "endpoint_secret="):
			sr.EndpointSecret = strings.TrimPrefix(line, "endpoint_secret=")
		case strings.HasPrefix(line, "subscription_id="):
			sr.SubscriptionID = strings.TrimPrefix(line, "subscription_id=")
		}
	}
	if sr.EndpointID == "" {
		return SeedResult{}, fmt.Errorf("no endpoint_id in seed output:\n%s", out)
	}
	return sr, nil
}

// ---- Load driver ------------------------------------------------------------

type Load struct {
	APIURL, Key, Topic string
}

func (l *Load) Post(ctx context.Context) error {
	body := fmt.Sprintf(`{"topic":%q,"payload":{"n":1}}`, l.Topic)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, l.APIURL+"/v1/events", bytes.NewBufferString(body))
	req.Header.Set("Authorization", "Bearer "+l.Key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 3 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusAccepted {
		return fmt.Errorf("ingest status %d", resp.StatusCode)
	}
	return nil
}

// Burst posts n events; returns the count of HTTP 202 accepted.
func (l *Load) Burst(ctx context.Context, n int) int {
	accepted := 0
	for i := 0; i < n; i++ {
		if err := l.Post(ctx); err == nil {
			accepted++
		}
	}
	return accepted
}

// StartSaturation launches n goroutines that POST continuously until ctx is
// cancelled. This keeps demand far above any per-endpoint cap for the whole
// measurement, so the window is always saturated — the cap is genuinely
// exercised and the admitted-count window can never be empty (the trailing
// fixed-window approach went vacuous precisely because demand had drained).
func (l *Load) StartSaturation(ctx context.Context, n int) {
	for i := 0; i < n; i++ {
		go func() {
			for ctx.Err() == nil {
				_ = l.Post(ctx)
			}
		}()
	}
}

// ---- PG helpers -------------------------------------------------------------

// DBNow returns the database clock. Both window bounds are taken from the DB so
// the window is measured in the same clock domain that writes requested_at —
// no host/container clock skew can shift the window.
func DBNow(ctx context.Context) (time.Time, error) {
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return time.Time{}, err
	}
	defer func() { _ = conn.Close(ctx) }()
	var t time.Time
	err = conn.QueryRow(ctx, `SELECT now()`).Scan(&t)
	return t, err
}

// CountAttemptsBetween counts delivery_attempts for an endpoint whose
// requested_at falls in [t0, t1). The window is anchored to DB-clock instants
// captured around a saturated steady-state interval (not a trailing wall-clock
// guess), so the measurement reflects the actual admit rate during the window.
func CountAttemptsBetween(ctx context.Context, endpointID string, t0, t1 time.Time) (int, error) {
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return 0, err
	}
	defer func() { _ = conn.Close(ctx) }()
	var count int
	err = conn.QueryRow(ctx,
		`SELECT count(*) FROM delivery_attempts da
		 JOIN deliveries d ON d.id = da.delivery_id
		 WHERE d.endpoint_id=$1
		   AND da.requested_at >= $2 AND da.requested_at < $3`,
		endpointID, t0, t1).Scan(&count)
	return count, err
}

// ---- Prometheus helpers -----------------------------------------------------

type RatelimitMetrics struct {
	DeniedGlobal int // result=denied,mode=global  (summed over ALL worker replicas)
	DeniedLocal  int // result=denied,mode=local
	FailOpen     int // mode=failopen (allowed+denied)
}

// ScrapeRatelimit reads ratelimit decision counters summed across every worker
// replica. fetchGauge sums all series the query returns, and prometheus.yml
// discovers each worker via DNS A-records, so a decision on ANY worker is
// counted — single-target scraping previously hid the other worker's
// failopen/denied decisions and made the oracle vacuous.
func ScrapeRatelimit(ctx context.Context) (RatelimitMetrics, error) {
	var m RatelimitMetrics
	var err error
	if m.DeniedGlobal, err = scrapeSum(ctx, `hookrail_ratelimit_decisions_total{result="denied",mode="global"}`); err != nil {
		return m, err
	}
	if m.DeniedLocal, err = scrapeSum(ctx, `hookrail_ratelimit_decisions_total{result="denied",mode="local"}`); err != nil {
		return m, err
	}
	// failopen series may be absent in a healthy run → treat absence as 0.
	m.FailOpen, _ = scrapeSum(ctx, `hookrail_ratelimit_decisions_total{mode="failopen"}`)
	return m, nil
}

func scrapeSum(ctx context.Context, query string) (int, error) {
	v, err := fetchGauge(ctx, promURL, query)
	return int(v), err
}

// WaitWorkerTargets blocks until Prometheus reports at least n UP worker
// targets, so the oracle's cross-worker sums (and especially failopen==0) cover
// every replica before any assertion runs.
func WaitWorkerTargets(ctx context.Context, n int, deadline time.Duration) error {
	end := time.Now().Add(deadline)
	for time.Now().Before(end) {
		if v, err := fetchGauge(ctx, promURL, `up{job="hookrail-worker"}`); err == nil && int(v) >= n {
			return nil
		}
		time.Sleep(time.Second)
	}
	return fmt.Errorf("prometheus did not discover %d UP worker targets within %s", n, deadline)
}

// WaitDenials blocks until at least one denied decision in the given mode has
// been observed, proving the cap is genuinely active+binding before measuring.
func WaitDenials(ctx context.Context, mode string, deadline time.Duration) (int, error) {
	q := fmt.Sprintf(`hookrail_ratelimit_decisions_total{result="denied",mode=%q}`, mode)
	end := time.Now().Add(deadline)
	for time.Now().Before(end) {
		if v, err := fetchGauge(ctx, promURL, q); err == nil && int(v) > 0 {
			return int(v), nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return 0, fmt.Errorf("no denied decisions in mode=%s within %s", mode, deadline)
}

func fetchGauge(ctx context.Context, baseURL, query string) (float64, error) {
	u := baseURL + "/api/v1/query?query=" + url.QueryEscape(query)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	var out struct {
		Data struct {
			Result []struct {
				Value [2]any `json:"value"`
			} `json:"result"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return 0, err
	}
	total := 0.0
	for _, r := range out.Data.Result {
		if s, ok := r.Value[1].(string); ok {
			if f, err := strconv.ParseFloat(s, 64); err == nil {
				total += f
			}
		}
	}
	return total, nil
}

// ---- Setup / Teardown -------------------------------------------------------

// SetupStack brings up the compose stack with the given options and returns a
// teardown function. It fails the test on any error.
func SetupStack(t *testing.T, opts ...StackOption) *Compose {
	t.Helper()
	c := &Compose{
		File:    composeFile,
		Project: composeProj,
		opts:    stackOpts{env: map[string]string{}},
	}
	for _, o := range opts {
		o(&c.opts)
	}
	ctx := context.Background()
	if err := c.Up(ctx); err != nil {
		t.Fatalf("setup stack: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = c.Down(ctx)
	})
	// Wait for API readiness.
	if err := c.waitReady(ctx); err != nil {
		t.Fatalf("stack ready: %v", err)
	}
	return c
}

func (c *Compose) waitReady(ctx context.Context) error {
	deadline := time.Now().Add(120 * time.Second)
	for time.Now().Before(deadline) {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, apiURL+"/readyz", nil)
		if resp, err := http.DefaultClient.Do(req); err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode < 300 {
				return nil
			}
		}
		time.Sleep(time.Second)
	}
	return fmt.Errorf("stack not ready after 120s")
}
