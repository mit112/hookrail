//go:build k8schaos

package k8schaos

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Redis pods of the StatefulSet (master + 1 replica). Sticky StatefulSet identity:
// a rescheduled redis-0 keeps the name redis-0 (and its PVC), so a CHANGE OF THE
// MASTER ORDINAL is the load-bearing non-vacuity signal — impossible without
// Sentinel + a live replica (spec §6.2, MINOR-5).
var redisPods = []string{"redis-0", "redis-1"}

const (
	redisStream = "hookrail:deliveries"
	redisGroup  = "deliverers"
)

// phaseNoMaster names the middle load bucket for the Redis experiments (the
// no-master / group-lost window). It aliases the shared phaseNoPrimary ordinal.
const phaseNoMaster = phaseNoPrimary

// ---- redis/sentinel exec helpers (kubectl exec into a LIVE pod, NOT a
// port-forward that dies when the pod we kill backs it — spec §6.5) ----------

// redisExecTry runs redis-cli inside a redis pod's `redis` container. stdout-only
// (Output) so kubectl stderr warnings never pollute parsed values.
func redisExecTry(pod string, args ...string) (string, error) {
	full := append([]string{"-n", ns, "exec", pod, "-c", "redis", "--", "redis-cli"}, args...)
	return kubectlTry(full...)
}

// sentinelExecTry runs redis-cli -p 26379 inside a sentinel pod's `sentinel` container.
func sentinelExecTry(pod string, args ...string) (string, error) {
	full := append([]string{"-n", ns, "exec", pod, "-c", "sentinel", "--", "redis-cli", "-p", "26379"}, args...)
	return kubectlTry(full...)
}

// aliveSentinelPod returns the first Running sentinel pod name.
func aliveSentinelPod(t *testing.T) string {
	t.Helper()
	out := kubectlOut(t, "-n", ns, "get", "pod", "-l", "app=redis-sentinel",
		"--field-selector=status.phase=Running",
		"-o", "jsonpath={range .items[*]}{.metadata.name}{\"\\n\"}{end}")
	for _, l := range strings.Split(out, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			return l
		}
	}
	t.Fatal("no Running sentinel pod found")
	return ""
}

// readySentinels counts Ready sentinel pods.
func readySentinels(t *testing.T) int {
	t.Helper()
	out := kubectlOut(t, "-n", ns, "get", "pod", "-l", "app=redis-sentinel",
		"-o", "jsonpath={range .items[*]}{range .status.conditions[?(@.type=='Ready')]}{.status}{end}{\"\\n\"}{end}")
	n := 0
	for _, l := range strings.Split(out, "\n") {
		if strings.TrimSpace(l) == "True" {
			n++
		}
	}
	return n
}

// sentinelDiscoveredReplicas returns how many replicas the Sentinel QUORUM has
// discovered for the master (`SENTINEL replicas hookrail` prints one entry per
// replica; each entry includes a `name` field). 0 means Sentinel has not converged on
// a promotable standby — entering the oracle then would be vacuous, not HA (Codex M3
// MAJOR-3). This is Sentinel's own view, distinct from the master-side INFO check.
func sentinelDiscoveredReplicas(t *testing.T) int {
	t.Helper()
	s := aliveSentinelPod(t)
	out, err := sentinelExecTry(s, "sentinel", "replicas", "hookrail")
	if err != nil {
		return 0
	}
	n := 0
	for _, l := range strings.Split(out, "\n") {
		if strings.TrimSpace(l) == "name" {
			n++
		}
	}
	return n
}

// redisRoleTry returns a redis pod's self-reported role ("master"/"slave") or "".
func redisRoleTry(pod string) string {
	out, err := redisExecTry(pod, "role")
	if err != nil || out == "" {
		return ""
	}
	// ROLE returns a multi-line array; the first line is the role string.
	return strings.TrimSpace(strings.SplitN(out, "\n", 2)[0])
}

// masterByRoleTry returns the single redis pod self-reporting `master`, or "" if
// none/ambiguous (the promotion window may transiently show 0 or 2). A REPLICA
// never self-promotes — only Sentinel issues REPLICAOF NO ONE — so a former
// replica reporting `master` is itself proof Sentinel acted.
func masterByRoleTry() string {
	got := ""
	n := 0
	for _, p := range redisPods {
		if redisRoleTry(p) == "master" {
			got = p
			n++
		}
	}
	if n != 1 {
		return ""
	}
	return got
}

