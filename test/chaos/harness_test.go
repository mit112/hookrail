package chaos

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
)

const (
	composeFile   = "../../deploy/compose/docker-compose.yml"
	composeProj   = "hookrail"
	defaultMaster = "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f"
)

// ---- Compose -------------------------------------------------------------

type Compose struct{ File, Project string }

func NewCompose() *Compose { return &Compose{File: composeFile, Project: composeProj} }

func (c *Compose) base(args ...string) []string {
	return append([]string{"compose", "-f", c.File, "-p", c.Project}, args...)
}

func (c *Compose) docker(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "docker", args...) //nolint:gosec
	env := os.Environ()
	if os.Getenv("HOOKRAIL_MASTER_KEY") == "" {
		env = append(env, "HOOKRAIL_MASTER_KEY="+defaultMaster)
	}
	if v := os.Getenv("BUILDX_CONFIG"); v != "" {
		env = append(env, "BUILDX_CONFIG="+v)
	}
	cmd.Env = env
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	// NB: capture output AFTER Run() returns. `return out.Bytes(), cmd.Run()`
	// evaluates out.Bytes() first (Go left-to-right return eval) → empty slice.
	err := cmd.Run()
	return out.Bytes(), err
}

func (c *Compose) Up(ctx context.Context, scale map[string]int) error {
	args := []string{"up", "-d", "--build"}
	for svc, count := range scale {
		args = append(args, "--scale", fmt.Sprintf("%s=%d", svc, count))
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
func (c *Compose) Exec(ctx context.Context, svc string, args ...string) ([]byte, error) {
	return c.docker(ctx, c.base(append([]string{"exec", "-T", svc}, args...)...)...)
}
func (c *Compose) Restart(ctx context.Context, svcs ...string) error {
	_, err := c.docker(ctx, c.base(append([]string{"restart"}, svcs...)...)...)
	return err
}
func (c *Compose) PS(ctx context.Context) ([]byte, error) {
	return c.docker(ctx, c.base("ps", "-q")...)
}

func (c *Compose) WaitReady(ctx context.Context, url string) error {
	deadline := time.Now().Add(120 * time.Second)
	for time.Now().Before(deadline) {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if resp, err := http.DefaultClient.Do(req); err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode < 300 {
				return nil
			}
		}
		time.Sleep(time.Second)
	}
	return fmt.Errorf("stack not ready at %s after 120s", url)
}

// ---- Injector ------------------------------------------------------------

type Runner func(ctx context.Context, name string, args ...string) ([]byte, error)

type Injector struct {
	C   *Compose
	Run Runner
}

func NewInjector(c *Compose) *Injector {
	return &Injector{C: c, Run: func(ctx context.Context, name string, args ...string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, name, args...) //nolint:gosec
		env := os.Environ()
		if os.Getenv("HOOKRAIL_MASTER_KEY") == "" {
			env = append(env, "HOOKRAIL_MASTER_KEY="+defaultMaster)
		}
		if v := os.Getenv("BUILDX_CONFIG"); v != "" {
			env = append(env, "BUILDX_CONFIG="+v)
		}
		cmd.Env = env
		var out bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &out
		err := cmd.Run() // capture output AFTER Run() (see Compose.docker)
		return out.Bytes(), err
	}}
}

func (i *Injector) compose(ctx context.Context, sub, svc string) error {
	args := append([]string{"docker"}, i.C.base(sub, svc)...)
	_, err := i.Run(ctx, args[0], args[1:]...)
	return err
}
func (i *Injector) Kill(ctx context.Context, svc string) error  { return i.compose(ctx, "kill", svc) }
func (i *Injector) Pause(ctx context.Context, svc string) error { return i.compose(ctx, "pause", svc) }
func (i *Injector) Unpause(ctx context.Context, svc string) error {
	return i.compose(ctx, "unpause", svc)
}
func (i *Injector) Start(ctx context.Context, svc string) error { return i.compose(ctx, "start", svc) }

