package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/mit112/hookrail/internal/backoff"
	"github.com/mit112/hookrail/internal/config"
	"github.com/mit112/hookrail/internal/queue"
	"github.com/mit112/hookrail/internal/ratelimit"
	"github.com/mit112/hookrail/internal/ssrf"
	"github.com/mit112/hookrail/internal/store"
	"github.com/mit112/hookrail/internal/worker"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cfg, err := config.Load()
	if err != nil {
		slog.Error("config", "err", err)
		os.Exit(1)
	}
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
	if err := q.EnsureGroup(ctx); err != nil {
		slog.Error("ensure group", "err", err)
		os.Exit(1)
	}

	prefixes, err := cfg.AllowPrefixes()
	if err != nil {
		slog.Error("config", "err", err)
		os.Exit(1)
	}
	pol := ssrf.Policy{AllowHTTP: cfg.AllowHTTP, AllowCIDRs: prefixes}

	host, _ := os.Hostname()
	limits := ratelimit.NewRegistry(50, 100) // per-endpoint default; per-sub rps is P1 wiring
	const poolSize = 8                       // goroutine pool (§3.4)
	var wg sync.WaitGroup
	for i := 0; i < poolSize; i++ {
		w := &worker.Worker{
			Store: s, Queue: q,
			Client: ssrf.NewHTTPClient(pol), Policy: pol,
			Backoff: backoff.Default(), Limits: limits,
			MasterKey: cfg.MasterKey, Lease: 30 * time.Second,
			Consumer: hostConsumer(host, i),
		}
		wg.Add(1)
		go func() { defer wg.Done(); _ = w.Run(ctx) }()
	}
	slog.Info("hookrail-worker started", "pool", poolSize)
	wg.Wait()
}

func hostConsumer(host string, i int) string {
	return fmt.Sprintf("%s-%c", host, 'a'+i)
}
