// cmd/hookrail-admin/main.go
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/mit112/hookrail/internal/admin"
	"github.com/mit112/hookrail/internal/config"
	"github.com/mit112/hookrail/internal/obs"
	"github.com/mit112/hookrail/internal/queue"
	"github.com/mit112/hookrail/internal/ratelimit"
	"github.com/mit112/hookrail/internal/ssrf"
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
	if cfg.AdminToken == "" {
		slog.Error("HOOKRAIL_ADMIN_TOKEN is required; refusing to boot an unauthenticated admin surface")
		os.Exit(1)
	}
	if len(cfg.AdminToken) < 16 {
		slog.Error("HOOKRAIL_ADMIN_TOKEN must be >= 16 chars")
		os.Exit(1)
	}
	shutdown, err := obs.InitTracing(ctx, "hookrail-admin")
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

	prefixes, err := cfg.AllowPrefixes()
	if err != nil {
		slog.Error("config", "err", err)
		os.Exit(1)
	}
	pol := ssrf.Policy{AllowHTTP: cfg.AllowHTTP, AllowCIDRs: prefixes}

	srv := &http.Server{
		Addr:              cfg.AdminListen,
		Handler:           admin.New(s, q, cfg.MasterKey, pol, ratelimit.NewRegistry(500, 1000), cfg.AdminToken, cfg.EventPayloadRetention).Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(shctx)
	}()
	slog.Info("hookrail-admin listening (INTERNAL)", "addr", cfg.AdminListen)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		slog.Error("serve", "err", err)
		os.Exit(1)
	}
}
