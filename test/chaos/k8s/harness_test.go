//go:build k8schaos

// Package k8schaos drives the CNPG Postgres-failover chaos experiment against a
// live k3d cluster (see scripts/pg-failover-e2e.sh). It uses plain `kubectl`
// (no client-go, no `kubectl cnpg` plugin) for k8s state + host-side
// port-forwards for the API/admin/receiver/DB channels. The oracle is the
// three-bucket / four-fact design in
// docs/superpowers/specs/2026-06-23-hookrail-p2-datastore-ha-design.md §6.1.
package k8schaos

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const ns = "hookrail"

// ---- kubectl helpers (plain kubectl, parse -o jsonpath) -------------------

func kubectlOut(t *testing.T, args ...string) string {
	t.Helper()
	out, err := exec.Command("kubectl", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("kubectl %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func kubectlTry(args ...string) (string, error) {
	// stdout only: kubectl writes warnings (e.g. "v1 Endpoints is deprecated")
	// to stderr, which must NOT pollute parsed jsonpath/psql values.
	out, err := exec.Command("kubectl", args...).Output()
	return strings.TrimSpace(string(out)), err
}

// primaryPod returns the current CNPG primary pod name + UID.
func primaryPod(t *testing.T) (name, uid string) {
	t.Helper()
	name = kubectlOut(t, "-n", ns, "get", "pod",
		"-l", "cnpg.io/cluster=hookrail-pg,cnpg.io/instanceRole=primary",
		"-o", "jsonpath={.items[0].metadata.name}")
	if name == "" {
		t.Fatal("no CNPG primary pod found")
	}
	uid = kubectlOut(t, "-n", ns, "get", "pod", name, "-o", "jsonpath={.metadata.uid}")
	return name, uid
}

// rwEndpointPod returns the pod name backing the hookrail-pg-rw Service (the
// pod the app actually connects to). After a failover this must flip.
func rwEndpointPod(t *testing.T) string {
	t.Helper()
	return kubectlOut(t, "-n", ns, "get", "endpoints", "hookrail-pg-rw",
		"-o", "jsonpath={.subsets[*].addresses[*].targetRef.name}")
}

// podExistsWithRole reports whether a pod exists and (optionally) carries the
// given cnpg instanceRole.
func podRole(name string) (string, bool) {
	out, err := kubectlTry("-n", ns, "get", "pod", name,
		"-o", "jsonpath={.metadata.labels.cnpg\\.io/instanceRole}")
	if err != nil {
		return "", false
	}
	return out, true
}

// podRestartCounts sums container restartCount per pod for a label selector.
func podRestartCounts(t *testing.T, selector string) map[string]int {
	t.Helper()
	raw := kubectlOut(t, "-n", ns, "get", "pod", "-l", selector,
		"-o", "jsonpath={range .items[*]}{.metadata.name}{\"=\"}{range .status.containerStatuses[*]}{.restartCount}{\"+\"}{end}{\"\\n\"}{end}")
	counts := map[string]int{}
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !strings.Contains(line, "=") {
			continue
		}
		kv := strings.SplitN(line, "=", 2)
		sum := 0
		for _, n := range strings.Split(strings.Trim(kv[1], "+"), "+") {
			if n == "" {
				continue
			}
			var v int
			_, _ = fmt.Sscanf(n, "%d", &v)
			sum += v
		}
		counts[kv[0]] = sum
	}
	return counts
}

func readyInstances(t *testing.T) int {
	t.Helper()
	out := kubectlOut(t, "-n", ns, "get", "cluster", "hookrail-pg",
		"-o", "jsonpath={.status.readyInstances}")
	var v int
	_, _ = fmt.Sscanf(out, "%d", &v)
	return v
}

// failoverQuorumActive asserts the FailoverQuorum safety gate is present — a
// failover proof on a cluster without it would be vacuous (spec §6.1 precond).
func failoverQuorumActive(t *testing.T) bool {
	t.Helper()
	out, err := kubectlTry("-n", ns, "get", "failoverquorum.postgresql.cnpg.io", "-o", "name")
	return err == nil && strings.TrimSpace(out) != ""
}

// portForward starts `kubectl port-forward` to a Service and returns a stop func.
func portForward(t *testing.T, svc string, local, remote int) func() {
	t.Helper()
	cmd := exec.Command("kubectl", "-n", ns, "port-forward",
		"svc/"+svc, fmt.Sprintf("%d:%d", local, remote))
	if err := cmd.Start(); err != nil {
		t.Fatalf("port-forward %s: %v", svc, err)
	}
	// give the forward a moment to establish
	time.Sleep(3 * time.Second)
	return func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() }
}

// ---- three-bucket load driver --------------------------------------------

type phase int32

const (
	phasePre phase = iota
	phaseNoPrimary
	phasePost
)

type bucket struct{ attempts, accepted int64 }

