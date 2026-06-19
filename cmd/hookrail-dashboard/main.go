// cmd/hookrail-dashboard/main.go
package main

import (
	"log"
	"net/http"

	"github.com/mit112/hookrail/internal/dashboard"
)

func main() {
	cfg, err := dashboard.LoadConfig()
	if err != nil {
		log.Fatalf("dashboard config: %v", err) // fail-closed before binding
	}
	srv := dashboard.NewServer(cfg)
	log.Printf("hookrail-dashboard listening on %s", cfg.Addr)
	//nolint:gosec // G114: ListenAndServe without timeouts is fine in a controlled env
	if err := http.ListenAndServe(cfg.Addr, srv.Handler()); err != nil {
		log.Fatal(err)
	}
}
