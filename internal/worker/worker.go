// Package worker implements §3.4: XREADGROUP → CAS-claim → SSRF-guarded
// signed POST with timeout budget → classify (§7) → attempt+transition in one
// PG txn → XACK. Crash anywhere: PG lease expiry + Redis PEL both recover it.
package worker

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/mit112/hookrail/internal/backoff"
	"github.com/mit112/hookrail/internal/crypto"
	"github.com/mit112/hookrail/internal/domain"
	"github.com/mit112/hookrail/internal/obs"
	"github.com/mit112/hookrail/internal/queue"
	"github.com/mit112/hookrail/internal/ratelimit"
	"github.com/mit112/hookrail/internal/signing"
	"github.com/mit112/hookrail/internal/ssrf"
	"github.com/mit112/hookrail/internal/store"
)

// DispatchQueue is the subset of *queue.Queue the dispatch loop uses. It is an
// interface so the NOGROUP-recovery path is unit-testable with a fake; *queue.Queue
// satisfies it, so production wiring is unchanged.
type DispatchQueue interface {
	Read(ctx context.Context, consumer string, count int, block time.Duration) ([]queue.Msg, error)
	Autoclaim(ctx context.Context, consumer string, minIdle time.Duration, count int) ([]queue.Msg, error)
	Ack(ctx context.Context, msgID string) error
	EnsureGroup(ctx context.Context) error
	Publish(ctx context.Context, deliveryID string) error
}

type Worker struct {
	Store     *store.Store
	Queue     DispatchQueue // nil in unit-style tests that call Process directly
	Client    *http.Client  // ssrf.NewHTTPClient(Policy)
	Policy    ssrf.Policy
	Backoff   backoff.Policy
	Limits    *ratelimit.Registry      // per endpoint id
	Global    *ratelimit.GlobalLimiter // when non-nil, override endpoints route through Redis-backed global limiter
	MasterKey [32]byte
	Lease     time.Duration
	Consumer  string

	InFlight  *InFlight // race-free in-flight tracker (reserve-before-claim); nil tolerated
	Heartbeat func()    // pumped once per dispatch-loop iteration for liveness; nil tolerated
}

// Run is the dispatch loop: drain new messages, then steal abandoned ones.
// intakeCtx controls the XREADGROUP/Autoclaim intake loop (canceled on SIGTERM).
// workCtx drives Process (claim/HTTP/record) so in-flight deliveries survive SIGTERM.
func (w *Worker) Run(intakeCtx, workCtx context.Context) error {
	for intakeCtx.Err() == nil {
		if w.Heartbeat != nil {
			// Beat on every pass — including empty ones where Read blocks ~2s —
			// so an idle worker still proves liveness. A per-message beat below
			// covers long batches (a pass can process up to 16 messages serially,
			// each up to the HTTP attempt timeout).
			w.Heartbeat()
		}
		msgs, err := w.Queue.Read(intakeCtx, w.Consumer, 16, 2*time.Second)
		if err != nil && intakeCtx.Err() == nil {
			if queue.IsNoGroup(err) {
				// Promoted Sentinel master is missing the consumer group (XGROUP
				// CREATE never replicated); recreate it (idempotent). Only fast-retry
				// on a SUCCESSFUL re-ensure — otherwise fall through to the same
				// bounded backoff as any other read error so we never hot-spin
				// XREADGROUP->NOGROUP->failed-CREATE during a failover (Codex MAJOR-3).
				if gerr := w.Queue.EnsureGroup(intakeCtx); gerr == nil {
					continue
				} else {
					slog.Warn("ensure group after NOGROUP", "err", gerr)
				}
			} else {
				slog.Warn("queue read", "err", err)
			}
			time.Sleep(time.Second)
			continue
		}
		if len(msgs) == 0 {
			// PEL recovery (§6): claim messages abandoned by crashed workers.
			// Observe the error like the Read path does — a silently-dropped
			// Autoclaim failure (network blip, NOGROUP after a fast Sentinel
			// failover) would make stalled PEL recovery invisible.
			claimed, aerr := w.Queue.Autoclaim(intakeCtx, w.Consumer, w.Lease, 16)
			if aerr != nil && intakeCtx.Err() == nil {
				if queue.IsNoGroup(aerr) {
					if gerr := w.Queue.EnsureGroup(intakeCtx); gerr != nil {
						slog.Warn("ensure group after autoclaim NOGROUP", "err", gerr)
					}
				} else {
					slog.Warn("autoclaim", "err", aerr)
				}
			}
			msgs = claimed
		}
		for _, m := range msgs {
			// On SIGTERM stop claiming NEW buffered work immediately: any
			// already-read-but-unprocessed messages stay in this consumer's
			// PEL and are recovered by a survivor's Autoclaim. This bounds the
			// post-SIGTERM work to at most the one Process already in flight per
			// worker, so wg.Wait() returns promptly and drain runs well within
			// the termination grace period (Codex M3 pre-gate BLOCKER-1).
			if intakeCtx.Err() != nil {
				break
			}
			if w.Heartbeat != nil {
				w.Heartbeat() // per-message: a long batch of slow deliveries stays live
			}
			w.Process(workCtx, m.DeliveryID)
			// Ack on a fresh bounded context, NOT intakeCtx: Process has already
			// committed a terminal state in PG, so the XACK must still land even
			// when SIGTERM has canceled intakeCtx. Otherwise the message lingers
			// in the PEL until a survivor's Autoclaim recovers it — a needless
			// duplicate-delivery window on every graceful shutdown.
			ackCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			err := w.Queue.Ack(ackCtx, m.ID)
			cancel()
			if err != nil {
				slog.Warn("ack failed", "msg", m.ID, "err", err)
			}
		}
	}
	return intakeCtx.Err()
}