// sentinelMasterPodTry asks the Sentinel quorum for the current master and maps the
// announced address to a redis pod NAME (announce-hostnames -> FQDN; fall back to
// matching a pod IP). Used to cross-check that SENTINEL (not just redis) promoted.
func sentinelMasterPodTry(t *testing.T) string {
	t.Helper()
	s := aliveSentinelPod(t)
	out, err := sentinelExecTry(s, "sentinel", "get-master-addr-by-name", "hookrail")
	if err != nil || out == "" {
		return ""
	}
	host := strings.TrimSpace(strings.SplitN(out, "\n", 2)[0])
	host = strings.Trim(host, "\"")
	if host == "" {
		return ""
	}
	// hostname form: redis-0.redis-headless... -> first DNS label is the pod name.
	if label := strings.SplitN(host, ".", 2)[0]; strings.HasPrefix(label, "redis-") {
		return label
	}
	// IP form: match against redis pod IPs.
	for _, p := range redisPods {
		ip, _ := kubectlTry("-n", ns, "get", "pod", p, "-o", "jsonpath={.status.podIP}")
		if strings.TrimSpace(ip) == host {
			return p
		}
	}
	return ""
}

// replicaOnlineBoundedLag asserts the master sees >=1 replica state=online with lag
// <= maxLagSeconds (spec §6.1 MINOR-5: master-link-status=ok alone does not bound
// staleness; a stale standby weakens the failover-window claim).
func replicaOnlineBoundedLag(master string, maxLagSeconds int) (bool, string) {
	out, err := redisExecTry(master, "info", "replication")
	if err != nil {
		return false, fmt.Sprintf("info replication: %v", err)
	}
	cs := 0
	online := false
	lagOK := false
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if v, ok := strings.CutPrefix(line, "connected_slaves:"); ok {
			cs, _ = strconv.Atoi(strings.TrimSpace(v))
		}
		if strings.HasPrefix(line, "slave") && strings.Contains(line, "state=online") {
			online = true
			for _, tok := range strings.Split(line, ",") {
				if v, ok := strings.CutPrefix(strings.TrimSpace(tok), "lag="); ok {
					if lag, e := strconv.Atoi(strings.TrimSpace(v)); e == nil && lag <= maxLagSeconds {
						lagOK = true
					}
				}
			}
		}
	}
	if cs < 1 || !online || !lagOK {
		return false, fmt.Sprintf("connected_slaves=%d online=%v lagOK=%v\n%s", cs, online, lagOK, out)
	}
	return true, ""
}

