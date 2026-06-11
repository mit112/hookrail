package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

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
	if err := s.Migrate(); err != nil { // idempotent self-heal; compose runs the dedicated migrate job first
		slog.Error("migrate", "err", err)
		os.Exit(1)
	}
	q, err := queue.New(cfg.RedisAddr, cfg.Stream, cfg.Group)
	if err != nil {
		slog.Error("queue", "err", err)
		os.Exit(1)
	}
	defer q.Close()
	if err := q.EnsureGroup(ctx); err != nil {
		slog.Error("ensure group", "err", err)
		os.Exit(1)
	}

	sw := &scheduler.Sweeper{Source: s, Publisher: q, Interval: 30 * time.Second, BatchSize: 1000}
	slog.Info("hookrail-scheduler started", "interval", sw.Interval)
	if err := sw.Run(ctx); err != nil && ctx.Err() == nil {
		slog.Error("sweeper exited", "err", err)
		os.Exit(1)
	}
}
