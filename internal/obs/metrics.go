// Package obs centralizes Prometheus metrics and OTel tracing setup (§12).
// Metric names are part of the published Grafana dashboard contract — rename
// only with a dashboard update.
package obs

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	IngestEventsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "hookrail_ingest_events_total",
		Help: "Events accepted at ingress, by result (accepted|replayed|conflict|rejected|forbidden).",
	}, []string{"result"})

	DeliveriesTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "hookrail_delivery_attempts_total",
		Help: "Delivery attempts, by outcome (success|retryable|permanent) and error_class.",
	}, []string{"outcome", "error_class"})

	AttemptDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "hookrail_attempt_duration_seconds",
		Help:    "Wall time of one delivery POST.",
		Buckets: prometheus.DefBuckets,
	})

	SweeperRepublished = promauto.NewCounter(prometheus.CounterOpts{
		Name: "hookrail_sweeper_republished_total",
		Help: "Deliveries republished by the PG sweeper (due + stuck).",
	})

	SchedulerIsLeader = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "hookrail_scheduler_is_leader",
		Help: "Whether this scheduler instance is the leader (1 = leader, 0 = standby).",
	})

	RetentionRowsPruned = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "hookrail_retention_rows_pruned_total", Help: "Rows pruned by the retention janitor, by job."},
		[]string{"job"})
	RetentionTickSeconds = promauto.NewHistogram(prometheus.HistogramOpts{
		Name: "hookrail_retention_tick_seconds", Help: "Wall-clock duration of one retention tick."})

	// Ordered-keys observability (§7). Both gauges are re-derived from
	// ordered_key_state on every sweeper reconcile tick (a real emit path).
	OrderedKeysBlocked = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "hookrail_ordered_keys_blocked",
		Help: "Ordered keys currently blocked on a dead_lettered head (paused FIFO).",
	})
	OrderedKeyBacklog = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "hookrail_ordered_key_backlog",
		Help: "Total non-terminal deliveries across all ordered keys (sum of per-key backlog_count).",
	})

	RatelimitDecisions = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "hookrail_ratelimit_decisions_total",
		Help: "Rate-limit decisions at the worker delivery path, by result (allowed|denied) and mode (global|local|failopen).",
	}, []string{"result", "mode"})

	RatelimitConfigAge = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "hookrail_ratelimit_config_age_seconds",
		Help: "Seconds since the last successful global override refresh.",
	})
)
