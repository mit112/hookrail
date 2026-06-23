// cmd/hookrail-dashboard/main.go
package main

import (
	"context"
	"log"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/mit112/hookrail/internal/dashboard"
)

func main() {
	// Structured logger as the default so NewServer's audit logging is JSON.
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))

	cfg, err := dashboard.LoadConfig()
	if err != nil {
		log.Fatalf("dashboard config: %v", err) // fail-closed before binding
	}
	app := dashboard.NewServer(cfg)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Attest the startup role tokens BEFORE enabling SIGHUP/reprobe so no
	// concurrent publish can race the initial attest. On failure we still start:
	// proxyAdmin/readyz fail closed (503) until an attested snapshot exists, and
	// the periodic re-probe recovers once the admin API is reachable (D11).
	if err := app.InitialAttest(ctx); err != nil {
		slog.Warn("initial role-token attestation failed; starting unready", "err", err)
	}
	// Hot-reload users + role tokens on SIGHUP (bounded revocation, D3).
	app.InstallSIGHUP(ctx)
	// Continuously re-attest so a revoked/rotated token fails closed (D11).
	app.StartReprobe(ctx, cfg.AttestInterval)

	slog.Info("hookrail-dashboard listening", "addr", cfg.Addr)
	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           app.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}