// KillLeader finds the scheduler container reporting hookrail_scheduler_is_leader 1
// by scraping /metrics inside each container (port 8083 is NOT exposed on the host),
// then SIGKILLs exactly that container.
func (i *Injector) KillLeader(ctx context.Context) error {
	// Get all scheduler container IDs.
	out, err := i.C.docker(ctx, i.C.base("ps", "-q", "scheduler")...)
	if err != nil {
		return fmt.Errorf("ps scheduler: %w\n%s", err, out)
	}
	ids := strings.Fields(string(out))
	if len(ids) == 0 {
		return fmt.Errorf("no scheduler containers found")
	}
	for _, id := range ids {
		metrics, err := i.C.docker(ctx, "exec", id, "wget", "-qO-", "http://localhost:8083/metrics")
		if err != nil {
			continue // container may be transient; try next
		}
		if strings.Contains(string(metrics), "hookrail_scheduler_is_leader 1") {
			_, err := i.C.docker(ctx, "kill", id)
			if err != nil {
				return fmt.Errorf("kill leader container %s: %w", id, err)
			}
			return nil
		}
	}
	return fmt.Errorf("no scheduler container reporting leader found among %d containers", len(ids))
}

// ---- Load driver ---------------------------------------------------------

func Seed(ctx context.Context, c *Compose, url, topic string) (string, error) {
	out, err := c.Run(ctx, "api", "hookrail-ctl", "seed", "-url", url, "-topic", topic)
	if err != nil {
		return "", fmt.Errorf("seed: %w\n%s", err, out)
	}
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		if v, ok := strings.CutPrefix(strings.TrimSpace(sc.Text()), "producer_key="); ok {
			return v, nil
		}
	}
	return "", fmt.Errorf("no producer_key in seed output:\n%s", out)
}

// SeedOrdered is like Seed but creates an ordered subscription.
func SeedOrdered(ctx context.Context, c *Compose, url, topic string) (string, error) {
	out, err := c.Run(ctx, "api", "hookrail-ctl", "seed", "-url", url, "-topic", topic, "-ordered")
	if err != nil {
		return "", fmt.Errorf("seed ordered: %w\n%s", err, out)
	}
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		if v, ok := strings.CutPrefix(strings.TrimSpace(sc.Text()), "producer_key="); ok {
			return v, nil
		}
	}
	return "", fmt.Errorf("no producer_key in seed output:\n%s", out)
}

type Load struct {
	APIURL, Key, Topic string
	HTTP               *http.Client
}

func (l *Load) client() *http.Client {
	if l.HTTP != nil {
		return l.HTTP
	}
	// Per-request timeout so a paused dependency cannot hang the test (Codex fold #3).
	return &http.Client{Timeout: 3 * time.Second}
}

type ingestResp struct {
	EventID     string   `json:"event_id"`
	DeliveryIDs []string `json:"delivery_ids"`
}

// Post returns ALL delivery IDs for the event (Codex fold #10).
func (l *Load) Post(ctx context.Context) ([]string, error) {
	body := fmt.Sprintf(`{"topic":%q,"payload":{"n":1}}`, l.Topic)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, l.APIURL+"/v1/events", bytes.NewBufferString(body))
	req.Header.Set("Authorization", "Bearer "+l.Key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := l.client().Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusAccepted {
		return nil, fmt.Errorf("ingest status %d", resp.StatusCode)
	}
	var out ingestResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil || len(out.DeliveryIDs) == 0 {
		return nil, fmt.Errorf("bad ingest resp: %w", err)
	}
	return out.DeliveryIDs, nil
}

// PostOrdered sends one event with an ordering key and a sequence payload.
func (l *Load) PostOrdered(ctx context.Context, orderingKey string, seq int) ([]string, error) {
	body := fmt.Sprintf(`{"topic":%q,"payload":{"key":%q,"seq":%d}}`, l.Topic, orderingKey, seq)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, l.APIURL+"/v1/events", bytes.NewBufferString(body))
	req.Header.Set("Authorization", "Bearer "+l.Key)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Hookrail-Ordering-Key", orderingKey)
	resp, err := l.client().Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusAccepted {
		return nil, fmt.Errorf("ingest status %d", resp.StatusCode)
	}
	var out ingestResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil || len(out.DeliveryIDs) == 0 {
		return nil, fmt.Errorf("bad ingest resp: %w", err)
	}
	return out.DeliveryIDs, nil
}

