package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/mit112/hookrail/internal/config"
	"github.com/mit112/hookrail/internal/obs"
	"github.com/mit112/hookrail/internal/queue"
	"github.com/mit112/hookrail/internal/scheduler"
	"github.com/mit112/hookrail/internal/store"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cfg, err := config.Load()
	if err != nil {
		slog.Error("config", "err", err)
		os.Exit(1)
	}
	shutdown, err := obs.InitTracing(ctx, "hookrail-scheduler")
	if err != nil {
		slog.Error("tracing", "err", err)
		os.Exit(1)
	}
	defer func() { _ = shutdown(context.Background()) }()
	s, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		slog.Error("store", "err", err)
		os.Exit(1)
	}
	defer s.Close()
	q, err := queue.New(cfg.RedisAddr, cfg.Stream, cfg.Group)
	if err != nil {
		slog.Error("queue", "err", err)
		os.Exit(1)
	}
	defer q.Close()
	q.MaxLen = cfg.StreamMaxLen
	if err := q.EnsureGroup(ctx); err != nil {
		slog.Error("ensure group", "err", err)
		os.Exit(1)
	}

	go func() {
		mux := http.NewServeMux()
		mux.Handle("GET /metrics", promhttp.Handler())
		_ = (&http.Server{
			Addr:              ":8083",
			Handler:           mux,
			ReadHeaderTimeout: 5 * time.Second,
			ReadTimeout:       15 * time.Second,
			WriteTimeout:      30 * time.Second,
			IdleTimeout:       60 * time.Second,
		}).ListenAndServe()
	}()

	sw := &scheduler.Sweeper{Source: s, Publisher: q, Reconciler: s, Interval: 30 * time.Second, BatchSize: 1000}

	if cfg.RetentionEnabled {
		j := &scheduler.Janitor{
			Store: s, PayloadAge: cfg.EventPayloadRetention, AttemptAge: cfg.AttemptRetention,
			Batch: cfg.RetentionBatch, Interval: cfg.RetentionInterval, TickBudget: cfg.RetentionTickBudget,
		}
		go j.Run(ctx)
		slog.Info("retention janitor started", "interval", cfg.RetentionInterval)
	}

	slog.Info("hookrail-scheduler started", "interval", sw.Interval)
	if err := sw.Run(ctx); err != nil && ctx.Err() == nil {
		slog.Error("sweeper exited", "err", err)
		os.Exit(1)
	}
}
