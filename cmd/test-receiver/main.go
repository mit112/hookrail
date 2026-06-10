package main

import (
	"log/slog"
	"net/http"
	"os"
	"time"
)

func main() {
	addr := os.Getenv("RECEIVER_LISTEN")
	if addr == "" {
		addr = ":9090"
	}
	slog.Info("test-receiver listening", "addr", addr)
	srv := &http.Server{Addr: addr, Handler: newReceiver().handler(), ReadHeaderTimeout: 5 * time.Second}
	if err := srv.ListenAndServe(); err != nil {
		slog.Error("serve", "err", err)
		os.Exit(1)
	}
}
