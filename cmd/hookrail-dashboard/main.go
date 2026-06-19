// cmd/hookrail-dashboard/main.go
package main

import (
	"log"
	"net/http"
	"time"

	"github.com/mit112/hookrail/internal/dashboard"
)

func main() {
	cfg, err := dashboard.LoadConfig()
	if err != nil {
		log.Fatalf("dashboard config: %v", err) // fail-closed before binding
	}
	app := dashboard.NewServer(cfg)
	log.Printf("hookrail-dashboard listening on %s", cfg.Addr)
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