// Process claims and attempts one delivery. Always safe to call with a
// delivery someone else owns — the CAS makes it a no-op.
func (w *Worker) Process(ctx context.Context, deliveryID string) {
	if w.InFlight != nil {
		w.InFlight.Reserve()
	}
	claimed, d, err := w.Store.ClaimDelivery(ctx, deliveryID, w.Lease)
	if err != nil {
		slog.Error("claim", "delivery_id", deliveryID, "err", err)
		if w.InFlight != nil {
			w.InFlight.Abort()
		}
		return
	}
	if !claimed {
		if w.InFlight != nil {
			w.InFlight.Abort()
		}
		return // ack & drop: someone else owns it (§3.4)
	}

	if w.InFlight != nil {
		w.InFlight.Finalize(d.ID, d.ClaimVersion)
	}

	// hard stop: a crash loop that claims-without-completing must terminate.
	// The claim increments attempt_count, so a delivery whose budget was
	// consumed entirely by crashed claims dead-letters here — through the
	// dedicated fenced operation, which writes NO consumer attempt row and
	// un-counts this claim (no HTTP request happened; the attempt history
	// must never exceed the max_attempts contract).
	if d.AttemptCount > d.MaxAttempts {
		_, err := w.Store.DeadLetterExhausted(ctx, d.ID, d.ClaimVersion)
		if err != nil && !errors.Is(err, store.ErrStaleClaim) {
			slog.Error("dead-letter exhausted failed; lease recovery will retry", "delivery_id", d.ID, "err", err)
			// do NOT Remove on error — drain must still release it
		} else {
			// success or ErrStaleClaim (another owner has it) → Remove
			if w.InFlight != nil {
				w.InFlight.Remove(d.ID)
			}
		}
		return
	}

	// per-endpoint token bucket (§4): over-budget → RELEASE the claim.
	// No HTTP request happened, so no attempt budget may be consumed — the
	// release decrements attempt_count back and reschedules shortly.
	now := time.Now()
	allowed, mode := w.allowDelivery(ctx, d.EndpointID, now)
	obs.RatelimitDecisions.WithLabelValues(decResult(allowed), mode).Inc()
	if !allowed {
		if err := w.Store.ReleaseClaim(ctx, d.ID, d.ClaimVersion, time.Second); err != nil {
			slog.Warn("release after rate limit failed", "delivery_id", d.ID, "err", err)
			// release failed → the row may still be in_flight under our claim;
			// leave it in the tracker so drain releases it.
		} else if w.InFlight != nil {
			// ReleaseClaim moved the row to retry_scheduled — it is no longer
			// our in-flight claim, so Remove it. Leaving it would leak entries
			// under sustained rate limiting (Codex M3 pre-gate MAJOR-3).
			w.InFlight.Remove(d.ID)
		}
		return
	}

	res := w.attempt(ctx, d)
	w.record(ctx, d, res)
}

// allowDelivery routes the rate-limit check: global Redis-backed for override
// endpoints, local in-process Registry for everything else.
func (w *Worker) allowDelivery(ctx context.Context, endpoint string, now time.Time) (bool, string) {
	if w.Global != nil {
		// Single atomic snapshot load inside Decide — no torn Has()+Allow() race.
		if handled, allowed, mode := w.Global.Decide(ctx, endpoint, now); handled {
			return allowed, mode
		}
	}
	return w.Limits.Allow(endpoint, now), "local"
}

func decResult(allowed bool) string {
	if allowed {
		return "allowed"
	}
	return "denied"
}

