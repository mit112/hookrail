package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/mit112/hookrail/internal/backoff"
	"github.com/mit112/hookrail/internal/config"
	"github.com/mit112/hookrail/internal/obs"
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
	shutdown, err := obs.InitTracing(ctx, "hookrail-worker")
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

	prefixes, err := cfg.AllowPrefixes()
	if err != nil {
		slog.Error("config", "err", err)
		os.Exit(1)
	}
	pol := ssrf.Policy{AllowHTTP: cfg.AllowHTTP, AllowCIDRs: prefixes}

	host, _ := os.Hostname()
	// Per-endpoint default; per-sub rps is P1 wiring. Default must clear the
	// §11 baseline profiles (fan-out 3 at 200 ev/s = 200 rps/endpoint + retries).
	defRPS := envFloat("HOOKRAIL_DEFAULT_RPS", 1000)
	defBurst := envFloat("HOOKRAIL_DEFAULT_BURST", 2000)
	limits := ratelimit.NewRegistry(defRPS, defBurst)
	el := &worker.EndpointLimits{Store: s, Registry: limits, Interval: 15 * time.Second, DefaultRate: defRPS, DefaultBurst: defBurst}
	go el.Run(ctx)
	go func() {
		mux := http.NewServeMux()
		mux.Handle("GET /metrics", promhttp.Handler())
		_ = (&http.Server{
			Addr:              ":8081",
			Handler:           mux,
			ReadHeaderTimeout: 5 * time.Second,
			ReadTimeout:       15 * time.Second,
			WriteTimeout:      30 * time.Second,
			IdleTimeout:       60 * time.Second,
		}).ListenAndServe()
	}()
	const poolSize = 8 // goroutine pool (§3.4)
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

func envFloat(key string, def float64) float64 {
	if v := os.Getenv(key); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f > 0 {
			return f
		}
		slog.Warn("invalid value, using default", "env", key, "value", v, "default", def)
	}
	return def
}
