//go:build k8schaos

package k8schaos

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"testing"
	"time"
)

// floor of accepted events required per liveness bucket (pre + post). Makes
// admitted=0 FAIL (spec §6.1 fact 3 / M2-ratelimit vacuity lesson).
const acceptFloor = 50

// TestExperimentPGFailover kills the CNPG primary mid-load and asserts the
// four-fact / three-bucket oracle (spec §6.1):
//  1. failover actually happened (new primary UID, -rw flipped, old pod rejoins replica)
//  2. RPO=0: every 202-acked delivery succeeds; receiver-distinct == DB succeeded
//  3. liveness: load crossed the no-primary window AND post-failover accepts succeed
//  4. mechanism-derived dup bound; no app crashloop across the failover
func TestExperimentPGFailover(t *testing.T) {
	ctx := context.Background()
	key := mustEnv(t, "HOOKRAIL_PRODUCER_KEY")
	adminTok := mustEnv(t, "HOOKRAIL_ADMIN_TOKEN")

	// --- precondition gate: a vacuous proof on a non-HA cluster is worthless ---
	if ri := readyInstances(t); ri != 3 {
		t.Fatalf("precondition: readyInstances=%d, want 3 (1 primary + 2 standby)", ri)
	}
	if !failoverQuorumHealthy(t) {
		t.Fatal("precondition: FailoverQuorum absent or not reporting ANY-quorum sync — safety gate inactive, proof would be vacuous")
	}

	// --- host-access channels (spec §6 MAJOR #7) ---
	stopAPI := portForward(t, "api", 18080, 8080)
	defer stopAPI()
	stopAdmin := portForward(t, "admin", 18082, 8082)
	defer stopAdmin()
	stopRecv := portForward(t, "test-receiver", 19090, 9090)
	defer stopRecv()
	// DB reconciliation uses `kubectl exec` into the live primary (see fetchDB) —
	// NOT a -rw port-forward, which would die when we kill the primary.

	apiURL := "http://127.0.0.1:18080"
	adminURL := "http://127.0.0.1:18082"
	recvURL := "http://127.0.0.1:19090"

	// endpoint -> in-cluster receiver; subscription on e2e.*
	epID := adminPost(t, adminURL, adminTok, "/v1/endpoints",
		`{"url":"http://test-receiver:9090/succeed"}`)
	adminPost(t, adminURL, adminTok, "/v1/subscriptions",
		fmt.Sprintf(`{"endpoint_id":%q,"topic_pattern":"e2e.*"}`, epID))

	load := &Load{APIURL: apiURL, Key: key, Topic: "e2e.test"}

	// --- bucket A: pre-kill load ---
	load.setPhase(phasePre)
	stopLoad := load.start(ctx, 25)
	waitAccepted(t, load, phasePre, acceptFloor, 60*time.Second)

	oldName, oldUID := primaryPod(t)
	const appSel = "app in (api,worker,scheduler,admin)" // all DB-owning daemons (Codex MAJOR #4)
	preRestarts := podRestartCounts(t, appSel)
	dbAtKill, _ := fetchDB()
	inFlightAtKill := dbAtKill.InFlight

	// --- bucket B: kill primary, load keeps flowing through the no-primary window ---
	load.setPhase(phaseNoPrimary)
	t.Logf("force-deleting primary %s (uid %s)", oldName, oldUID)
	// --force --grace-period=0: abrupt removal simulates a real primary failure so
	// CNPG promotes a replica instead of fast-restarting the same instance in place.
	if out, err := exec.Command("kubectl", "-n", ns, "delete", "pod", oldName,
		"--force", "--grace-period=0", "--wait=false").CombinedOutput(); err != nil {
		t.Fatalf("delete primary: %v\n%s", err, out)
	}

	// poll for a NEW primary pod (different name) — the core "failover happened" signal.
	start := time.Now()
	var newName, newUID string
	for time.Since(start) < 180*time.Second {
		n, u := primaryPodTry()
		if n != "" && n != oldName {
			newName, newUID = n, u
			break
		}
		if int(time.Since(start).Seconds())%5 == 0 {
			t.Logf("waiting failover t=%s: primary=%q rw=%q", time.Since(start).Round(time.Second), n, rwEndpointPodTry())
		}
		time.Sleep(time.Second)
	}
	if newName == "" {
		t.Fatalf("no new primary promoted within 180s (old=%s); pods:\n%s", oldName,
			kubectlOut(t, "-n", ns, "get", "pod", "-l", "cnpg.io/cluster=hookrail-pg",
				"-o", "custom-columns=NAME:.metadata.name,ROLE:.metadata.labels.cnpg\\.io/instanceRole,PHASE:.status.phase"))
	}
	rto := time.Since(start)
	t.Logf("failover: %s -> %s in ~%s", oldName, newName, rto.Round(time.Second))

	// the -rw Service must flip to the new primary (assert separately, with its own budget)
	rwFlipped := false
	for i := 0; i < 60; i++ {
		if rwEndpointPodTry() == newName {
			rwFlipped = true
			break
		}
		time.Sleep(time.Second)
	}
	if !rwFlipped {
		t.Fatalf("fact1: -rw endpoint never flipped to new primary %s (got %q)", newName, rwEndpointPodTry())
	}
	// no split-brain: exactly one pod claims the primary role (Codex MINOR #7)
	if np := countPrimaries(t); np != 1 {
		t.Fatalf("fact1: %d primaries after failover, want exactly 1 (split-brain)", np)
	}

	// --- bucket C: post-promotion load must also succeed (liveness, not just old durable) ---
	load.setPhase(phasePost)
	waitAccepted(t, load, phasePost, acceptFloor, 90*time.Second)
	stopLoad()

	// ===================== ASSERTIONS =====================

	// Fact 1: failover actually happened (new primary UID; -rw flip already asserted above).
	if newUID == oldUID {
		t.Fatalf("fact1: primary UID unchanged (%s) — no real promotion", oldUID)
	}
	// old pod rejoins as a replica (CNPG post-failover behavior)
	rejoined := false
	for i := 0; i < 60; i++ {
		if role, ok := podRole(oldName); ok && role == "replica" {
			rejoined = true
			break
		}
		time.Sleep(2 * time.Second)
	}
	if !rejoined {
		t.Fatalf("fact1: old primary %s did not rejoin as replica", oldName)
	}

	// Fact 3a: load genuinely crossed the no-primary window.
	if att, _ := load.snap(phaseNoPrimary); att == 0 {
		t.Fatal("fact3: no POST attempts during the no-primary window — load did not cross the outage")
	}
	// Fact 3b: non-empty accepted sets pre AND post (admitted=0 must FAIL).
	_, preAcc := load.snap(phasePre)
	_, postAcc := load.snap(phasePost)
	if preAcc < acceptFloor || postAcc < acceptFloor {
		t.Fatalf("fact3: accepted pre=%d post=%d, want >=%d each (liveness)", preAcc, postAcc, acceptFloor)
	}

	// Totals across all three buckets.
	totalAccepted, totalAttempts := 0, 0
	for _, p := range []phase{phasePre, phaseNoPrimary, phasePost} {
		a, acc := load.snap(p)
		totalAttempts += a
		totalAccepted += acc
	}

	// Fact 2 + 4: RPO=0 + exact reconciliation + mechanism-derived dup bound.
	// expectedMin = totalAccepted: every 202-acked delivery must succeed (no loss).
	// expectedMax = totalAttempts: a boundary post may time out client-side yet
	// commit server-side on recovery; you cannot deliver an event never posted.
	// dupBound: in-flight at kill (each may be redelivered once on failover) + slack.
	dupBound := inFlightAtKill + totalAccepted/10 + 10
	snap, err := pollRecovered(recvURL, totalAccepted, totalAttempts, dupBound, 150*time.Second)
	if err != nil {
		t.Fatalf("fact2/4 (aggregate drain): %v", err)
	}
	t.Logf("reconciled(aggregate): succeeded=%d distinct=%d dups=%d accepted=%d attempts=%d dupBound=%d",
		snap.DB.Succeeded, snap.Stats.Distinct, snap.Stats.Duplicates, totalAccepted, totalAttempts, dupBound)

	// Fact 2 (RPO=0, IDENTITY-level — Codex BLOCKER): every accepted delivery_id
	// must exist as a row AND be 'succeeded'. A lost accept makes found<len; a
	// count-only check could mask it with a timed-out-but-committed delivery.
	acceptedIDs := load.acceptedIDsAll()
	if len(acceptedIDs) != totalAccepted {
		t.Fatalf("fact2: tracked accepted ids=%d != counted accepted=%d", len(acceptedIDs), totalAccepted)
	}
	succ, found, err := succeededAmong(acceptedIDs)
	if err != nil {
		t.Fatalf("fact2: per-id reconcile: %v", err)
	}
	if found != len(acceptedIDs) {
		t.Fatalf("fact2: DATA LOSS — only %d of %d accepted deliveries exist as rows after failover", found, len(acceptedIDs))
	}
	if succ != len(acceptedIDs) {
		t.Fatalf("fact2: only %d of %d accepted deliveries reached 'succeeded'", succ, len(acceptedIDs))
	}
	// Fact 2 (identity, receiver side — Codex MAJOR #2): each accepted delivery_id
	// was actually received (>=1 2xx) end-to-end, not just marked succeeded.
	missing := 0
	for _, id := range acceptedIDs {
		if n, e := receivedCount(recvURL, id); e != nil || n < 1 {
			missing++
		}
	}
	if missing != 0 {
		t.Fatalf("fact2: %d of %d accepted deliveries not confirmed received by the endpoint", missing, len(acceptedIDs))
	}
	t.Logf("reconciled(per-id): all %d accepted deliveries succeeded + received", len(acceptedIDs))

	// Fact 4b: no app CRASHLOOP and no pod REPLACEMENT across the failover (ties to
	// M1 startup-retry). A single in-place restart that recovers via OpenWithRetry
	// is the feature working; a crashloop is repeated restarts; a vanished pod name
	// means it was killed/rescheduled (not expected for a PG failover) — Codex MAJOR #4.
	postRestarts := podRestartCounts(t, appSel)
	for pod, pre := range preRestarts {
		post, ok := postRestarts[pod]
		if !ok {
			t.Fatalf("fact4: app pod %s vanished during failover (replaced, not restarted-in-place)", pod)
		}
		if post > pre+1 {
			t.Fatalf("fact4: pod %s crashlooped during failover (%d->%d) — startup not failover-resilient", pod, pre, post)
		}
	}
}