// Load is a continuous saturator that POSTs events and tags each attempt +
// accepted-delivery count into the bucket for the current phase, so the oracle
// can prove load actually crossed the no-primary window (spec §6.1 BLOCKER #2).
type Load struct {
	APIURL string
	Key    string
	Topic  string
	ph     int32
	b      [3]bucket
}

type ingestResp struct {
	EventID     string   `json:"event_id"`
	DeliveryIDs []string `json:"delivery_ids"`
}

func (l *Load) setPhase(p phase) { atomic.StoreInt32(&l.ph, int32(p)) }
func (l *Load) cur() *bucket     { return &l.b[atomic.LoadInt32(&l.ph)] }

func (l *Load) post(ctx context.Context) {
	b := l.cur()
	atomic.AddInt64(&b.attempts, 1)
	body := fmt.Sprintf(`{"topic":%q,"payload":{"t":%d}}`, l.Topic, time.Now().UnixNano())
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, l.APIURL+"/v1/events", bytes.NewBufferString(body))
	req.Header.Set("Authorization", "Bearer "+l.Key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 4 * time.Second}).Do(req)
	if err != nil {
		return
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusAccepted {
		return
	}
	var out ingestResp
	if json.NewDecoder(resp.Body).Decode(&out) == nil && len(out.DeliveryIDs) > 0 {
		atomic.AddInt64(&b.accepted, int64(len(out.DeliveryIDs)))
	}
}

// start launches the saturator at ~rate/s; returns a stop func.
func (l *Load) start(ctx context.Context, ratePerSec int) func() {
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		tick := time.NewTicker(time.Second / time.Duration(ratePerSec))
		defer tick.Stop()
		for {
			select {
			case <-stop:
				return
			case <-tick.C:
				l.post(ctx)
			}
		}
	}()
	return func() { close(stop); wg.Wait() }
}

func (l *Load) snap(p phase) (attempts, accepted int) {
	b := &l.b[p]
	return int(atomic.LoadInt64(&b.attempts)), int(atomic.LoadInt64(&b.accepted))
}

// ---- oracle (adapted from test/chaos/harness_test.go) ---------------------

type Stats struct {
	Receipts   int `json:"receipts"`
	Distinct   int `json:"distinct_deliveries"`
	Duplicates int `json:"duplicates"`
}

type DBState struct{ Total, Succeeded, Pending, InFlight, RetryScheduled, DeadLettered, Cancelled int }

func (d DBState) NonTerminal() int { return d.Pending + d.InFlight + d.RetryScheduled }

func fetchStats(ctx context.Context, recvURL string) (Stats, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, recvURL+"/stats", nil)
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		return Stats{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	var s Stats
	return s, json.NewDecoder(resp.Body).Decode(&s)
}

// fetchDB queries the deliveries state counts by exec-ing psql in the CURRENT
// primary pod (resolved fresh each call). A host port-forward to the -rw service
// would die when we kill the primary it bound to; exec re-resolves the live
// primary so reconciliation survives the failover.
func fetchDB() (DBState, error) {
	primary, err := kubectlTry("-n", ns, "get", "pod",
		"-l", "cnpg.io/cluster=hookrail-pg,cnpg.io/instanceRole=primary",
		"-o", "jsonpath={.items[0].metadata.name}")
	if err != nil || primary == "" {
		return DBState{}, fmt.Errorf("no primary for DB query: %v", err)
	}
	const q = `SELECT count(*),count(*) FILTER (WHERE state='succeeded'),` +
		`count(*) FILTER (WHERE state='pending'),count(*) FILTER (WHERE state='in_flight'),` +
		`count(*) FILTER (WHERE state='retry_scheduled'),count(*) FILTER (WHERE state='dead_lettered'),` +
		`count(*) FILTER (WHERE state='cancelled') FROM deliveries`
	out, err := kubectlTry("-n", ns, "exec", primary, "-c", "postgres", "--",
		"psql", "-U", "postgres", "-d", "hookrail", "-tAF,", "-c", q)
	if err != nil {
		return DBState{}, fmt.Errorf("psql exec: %v (%s)", err, out)
	}
	// take the single comma-separated data line (ignore any NOTICE/warning lines)
	var line string
	for _, l := range strings.Split(out, "\n") {
		if strings.Count(l, ",") == 6 {
			line = strings.TrimSpace(l)
			break
		}
	}
	parts := strings.Split(line, ",")
	if len(parts) != 7 {
		return DBState{}, fmt.Errorf("unexpected psql output: %q", out)
	}
	d := DBState{}
	fields := []*int{&d.Total, &d.Succeeded, &d.Pending, &d.InFlight, &d.RetryScheduled, &d.DeadLettered, &d.Cancelled}
	for i, p := range parts {
		if _, e := fmt.Sscanf(strings.TrimSpace(p), "%d", fields[i]); e != nil {
			return DBState{}, fmt.Errorf("parse field %d %q: %v", i, p, e)
		}
	}
	return d, nil
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