// Burst posts n events; returns the count of accepted DELIVERIES.
func (l *Load) Burst(ctx context.Context, n int) (int, error) {
	deliveries := 0
	for i := 0; i < n; i++ {
		ids, err := l.Post(ctx)
		if err != nil {
			return deliveries, err
		}
		deliveries += len(ids)
	}
	return deliveries, nil
}

// Steady posts at ~rate/s until stop(); stop returns (accepted DELIVERIES, total ATTEMPTS).
// Posts that fail (e.g. a paused dependency) are counted as rejected, not accepted — fail-
// closed ingress. attempts is every post issued (accepted or not); it is the sound upper
// bound on how many events could have committed, used by ingress-fault oracles (E2) where a
// boundary post may time out client-side yet commit server-side on recovery.
func (l *Load) Steady(ctx context.Context, ratePerSec int) func() (int, int) {
	var deliveries, attempts int64
	stopCh := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		tick := time.NewTicker(time.Second / time.Duration(ratePerSec))
		defer tick.Stop()
		for {
			select {
			case <-stopCh:
				return
			case <-tick.C:
				atomic.AddInt64(&attempts, 1)
				if ids, err := l.Post(ctx); err == nil {
					atomic.AddInt64(&deliveries, int64(len(ids)))
				}
			}
		}
	}()
	return func() (int, int) {
		close(stopCh)
		wg.Wait()
		return int(atomic.LoadInt64(&deliveries)), int(atomic.LoadInt64(&attempts))
	}
}

// ---- Oracle --------------------------------------------------------------

type Stats struct {
	Receipts   int `json:"receipts"`
	Distinct   int `json:"distinct_deliveries"`
	Duplicates int `json:"duplicates"`
}

type DBState struct{ Total, Succeeded, Pending, InFlight, RetryScheduled, DeadLettered, Cancelled int }

func (d DBState) NonTerminal() int { return d.Pending + d.InFlight + d.RetryScheduled }

type Snapshot struct {
	Stats Stats
	DB    DBState
	// Succeeded must land in [ExpectedMin, ExpectedMax]. For a delivery-path fault (E1/E3)
	// Min==Max==accepted (exact). For an ingress-path fault (E2: paused Postgres) Max admits
	// the posts in-flight at the boundary that commit on recovery beyond the client-confirmed
	// accepted count.
	ExpectedMin int
	ExpectedMax int
	DupBound    int
}

// Recovered: everything drained (nothing non-terminal / unexpectedly dead-lettered /
// cancelled), the count of succeeded deliveries sits within [ExpectedMin, ExpectedMax], the
// receiver saw exactly the succeeded set (no loss, no phantom), and duplicates are within the
// mechanism-derived bound (Codex folds #1,#7,#10). distinct==succeeded (not ==expected) so the
// bounded-Max E2 case stays honest: every succeeded delivery was observed exactly once.
func (s Snapshot) Recovered() bool {
	return s.DB.NonTerminal() == 0 &&
		s.DB.DeadLettered == 0 && s.DB.Cancelled == 0 &&
		s.DB.Succeeded >= s.ExpectedMin && s.DB.Succeeded <= s.ExpectedMax &&
		s.Stats.Distinct == s.DB.Succeeded &&
		s.Stats.Duplicates <= s.DupBound
}

func FetchStats(ctx context.Context, recvURL string) (Stats, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, recvURL+"/stats", nil)
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		return Stats{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	var s Stats
	return s, json.NewDecoder(resp.Body).Decode(&s)
}

func FetchDB(ctx context.Context, dsn string) (DBState, error) {
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return DBState{}, err
	}
	defer func() { _ = conn.Close(ctx) }()
	var d DBState
	err = conn.QueryRow(ctx, `
		SELECT count(*),
		       count(*) FILTER (WHERE state='succeeded'),
		       count(*) FILTER (WHERE state='pending'),
		       count(*) FILTER (WHERE state='in_flight'),
		       count(*) FILTER (WHERE state='retry_scheduled'),
		       count(*) FILTER (WHERE state='dead_lettered'),
		       count(*) FILTER (WHERE state='cancelled')
		FROM deliveries`).
		Scan(&d.Total, &d.Succeeded, &d.Pending, &d.InFlight, &d.RetryScheduled, &d.DeadLettered, &d.Cancelled)
	return d, err
}

