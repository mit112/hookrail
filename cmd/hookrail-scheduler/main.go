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
	"github.com/mit112/hookrail/internal/leader"
	"github.com/mit112/hookrail/internal/obs"
	"github.com/mit112/hookrail/internal/queue"
	"github.com/mit112/hookrail/internal/redisclient"
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
	s, err := store.OpenWithRetry(ctx, cfg.DatabaseURL, cfg.DBConnectTimeout)
	if err != nil {
		slog.Error("store", "err", err)
		os.Exit(1)
	}
	defer s.Close()
	rdb, err := redisclient.New(redisclient.Options{
		Addr:          cfg.RedisAddr,
		SentinelAddrs: cfg.RedisSentinelAddrs,
		MasterName:    cfg.RedisMasterName,
	})
	if err != nil {
		slog.Error("redis", "err", err)
		os.Exit(1)
	}
	q := queue.NewWithClient(rdb, cfg.Stream, cfg.Group)
	defer q.Close()
	q.MaxLen = cfg.StreamMaxLen
	// The scheduler only XADDs (which auto-creates the stream); the worker is the
	// sole consumer-group reader and owns NOGROUP recovery after a Sentinel failover.
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

	el := leader.New(cfg.DatabaseURL, leader.SchedulerLeaderLockKey, 5*time.Second, func(v bool) {
		obs.SchedulerIsLeader.Set(map[bool]float64{true: 1, false: 0}[v])
		if v {
			slog.Info("became leader")
		} else {
			slog.Info("lost leadership")
		}
	})

	if cfg.RetentionEnabled {
		j := &scheduler.Janitor{
			Store: s, PayloadAge: cfg.EventPayloadRetention, AttemptAge: cfg.AttemptRetention,
			Batch: cfg.RetentionBatch, Interval: cfg.RetentionInterval, TickBudget: cfg.RetentionTickBudget,
		}
		go j.Run(ctx)
		slog.Info("retention janitor started", "interval", cfg.RetentionInterval)
	}

	slog.Info("hookrail-scheduler started", "interval", sw.Interval)
	if err := el.Run(ctx, 30*time.Second, sw.Startup, sw.Cycle); err != nil && ctx.Err() == nil {
		slog.Error("elector exited", "err", err)
		os.Exit(1)
	}
}
