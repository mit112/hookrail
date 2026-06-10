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

	"github.com/mit112/hookrail/internal/backoff"
	"github.com/mit112/hookrail/internal/crypto"
	"github.com/mit112/hookrail/internal/domain"
	"github.com/mit112/hookrail/internal/queue"
	"github.com/mit112/hookrail/internal/ratelimit"
	"github.com/mit112/hookrail/internal/signing"
	"github.com/mit112/hookrail/internal/ssrf"
	"github.com/mit112/hookrail/internal/store"
)

type Worker struct {
	Store     *store.Store
	Queue     *queue.Queue // nil in unit-style tests that call Process directly
	Client    *http.Client // ssrf.NewHTTPClient(Policy)
	Policy    ssrf.Policy
	Backoff   backoff.Policy
	Limits    *ratelimit.Registry // per endpoint id
	MasterKey [32]byte
	Lease     time.Duration
	Consumer  string
}

// Run is the dispatch loop: drain new messages, then steal abandoned ones.
func (w *Worker) Run(ctx context.Context) error {
	for ctx.Err() == nil {
		msgs, err := w.Queue.Read(ctx, w.Consumer, 16, 2*time.Second)
		if err != nil && ctx.Err() == nil {
			slog.Warn("queue read", "err", err)
			time.Sleep(time.Second)
			continue
		}
		if len(msgs) == 0 {
			// PEL recovery (§6): claim messages abandoned by crashed workers
			msgs, _ = w.Queue.Autoclaim(ctx, w.Consumer, w.Lease, 16)
		}
		for _, m := range msgs {
			w.Process(ctx, m.DeliveryID)
			if err := w.Queue.Ack(ctx, m.ID); err != nil {
				slog.Warn("ack failed", "msg", m.ID, "err", err)
			}
		}
	}
	return ctx.Err()
}

// Process claims and attempts one delivery. Always safe to call with a
// delivery someone else owns — the CAS makes it a no-op.
func (w *Worker) Process(ctx context.Context, deliveryID string) {
	claimed, d, err := w.Store.ClaimDelivery(ctx, deliveryID, w.Lease)
	if err != nil {
		slog.Error("claim", "delivery_id", deliveryID, "err", err)
		return
	}
	if !claimed {
		return // ack & drop: someone else owns it (§3.4)
	}

	// hard stop: a crash loop that claims-without-completing must terminate.
	// The claim increments attempt_count, so a delivery whose budget was
	// consumed entirely by crashed claims dead-letters here — through the
	// dedicated fenced operation, which writes NO consumer attempt row and
	// un-counts this claim (no HTTP request happened; the attempt history
	// must never exceed the max_attempts contract).
	if d.AttemptCount > d.MaxAttempts {
		if err := w.Store.DeadLetterExhausted(ctx, d.ID, d.ClaimVersion); err != nil && !errors.Is(err, store.ErrStaleClaim) {
			slog.Error("dead-letter exhausted failed; lease recovery will retry", "delivery_id", d.ID, "err", err)
		}
		return
	}

	// per-endpoint token bucket (§4): over-budget → RELEASE the claim.
	// No HTTP request happened, so no attempt budget may be consumed — the
	// release decrements attempt_count back and reschedules shortly.
	if !w.Limits.Allow(d.EndpointID, time.Now()) {
		if err := w.Store.ReleaseClaim(ctx, d.ID, d.ClaimVersion, time.Second); err != nil {
			slog.Warn("release after rate limit failed", "delivery_id", d.ID, "err", err)
		}
		return
	}

	res := w.attempt(ctx, d)
	w.record(ctx, d, res)
}

// attempt runs the guarded POST and classifies. Panics are recovered to a
// permanent policy outcome (§10: poison payload → error_class=panic).
func (w *Worker) attempt(ctx context.Context, d store.ClaimedDelivery) (res store.AttemptResult) {
	started := time.Now()
	res = store.AttemptResult{DeliveryID: d.ID, AttemptNo: d.AttemptCount, ClaimVersion: d.ClaimVersion, RequestedAt: started}
	defer func() {
		if r := recover(); r != nil {
			slog.Error("panic during delivery", "delivery_id", d.ID, "panic", r)
			res.Outcome, res.ErrorClass = domain.ClassifyError(domain.ErrPanic)
			res.CompletedAt = time.Now()
		}
	}()
	finish := func() {
		res.CompletedAt = time.Now()
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
	req.Header.Set("hookrail-event-id", d.EventID)       // both ids ship in headers (§4)
	req.Header.Set("hookrail-delivery-id", d.ID)         // the consumer dedup key (§4)
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
	switch err := w.Store.CompleteAttempt(ctx, res, w.Backoff, d.MaxAttempts); {
	case err == nil:
	case errors.Is(err, store.ErrStaleClaim):
		// our lease expired and another worker owns this delivery now —
		// drop our result; the new owner's completion is authoritative
		slog.Info("dropped stale completion", "delivery_id", d.ID, "attempt", res.AttemptNo)
	default:
		// PG write failed: do nothing — the lease expires and both recovery
		// layers re-run the attempt. Duplicate possible, loss impossible (§10).
		slog.Error("record attempt failed; lease recovery will retry",
			"delivery_id", d.ID, "err", err)
	}
}