// TestExperimentRedisFailover kills the Redis master pod mid-load and asserts the
// non-vacuous Sentinel-failover oracle (spec §6):
//
//	§6.1 precondition gate (>=3 sentinels, master+online-replica with bounded lag)
//	§6.2 a DIFFERENT-ORDINAL pod is promoted to master (impossible without Sentinel+
//	     a live replica), sentinel agrees, old pod rejoins as replica
//	§6.3 liveness (load crossed the no-master window; pre+post accepts >= floor) +
//	     per-delivery-id RPO=0 (a PG+sweeper property) + convergence
//	     (nonTerminal==0 ∧ distinct==succeeded ∧ no deadletter/cancelled). The dup
//	     count is logged-only, NOT a tight ceiling (async Sentinel replication).
func TestExperimentRedisFailover(t *testing.T) {
	ctx := context.Background()
	key := mustEnv(t, "HOOKRAIL_PRODUCER_KEY")
	adminTok := mustEnv(t, "HOOKRAIL_ADMIN_TOKEN")

	// --- §6.1 precondition gate (a vacuous proof on a non-HA Redis is worthless) ---
	if rs := readySentinels(t); rs < 3 {
		t.Fatalf("precondition: readySentinels=%d, want >=3 (quorum)", rs)
	}
	master0 := masterByRoleTry()
	if master0 == "" {
		t.Fatal("precondition: no single redis master self-reports `master`")
	}
	if sm := sentinelMasterPodTry(t); sm != master0 {
		t.Fatalf("precondition: sentinel master=%q != redis self-reported master=%q (quorum disagrees)", sm, master0)
	}
	if ok, why := replicaOnlineBoundedLag(master0, 5); !ok {
		t.Fatalf("precondition: no online replica with bounded lag — proof would be vacuous: %s", why)
	}
	if r := sentinelDiscoveredReplicas(t); r < 1 {
		t.Fatalf("precondition: Sentinel has discovered %d replicas, want >=1 (no promotable standby -> vacuous)", r)
	}

	// --- host-access channels (port-forwards to app services we DON'T kill) ---
	stopAPI := portForward(t, "api", 18080, 8080)
	defer stopAPI()
	stopAdmin := portForward(t, "admin", 18082, 8082)
	defer stopAdmin()
	stopRecv := portForward(t, "test-receiver", 19090, 9090)
	defer stopRecv()

	apiURL := "http://127.0.0.1:18080"
	adminURL := "http://127.0.0.1:18082"
	recvURL := "http://127.0.0.1:19090"

	epID := adminPost(t, adminURL, adminTok, "/v1/endpoints",
		`{"url":"http://test-receiver:9090/succeed"}`)
	adminPost(t, adminURL, adminTok, "/v1/subscriptions",
		fmt.Sprintf(`{"endpoint_id":%q,"topic_pattern":"e2e.*"}`, epID))

	// Baseline the DB before our load: k8schaos tests share one cluster/DB, so the
	// global succeeded count includes any prior test's (terminal) deliveries. Assert
	// our contribution as a delta (per-id reconciliation below is already isolated).
	baseDB, _ := fetchDB()
	load := &Load{APIURL: apiURL, Key: key, Topic: "e2e.test"}

	// --- bucket A: pre-kill load ---
	load.setPhase(phasePre)
	stopLoad := load.start(ctx, 25)
	waitAccepted(t, load, phasePre, acceptFloor, 60*time.Second)

	const appSel = "app in (api,worker,scheduler,admin)"
	preRestarts := podRestartCounts(t, appSel)

	// --- bucket B: kill the master; load keeps flowing through the no-master window ---
	load.setPhase(phaseNoMaster)
	t.Logf("force-deleting redis master %s", master0)
	out := kubectlOut(t, "-n", ns, "delete", "pod", master0, "--force", "--grace-period=0", "--wait=false")
	t.Logf("delete: %s", out)

	// §6.2: a DIFFERENT ordinal must be promoted to master within the RTO budget.
	start := time.Now()
	newMaster := ""
	for time.Since(start) < 120*time.Second {
		if m := masterByRoleTry(); m != "" && m != master0 {
			newMaster = m
			break
		}
		time.Sleep(time.Second)
	}
	if newMaster == "" {
		t.Fatalf("no different-ordinal master promoted within 120s (old=%s) — Sentinel did not fail over (or topology is non-HA)", master0)
	}
	t.Logf("failover: master %s -> %s in ~%s", master0, newMaster, time.Since(start).Round(time.Second))

	// Sentinel must AGREE on the new master (it drove the promotion, not just redis).
	sentAgree := false
	for i := 0; i < 60; i++ {
		if sentinelMasterPodTry(t) == newMaster {
			sentAgree = true
			break
		}
		time.Sleep(time.Second)
	}
	if !sentAgree {
		t.Fatalf("§6.2: sentinel never reported the new master %s (got %q)", newMaster, sentinelMasterPodTry(t))
	}

	// --- bucket C: post-promotion load must also be delivered (liveness) ---
	load.setPhase(phasePost)
	waitAccepted(t, load, phasePost, acceptFloor, 120*time.Second)
	stopLoad()

	// ===================== ASSERTIONS =====================

	// §6.2: the killed pod, once rescheduled, rejoins as a REPLICA of the new master
	// (Sentinel reconfigured topology — not just k8s restarting a box).
	rejoined := false
	for i := 0; i < 90; i++ {
		if redisRoleTry(master0) == "slave" {
			rejoined = true
			break
		}
		time.Sleep(2 * time.Second)
	}
	if !rejoined {
		t.Fatalf("§6.2: old master %s did not rejoin as a replica of %s", master0, newMaster)
	}

	// §6.3 liveness: load genuinely crossed the no-master window.
	if att, _ := load.snap(phaseNoMaster); att == 0 {
		t.Fatal("§6.3: no POST attempts during the no-master window — load did not cross the outage")
	}
	_, preAcc := load.snap(phasePre)
	_, postAcc := load.snap(phasePost)
	if preAcc < acceptFloor || postAcc < acceptFloor {
		t.Fatalf("§6.3: accepted pre=%d post=%d, want >=%d each (admitted=0 must fail)", preAcc, postAcc, acceptFloor)
	}

	totalAccepted, totalAttempts := 0, 0
	for _, p := range []phase{phasePre, phaseNoMaster, phasePost} {
		a, acc := load.snap(p)
		totalAttempts += a
		totalAccepted += acc
	}

	// §6.3 convergence (BLOCKER-1): nonTerminal==0 ∧ distinct==succeeded ∧ no
	// deadletter/cancelled, succeeded ∈ [accepted, attempts]. The dup ceiling is
	// loose (== attempts) because under async Sentinel replication no tight,
	// mechanism-derived dup bound exists — the duplicate count is logged only.
	snap, err := pollRecovered(recvURL, baseDB.Succeeded+totalAccepted, baseDB.Succeeded+totalAttempts, -1 /* dup ceiling disabled: logged-only */, 180*time.Second)
	if err != nil {
		t.Fatalf("§6.3 convergence: %v", err)
	}
	t.Logf("reconciled(aggregate): succeeded=%d distinct=%d dups(logged)=%d accepted=%d attempts=%d",
		snap.DB.Succeeded, snap.Stats.Distinct, snap.Stats.Duplicates, totalAccepted, totalAttempts)

	// §6.3 RPO=0 per-delivery-id (a PG+sweeper property, NOT a Redis property): every
	// 202-acked delivery_id exists as a row, is `succeeded`, and was received >=1×.
	acceptedIDs := load.acceptedIDsAll()
	if len(acceptedIDs) != totalAccepted {
		t.Fatalf("§6.3: tracked accepted ids=%d != counted accepted=%d", len(acceptedIDs), totalAccepted)
	}
	succ, found, err := succeededAmong(acceptedIDs)
	if err != nil {
		t.Fatalf("§6.3 per-id reconcile: %v", err)
	}
	if found != len(acceptedIDs) {
		t.Fatalf("§6.3: DATA LOSS — only %d of %d accepted deliveries exist as rows", found, len(acceptedIDs))
	}
	if succ != len(acceptedIDs) {
		t.Fatalf("§6.3: only %d of %d accepted deliveries reached 'succeeded'", succ, len(acceptedIDs))
	}
	missing := 0
	for _, id := range acceptedIDs {
		if n, e := receivedCount(recvURL, id); e != nil || n < 1 {
			missing++
		}
	}
	if missing != 0 {
		t.Fatalf("§6.3: %d of %d accepted deliveries not confirmed received end-to-end", missing, len(acceptedIDs))
	}
	t.Logf("reconciled(per-id): all %d accepted deliveries succeeded + received across the failover", len(acceptedIDs))

	// §6.3: no app crashloop / no pod REPLACEMENT — clients reconnect via FailoverClient,
	// they don't restart.
	postRestarts := podRestartCounts(t, appSel)
	for pod, pre := range preRestarts {
		post, ok := postRestarts[pod]
		if !ok {
			t.Fatalf("§6.3: app pod %s vanished during failover (replaced, not reconnect-in-place)", pod)
		}
		if post > pre+1 {
			t.Fatalf("§6.3: app pod %s crashlooped during failover (%d->%d)", pod, pre, post)
		}
	}
}