// ---- experiment helpers ---------------------------------------------------

func mustEnv(t *testing.T, k string) string {
	t.Helper()
	v := envOr(k, "")
	if v == "" {
		t.Fatalf("required env %s not set (see scripts/pg-failover-e2e.sh)", k)
	}
	return v
}

func primaryPodTry() (name, uid string) {
	// range-form jsonpath returns "" (not an error) when no primary exists during
	// the promotion window — a bare {.items[0]...} errors "array index out of bounds"
	// and that error text must NOT be mistaken for a pod name.
	out, err := kubectlTry("-n", ns, "get", "pod",
		"-l", "cnpg.io/cluster=hookrail-pg,cnpg.io/instanceRole=primary",
		"-o", "jsonpath={range .items[*]}{.metadata.name}{end}")
	if err != nil || out == "" {
		return "", ""
	}
	name = out
	uid, _ = kubectlTry("-n", ns, "get", "pod", name, "-o", "jsonpath={.metadata.uid}")
	return name, uid
}

func rwEndpointPodTry() string {
	out, _ := kubectlTry("-n", ns, "get", "endpoints", "hookrail-pg-rw",
		"-o", "jsonpath={.subsets[*].addresses[*].targetRef.name}")
	return out
}

func waitAccepted(t *testing.T, l *Load, p phase, floor int, deadline time.Duration) {
	t.Helper()
	until := time.Now().Add(deadline)
	for time.Now().Before(until) {
		if _, acc := l.snap(p); acc >= floor {
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	_, acc := l.snap(p)
	t.Fatalf("phase %d: accepted=%d never reached floor %d within %s", p, acc, floor, deadline)
}

func adminPost(t *testing.T, adminURL, tok, path, body string) string {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, adminURL+path, bytes.NewBufferString(body))
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("admin POST %s: %v", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		t.Fatalf("admin POST %s: status %d", path, resp.StatusCode)
	}
	var out struct {
		ID string `json:"id"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return out.ID
}

// pollRecovered drains to terminal and asserts: nothing non-terminal/dead/cancelled,
// succeeded in [expectedMin, expectedMax], receiver-distinct == DB succeeded (exact),
// and duplicates <= dupBound. A NEGATIVE dupBound disables the duplicate ceiling
// entirely (duplicates become a logged-only diagnostic) — used by the Redis-failover
// oracle, where async Sentinel replication admits no tight, mechanism-derived dup
// bound (spec §6.3 / Codex M3 MINOR-4). The PG-failover oracle passes a positive,
// mechanism-derived bound and is unaffected. Adapted from harness_test.go.
func pollRecovered(recvURL string, expectedMin, expectedMax, dupBound int, deadline time.Duration) (struct {
	DB    DBState
	Stats Stats
}, error) {
	ctx := context.Background()
	type result = struct {
		DB    DBState
		Stats Stats
	}
	var last result
	until := time.Now().Add(deadline)
	for time.Now().Before(until) {
		st, e1 := fetchStats(ctx, recvURL)
		db, e2 := fetchDB()
		if e1 == nil && e2 == nil {
			last = result{DB: db, Stats: st}
			dupOK := dupBound < 0 || st.Duplicates <= dupBound
			if db.NonTerminal() == 0 && db.DeadLettered == 0 && db.Cancelled == 0 &&
				db.Succeeded >= expectedMin && db.Succeeded <= expectedMax &&
				st.Distinct == db.Succeeded && dupOK {
				return last, nil
			}
		}
		time.Sleep(time.Second)
	}
	return last, fmt.Errorf("not recovered within %s: nonTerminal=%d succeeded=%d want[%d,%d] distinct=%d dups=%d (bound %d; <0=unbounded) deadletter=%d cancelled=%d",
		deadline, last.DB.NonTerminal(), last.DB.Succeeded, expectedMin, expectedMax, last.Stats.Distinct, last.Stats.Duplicates, dupBound, last.DB.DeadLettered, last.DB.Cancelled)
}
