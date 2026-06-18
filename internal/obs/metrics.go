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
		Help: "Events accepted at ingress, by result (accepted|replayed|conflict|rejected).",
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

	RetentionRowsPruned = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "hookrail_retention_rows_pruned_total", Help: "Rows pruned by the retention janitor, by job."},
		[]string{"job"})
	RetentionTickSeconds = promauto.NewHistogram(prometheus.HistogramOpts{
		Name: "hookrail_retention_tick_seconds", Help: "Wall-clock duration of one retention tick."})
)