// TestExperimentRedisNoGroupRecovery proves the MAJOR-3 fix (spec §6.6): when the
// consumer group is absent on the master (the failover analog — a freshly-promoted
// master that never replicated the XGROUP CREATE), the worker read loop hits NOGROUP,
// re-runs EnsureGroup, and drains to terminal WITHOUT a pod restart. We induce the
// missing group deterministically via XGROUP DESTROY on the live master (the next
// XREADGROUP returns the identical NOGROUP error the promoted-master case produces),
// which makes the test reliable on CI ×3 rather than racing replication timing.
func TestExperimentRedisNoGroupRecovery(t *testing.T) {
	ctx := context.Background()
	key := mustEnv(t, "HOOKRAIL_PRODUCER_KEY")
	adminTok := mustEnv(t, "HOOKRAIL_ADMIN_TOKEN")

	if rs := readySentinels(t); rs < 3 {
		t.Fatalf("precondition: readySentinels=%d, want >=3", rs)
	}
	master := masterByRoleTry()
	if master == "" {
		t.Fatal("precondition: no single redis master")
	}
	if r := sentinelDiscoveredReplicas(t); r < 1 {
		t.Fatalf("precondition: Sentinel has discovered %d replicas, want >=1", r)
	}

	stopAPI := portForward(t, "api", 18080, 8080)
	defer stopAPI()
	stopAdmin := portForward(t, "admin", 18082, 8082)
	defer stopAdmin()
	stopRecv := portForward(t, "test-receiver", 19090, 9090)
	defer stopRecv()

	apiURL := "http://127.0.0.1:18080"
	adminURL := "http://127.0.0.1:18082"
	recvURL := "http://127.0.0.1:19090"

	epID := adminPost(t, adminURL, adminTok, "/v1/endpoints",
		`{"url":"http://test-receiver:9090/succeed"}`)
	adminPost(t, adminURL, adminTok, "/v1/subscriptions",
		fmt.Sprintf(`{"endpoint_id":%q,"topic_pattern":"ng.*"}`, epID))

	baseDB, _ := fetchDB() // delta-baseline: prior test's deliveries share this DB
	load := &Load{APIURL: apiURL, Key: key, Topic: "ng.test"}
	load.setPhase(phasePre)
	stopLoad := load.start(ctx, 25)
	waitAccepted(t, load, phasePre, acceptFloor, 60*time.Second)

	// Snapshot restart counts for EVERY process that can (re)create the group: the
	// worker (read-loop re-ensure, the path under test) AND the scheduler (startup
	// EnsureGroupWithRetry). If either restarted, the group could have been recreated
	// for the wrong reason — so we require BOTH to stay stable (Codex M3 MAJOR-2).
	const ensurerSel = "app in (worker,scheduler)"
	preRestarts := podRestartCounts(t, ensurerSel)

	// Induce NOGROUP: destroy the consumer group on the live master. The worker read
	// loop's next XREADGROUP returns NOGROUP -> IsNoGroup -> EnsureGroup -> recovers.
	load.setPhase(phaseNoMaster) // reuse the middle bucket as the "group-lost" window
	if out, err := redisExecTry(master, "XGROUP", "DESTROY", redisStream, redisGroup); err != nil {
		t.Fatalf("XGROUP DESTROY: %v (%s)", err, out)
	} else {
		t.Logf("destroyed consumer group %s on %s: %s", redisGroup, master, out)
	}

	// Keep load flowing so the worker hits NOGROUP and must re-ensure to make progress.
	load.setPhase(phasePost)
	waitAccepted(t, load, phasePost, acceptFloor, 120*time.Second)
	stopLoad()

	totalAccepted, totalAttempts := 0, 0
	for _, p := range []phase{phasePre, phaseNoMaster, phasePost} {
		a, acc := load.snap(p)
		totalAttempts += a
		totalAccepted += acc
	}

	// Recovery: everything converges to succeeded (the re-ensure path drains the
	// post-destroy load; any stream gap is re-driven by the sweeper from PG).
	snap, err := pollRecovered(recvURL, baseDB.Succeeded+totalAccepted, baseDB.Succeeded+totalAttempts, -1 /* dup ceiling disabled: logged-only */, 180*time.Second)
	if err != nil {
		t.Fatalf("NOGROUP recovery did not converge: %v", err)
	}
	t.Logf("NOGROUP recovery: succeeded=%d distinct=%d dups(logged)=%d accepted=%d",
		snap.DB.Succeeded, snap.Stats.Distinct, snap.Stats.Duplicates, totalAccepted)

	acceptedIDs := load.acceptedIDsAll()
	succ, found, err := succeededAmong(acceptedIDs)
	if err != nil {
		t.Fatalf("per-id reconcile: %v", err)
	}
	if found != len(acceptedIDs) || succ != len(acceptedIDs) {
		t.Fatalf("NOGROUP: %d/%d found, %d/%d succeeded (expected all)", found, len(acceptedIDs), succ, len(acceptedIDs))
	}

	// The load-bearing assertion: recovery was the worker read-loop's in-place
	// re-ensure — NOT a crash+restart of the worker OR scheduler re-running startup
	// EnsureGroup. So NO worker/scheduler pod may have restarted or been replaced.
	postRestarts := podRestartCounts(t, ensurerSel)
	for pod, pre := range preRestarts {
		post, ok := postRestarts[pod]
		if !ok {
			t.Fatalf("pod %s vanished — recovery via replacement, not in-place re-ensure", pod)
		}
		if post != pre {
			t.Fatalf("pod %s restarted (%d->%d) — NOGROUP recovery must be the worker read-loop re-ensure, not a restart", pod, pre, post)
		}
	}
	t.Logf("NOGROUP recovery in-place confirmed: all %d worker+scheduler pods stable across re-ensure", len(preRestarts))
}