// attempt runs the guarded POST and classifies. Panics are recovered to a
// permanent policy outcome (§10: poison payload → error_class=panic).
func (w *Worker) attempt(ctx context.Context, d store.ClaimedDelivery) (res store.AttemptResult) {
	started := time.Now()
	res = store.AttemptResult{DeliveryID: d.ID, AttemptNo: d.AttemptCount, ClaimVersion: d.ClaimVersion, RequestedAt: started}
	ctx, span := otel.Tracer("hookrail-worker").Start(ctx, "delivery_attempt",
		trace.WithAttributes(
			attribute.String("hookrail.delivery_id", d.ID),
			attribute.Int("hookrail.attempt_no", d.AttemptCount)))
	defer span.End()
	defer func() {
		if r := recover(); r != nil {
			slog.Error("panic during delivery", "delivery_id", d.ID, "panic", r)
			res.Outcome, res.ErrorClass = domain.ClassifyError(domain.ErrPanic)
			res.CompletedAt = time.Now()
		}
	}()
	finish := func() {
		res.CompletedAt = time.Now()
		obs.AttemptDuration.Observe(res.CompletedAt.Sub(started).Seconds())
		obs.DeliveriesTotal.WithLabelValues(string(res.Outcome), orDash(res.ErrorClass)).Inc()
		res.LatencyMS = int(res.CompletedAt.Sub(started).Milliseconds())
	}

	// URL policy at dial time too — DNS/rows change between registration and now (§8)
	if err := w.Policy.ValidateURL(d.URL); err != nil {
		res.Outcome, res.ErrorClass = domain.ClassifyError(err)
		finish()
		return res
	}
	secret, err := crypto.Decrypt(w.MasterKey, d.SecretCiphertext)
	if err != nil {
		res.Outcome, res.ErrorClass = domain.OutcomePermanent, "secret_decrypt"
		finish()
		return res
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.URL, bytes.NewReader(d.Payload))
	if err != nil {
		res.Outcome, res.ErrorClass = domain.ClassifyError(err)
		finish()
		return res
	}
	now := time.Now()
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "hookrail/0.1")
	req.Header.Set("hookrail-event-id", d.EventID) // both ids ship in headers (§4)
	req.Header.Set("hookrail-delivery-id", d.ID)   // the consumer dedup key (§4)
	req.Header.Set("hookrail-topic", d.Topic)
	req.Header.Set(signing.Header, signing.Sign(secret, now, d.ID, d.Payload))

	resp, err := w.Client.Do(req)
	if err != nil {
		res.Outcome, res.ErrorClass = domain.ClassifyError(err)
		finish()
		return res
	}
	defer func() { _ = resp.Body.Close() }()
	// read at most 64KB then discard (§8: slow-loris / huge-body defense)
	_, _ = io.CopyN(io.Discard, resp.Body, ssrf.MaxResponseBytes)

	res.HTTPStatus = resp.StatusCode
	res.Outcome, res.ErrorClass = domain.ClassifyStatus(resp.StatusCode)
	if res.Outcome == domain.OutcomeRetryable {
		if ra, ok := backoff.ParseRetryAfter(resp.Header.Get("Retry-After"), time.Now()); ok {
			res.RetryAfter = ra
		}
	}
	finish()
	return res
}

func (w *Worker) record(ctx context.Context, d store.ClaimedDelivery, res store.AttemptResult) {
	pol := backoff.FromJSON(d.BackoffPolicy, d.MaxAttempts) // per-delivery (design §4.3); nil → w.Backoff-equivalent default
	nextHead, err := w.Store.CompleteAttempt(ctx, res, pol, d.MaxAttempts)
	switch {
	case err == nil:
		// Confirmed terminal write → Remove from tracker.
		if w.InFlight != nil {
			w.InFlight.Remove(d.ID)
		}
		// Publish the next head to Redis AFTER commit (store never XADDs — BLOCKER-2).
		// The sweeper is the backstop if the publish fails.
		if nextHead != nil && w.Queue != nil {
			if perr := w.Queue.Publish(ctx, *nextHead); perr != nil {
				slog.Warn("publish next ordered head failed; sweeper will repair", "next_head", *nextHead, "err", perr)
			}
		}
	case errors.Is(err, store.ErrStaleClaim):
		// Another owner has it — Remove from tracker.
		if w.InFlight != nil {
			w.InFlight.Remove(d.ID)
		}
		// our lease expired and another worker owns this delivery now —
		// drop our result; the new owner's completion is authoritative
		slog.Info("dropped stale completion", "delivery_id", d.ID, "attempt", res.AttemptNo)
	default:
		// PG write failed: do NOT Remove — drain will release it.
		// The lease expires and both recovery layers re-run the attempt.
		// Duplicate possible, loss impossible (§10).
		slog.Error("record attempt failed; lease recovery will retry",
			"delivery_id", d.ID, "err", err)
	}
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
