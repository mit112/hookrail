package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/mit112/hookrail/internal/api"
	"github.com/mit112/hookrail/internal/config"
	"github.com/mit112/hookrail/internal/obs"
	"github.com/mit112/hookrail/internal/queue"
	"github.com/mit112/hookrail/internal/ratelimit"
	"github.com/mit112/hookrail/internal/redisclient"
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
	shutdown, err := obs.InitTracing(ctx, "hookrail-api")
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

	// Per-producer-key ingress limiter. NOTE: this is per-API-replica and in
	// process (§4.3), so the effective ceiling is rate × replica count — see
	// SPEC.md §10 / README "Honest limitations". Tunable via
	// HOOKRAIL_INGRESS_RATE_RPS / HOOKRAIL_INGRESS_BURST (internal/config).
	srv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           api.New(s, q, ratelimit.NewRegistry(cfg.IngressRateRPS, cfg.IngressBurst), cfg.IdemTTL, cfg.OrderingKeyMaxLen, cfg.OrderedKeyBacklogMax).Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(shctx)
	}()
	slog.Info("hookrail-api listening", "addr", cfg.ListenAddr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		slog.Error("serve", "err", err)
		os.Exit(1)
	}
}