// WaitNonTerminal polls until at least `min` deliveries are non-terminal (proves a fault
// has live work to disrupt) or the deadline; fails loud otherwise (Codex folds #2,#6).
func WaitNonTerminal(ctx context.Context, dsn string, min int, deadline time.Duration) (DBState, error) {
	var last DBState
	until := time.Now().Add(deadline)
	for time.Now().Before(until) {
		db, err := FetchDB(ctx, dsn)
		if err == nil {
			last = db
			if db.NonTerminal() >= min {
				return db, nil
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	return last, fmt.Errorf("non-terminal never reached %d within %s (last=%d)", min, deadline, last.NonTerminal())
}

// WaitInFlight polls until at least `min` deliveries are in_flight — proving a delivery-path
// fault has a claimed-but-unacked delivery to disrupt. For ordered keys at-most-one-in-flight
// per key means NonTerminal is dominated by pending backlog, so in_flight must be asserted
// specifically (else the kill only proves drain-after-restart, not in-flight recovery).
func WaitInFlight(ctx context.Context, dsn string, min int, deadline time.Duration) (DBState, error) {
	var last DBState
	until := time.Now().Add(deadline)
	for time.Now().Before(until) {
		db, err := FetchDB(ctx, dsn)
		if err == nil {
			last = db
			if db.InFlight >= min {
				return db, nil
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	return last, fmt.Errorf("in_flight never reached %d within %s (last in_flight=%d, non_terminal=%d)", min, deadline, last.InFlight, last.NonTerminal())
}

// PollRecovered asserts an EXACT succeeded count — every accepted delivery, and only those,
// must succeed. Delivery-path faults (E1 worker-crash, E3 redis loss) use this: all accepted
// posts committed to PG before the fault, so succeeded must equal accepted exactly.
func PollRecovered(ctx context.Context, recvURL, dsn string, expected, dupBound int, deadline time.Duration) (Snapshot, error) {
	return PollRecoveredBounded(ctx, recvURL, dsn, expected, expected, dupBound, deadline)
}

// PollRecoveredBounded asserts succeeded lands in [expectedMin, expectedMax]. An ingress-path
// fault (E2: paused Postgres) lets posts in-flight at the pause boundary commit on unpause and
// deliver exactly once, so succeeded can exceed the client-confirmed accepted count — bounded
// above by the number of posts ever attempted (you cannot deliver an event never posted).
func PollRecoveredBounded(ctx context.Context, recvURL, dsn string, expectedMin, expectedMax, dupBound int, deadline time.Duration) (Snapshot, error) {
	var last Snapshot
	until := time.Now().Add(deadline)
	for time.Now().Before(until) {
		st, e1 := FetchStats(ctx, recvURL)
		db, e2 := FetchDB(ctx, dsn)
		if e1 == nil && e2 == nil {
			last = Snapshot{Stats: st, DB: db, ExpectedMin: expectedMin, ExpectedMax: expectedMax, DupBound: dupBound}
			if last.Recovered() {
				return last, nil
			}
		}
		time.Sleep(time.Second)
	}
	return last, fmt.Errorf("not recovered within %s: nonTerminal=%d succeeded=%d want[%d,%d] distinct=%d dups=%d (bound %d) deadletter=%d cancelled=%d",
		deadline, last.DB.NonTerminal(), last.DB.Succeeded, expectedMin, expectedMax, last.Stats.Distinct, last.Stats.Duplicates, dupBound, last.DB.DeadLettered, last.DB.Cancelled)
}

// ---- Prometheus metric reader (proves observable fault effect) -----------

func FetchCounter(ctx context.Context, promURL, query string) (float64, error) {
	u := promURL + "/api/v1/query?query=" + query
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

// WaitCounterAbove polls until the counter exceeds base (proves the fault produced the
// expected signal) or the deadline; fails loud otherwise.
func WaitCounterAbove(ctx context.Context, promURL, query string, base float64, deadline time.Duration) (float64, error) {
	until := time.Now().Add(deadline)
	var last float64
	for time.Now().Before(until) {
		v, err := FetchCounter(ctx, promURL, query)
		if err == nil {
			last = v
			if v > base {
				return v, nil
			}
		}
		time.Sleep(time.Second)
	}
	return last, fmt.Errorf("counter %q never rose above %v within %s (last=%v)", query, base, deadline, last)
}
