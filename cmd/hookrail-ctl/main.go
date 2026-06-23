// hookrail-ctl is the P0 stand-in for the admin API (P1): `seed` creates a
// producer key, endpoint, and subscription so the demo/e2e/baseline can run;
// `migrate` applies migrations and exits (the compose one-shot job);
// `retention --once` runs one janitor tick (ops escape hatch, §3).
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"

	"github.com/mit112/hookrail/internal/config"
	"github.com/mit112/hookrail/internal/scheduler"
	"github.com/mit112/hookrail/internal/ssrf"
	"github.com/mit112/hookrail/internal/store"
)

func usage() {
	fmt.Fprintln(os.Stderr, "usage: hookrail-ctl <seed|migrate|retention|create-producer-key|create-admin-token> [flags]")
}

func wantsHelp(args []string) bool {
	if len(args) == 0 {
		return false
	}
	// Only check command-position args, not topic values etc.
	if args[0] == "--help" || args[0] == "-h" || args[0] == "help" {
		return true
	}
	if len(args) >= 2 {
		if args[1] == "--help" || args[1] == "-h" || args[1] == "help" {
			return true
		}
	}
	return false
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	if wantsHelp(os.Args[1:]) { // help NEVER loads config or opens the DB
		usage()
		os.Exit(0)
	}
	if os.Args[1] == "migrate" {
		cfg, err := config.Load()
		if err != nil {
			slog.Error("config", "err", err)
			os.Exit(1)
		}
		s, err := store.Open(context.Background(), cfg.DatabaseURL)
		if err != nil {
			slog.Error("store", "err", err)
			os.Exit(1)
		}
		defer s.Close()
		if err := s.Migrate(); err != nil {
			slog.Error("migrate", "err", err)
			os.Exit(1)
		}
		fmt.Println("migrations applied")
		return
	}
	if os.Args[1] == "retention" {
		fs := flag.NewFlagSet("retention", flag.ExitOnError)
		once := fs.Bool("once", false, "run one retention tick and exit")
		_ = fs.Parse(os.Args[2:])
		if !*once {
			fmt.Fprintln(os.Stderr, "usage: hookrail-ctl retention --once")
			os.Exit(2)
		}
		cfg, err := config.Load()
		if err != nil {
			slog.Error("config", "err", err)
			os.Exit(1)
		}
		s, err := store.Open(context.Background(), cfg.DatabaseURL)
		if err != nil {
			slog.Error("store", "err", err)
			os.Exit(1)
		}
		defer s.Close()
		j := &scheduler.Janitor{
			Store: s, PayloadAge: cfg.EventPayloadRetention, AttemptAge: cfg.AttemptRetention,
			Batch: cfg.RetentionBatch, TickBudget: cfg.RetentionTickBudget,
		}
		if err := j.RunOnce(context.Background()); err != nil {
			slog.Error("retention had failures", "err", err) // RunOnce aggregates job errors + budget
			os.Exit(1)
		}
		fmt.Println("retention tick complete")
		return
	}
	if os.Args[1] == "create-producer-key" {
		fs := flag.NewFlagSet("create-producer-key", flag.ExitOnError)
		name := fs.String("name", "dashboard", "human label for the key")
		_ = fs.Parse(os.Args[2:])
		cfg, err := config.Load()
		if err != nil {
			slog.Error("config", "err", err)
			os.Exit(1)
		}
		s, err := store.Open(context.Background(), cfg.DatabaseURL)
		if err != nil {
			slog.Error("store", "err", err)
			os.Exit(1)
		}
		defer s.Close()
		id, plaintext, err := s.CreateProducerKey(context.Background(), *name, []string{"*"})
		if err != nil {
			slog.Error("create-producer-key", "err", err)
			os.Exit(1)
		}
		fmt.Printf("producer_key=%s\nkey_id=%s\n", plaintext, id)
		return
	}
	if os.Args[1] == "create-admin-token" {
		fs := flag.NewFlagSet("create-admin-token", flag.ExitOnError)
		role := fs.String("role", "", "role: viewer|operator|admin")
		label := fs.String("label", "dashboard", "human label for the token")
		_ = fs.Parse(os.Args[2:])
		if *role != "viewer" && *role != "operator" && *role != "admin" {
			slog.Error("create-admin-token", "err", "role must be viewer|operator|admin")
			os.Exit(2)
		}
		cfg, err := config.Load()
		if err != nil {
			slog.Error("config", "err", err)
			os.Exit(1)
		}
		s, err := store.Open(context.Background(), cfg.DatabaseURL)
		if err != nil {
			slog.Error("store", "err", err)
			os.Exit(1)
		}
		defer s.Close()
		id, plaintext, err := s.CreateAdminToken(context.Background(), *role, *label)
		if err != nil {
			slog.Error("create-admin-token", "err", err)
			os.Exit(1)
		}
		fmt.Printf("admin_token=%s\ntoken_id=%s\n", plaintext, id)
		return
	}
	if os.Args[1] != "seed" {
		usage()
		os.Exit(2)
	}
	fs := flag.NewFlagSet("seed", flag.ExitOnError)
	url := fs.String("url", "http://test-receiver:9090/succeed", "endpoint URL to deliver to")
	topic := fs.String("topic", "demo.*", "subscription topic pattern")
	ordered := fs.Bool("ordered", false, "create ordered subscription")
	rps := fs.Float64("rps", 0, "rate_limit_rps for the subscription (0=unlimited)")
	_ = fs.Parse(os.Args[2:])

	cfg, err := config.Load()
	if err != nil {
		slog.Error("config", "err", err)
		os.Exit(1)
	}

	// registration-time SSRF check (§8) — the SAME policy the worker dials
	// with (incl. the CIDR allowlist), with DNS resolved so a hostname that
	// already points at a blocked range is rejected at write time
	ctx := context.Background()
	prefixes, err := cfg.AllowPrefixes()
	if err != nil {
		slog.Error("config", "err", err)
		os.Exit(1)
	}
	pol := ssrf.Policy{AllowHTTP: cfg.AllowHTTP, AllowCIDRs: prefixes}
	if err := pol.CheckURLResolved(ctx, *url); err != nil {
		slog.Error("endpoint url rejected by SSRF policy", "url", *url, "err", err)
		os.Exit(1)
	}

	s, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		slog.Error("store", "err", err)
		os.Exit(1)
	}
	defer s.Close()
	if err := s.Migrate(); err != nil {
		slog.Error("migrate", "err", err)
		os.Exit(1)
	}

	_, key, err := s.CreateProducerKey(ctx, "demo", []string{"*"})
	if err != nil {
		slog.Error("create key", "err", err)
		os.Exit(1)
	}
	epID, secret, err := s.CreateEndpoint(ctx, cfg.MasterKey, *url, "demo endpoint")
	if err != nil {
		slog.Error("create endpoint", "err", err)
		os.Exit(1)
	}
	var rateLimitRPS *float64
	if *rps > 0 {
		rateLimitRPS = rps
	}
	subID, err := s.CreateSubscriptionFull(ctx, store.SubInput{
		TopicPattern: *topic,
		EndpointID:   epID,
		MaxAttempts:  8,
		RateLimitRPS: rateLimitRPS,
		Ordered:      *ordered,
	})
	if err != nil {
		slog.Error("create subscription", "err", err)
		os.Exit(1)
	}

	fmt.Printf("producer_key=%s\nendpoint_id=%s\nendpoint_secret=%s\nsubscription_id=%s\n",
		key, epID, secret, subID)
}
