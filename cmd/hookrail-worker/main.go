package main

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/redis/go-redis/v9"

	"github.com/mit112/hookrail/internal/backoff"
	"github.com/mit112/hookrail/internal/config"
	"github.com/mit112/hookrail/internal/obs"
	"github.com/mit112/hookrail/internal/queue"
	"github.com/mit112/hookrail/internal/ratelimit"
	"github.com/mit112/hookrail/internal/redisclient"
	"github.com/mit112/hookrail/internal/ssrf"
	"github.com/mit112/hookrail/internal/store"
	"github.com/mit112/hookrail/internal/worker"
)

func main() {
	intakeCtx, intakeStop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer intakeStop()
	workCtx, workCancel := context.WithCancel(context.Background())
	defer workCancel()

	cfg, err := config.Load()
	if err != nil {
		slog.Error("config", "err", err)
		os.Exit(1)
	}
	shutdown, err := obs.InitTracing(intakeCtx, "hookrail-worker")
	if err != nil {
		slog.Error("tracing", "err", err)
		os.Exit(1)
	}
	defer func() { _ = shutdown(context.Background()) }()
	s, err := store.OpenWithRetry(intakeCtx, cfg.DatabaseURL, cfg.DBConnectTimeout)
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
	// Bounded retry: in Sentinel mode the master can be briefly unresolvable at boot
	// (the wait-redis init only gates on a reachable sentinel, not master discovery).
	if err := q.EnsureGroupWithRetry(intakeCtx, 60*time.Second); err != nil {
		slog.Error("ensure group", "err", err)
		os.Exit(1)
	}

	// Dedicated Redis client for the global rate limiter (P2).
	// Separate from the queue client so blocking XREADGROUP does not
	// head-of-line-block limiter EVAL calls.
	var rlClient *redis.Client
	if cfg.GlobalRateLimit {
		// Plain mode: RLRedisAddr carries the addr/toxiproxy override. Sentinel mode:
		// RLRedisAddr is "" and SentinelAddrs wins (a separate FailoverClient so a
		// blocking XREADGROUP never head-of-line-blocks limiter EVALs).
		rlClient, err = redisclient.New(redisclient.Options{
			Addr:          cfg.RLRedisAddr,
			SentinelAddrs: cfg.RedisSentinelAddrs,
			MasterName:    cfg.RedisMasterName,
			PoolSize:      8,
			ReadTimeout:   200 * time.Millisecond,
			WriteTimeout:  200 * time.Millisecond,
		})
		if err != nil {
			slog.Error("limiter redis", "err", err)
			os.Exit(1)
		}
		defer func() { _ = rlClient.Close() }()
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
	var global *ratelimit.GlobalLimiter
	var fb *ratelimit.Registry
	if cfg.GlobalRateLimit {
		fb = ratelimit.NewRegistry(defRPS, defBurst)
		rl := ratelimit.NewRedisLimiter(rlClient, cfg.RLTTLFloor)
		global = ratelimit.NewGlobalLimiter(rl, fb, cfg.RLTimeout)
	}
	el := &worker.EndpointLimits{
		Store: s, Registry: limits,
		Global: global, Fallback: fb,
		Interval:    cfg.LimitsRefreshInterval,
		DefaultRate: defRPS, DefaultBurst: defBurst,
	}
	if global != nil {
		if err := el.Refresh(intakeCtx); err != nil {
			slog.Error("initial rate-limit snapshot load failed; refusing to start in global mode", "err", err)
			os.Exit(1)
		}
	}
	go el.Run(intakeCtx)
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
	tracker := worker.NewInFlight()

	const poolSize = 8 // goroutine pool (§3.4)
	var wg sync.WaitGroup
	for i := 0; i < poolSize; i++ {
		w := &worker.Worker{
			Store: s, Queue: q,
			Client: ssrf.NewHTTPClient(pol), Policy: pol,
			Backoff: backoff.Default(), Limits: limits,
			Global:    global,
			MasterKey: cfg.MasterKey, Lease: 30 * time.Second,
			Consumer: hostConsumer(host, i),
			InFlight: tracker,
		}
		wg.Add(1)
		go func() { defer wg.Done(); _ = w.Run(intakeCtx, workCtx) }()
	}
	slog.Info("hookrail-worker started", "pool", poolSize)
	wg.Wait()

	// Graceful drain: release any in-flight deliveries so survivors pick them up.
	drainCtx, drainCancel := context.WithTimeout(workCtx, cfg.DrainDeadline)
	defer drainCancel()
	held := tracker.DrainSnapshot(drainCtx)
	for _, h := range held {
		jitter := time.Duration(rand.Int63n(int64(cfg.DrainRetryJitterMax))) //nolint:gosec // drain jitter, not a security context
		if err := s.ReleaseClaimForDrain(drainCtx, h.ID, h.ClaimVersion, jitter); err != nil {
			slog.Warn("drain release failed", "delivery_id", h.ID, "err", err)
		}
	}
	workCancel()
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
