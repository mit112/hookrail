# Changelog

All notable changes documented here. Honest history — early entries include design corrections.

## [Unreleased]

## [P1]

Backend product surface, a published SDK, an admin dashboard, and a deployable
target — built in slices on top of the P0 core.

- **Admin surface (Slice A):** `hookrail-admin` CRUD / query / DLQ-replay API
  (RFC 7807 errors) and a retention janitor in `hookrail-scheduler`.
- **Python SDK (Slice B):** [`hookrail`](https://pypi.org/project/hookrail/) 0.1.0
  on PyPI — typed producer client (sync + async), delivery-status reads, and a
  webhook signature-verification helper.
- **Admin dashboard (Slice C):** React/TypeScript SPA behind a Go BFF that keeps
  the admin token server-side; password login with an HMAC-signed session cookie
  and an allowlist proxy.
- **k3s deploy (Slice E):** Kustomize base + prod overlay, multi-arch GHCR images,
  a Cloudflare Tunnel for public TLS, default-deny NetworkPolicies, and an
  attended cutover runbook. Plus ops-hardening (least-privilege DB role, server
  timeouts, narrowed error mapping).
- **Docs v2 (Slice F):** README rewritten as a lean front door, deep operator
  detail split into `docs/`, a non-vacuous `scripts/docs-verify.sh` that asserts
  documented facts against the code, and corrected stale env names/defaults.

## [P0]

- Project bootstrap: module layout, lint, CI.
- P0 core: transactional ingest with idempotency (replay/409), durable
  deliveries state machine, Redis Streams dispatch with PG sweeper repair,
  CAS claims with lease takeover, SSRF-guarded HMAC-signed delivery,
  classification + full-jitter backoff with Retry-After, DLQ.
- Compose environment with Prometheus, Grafana, OTel Collector, Jaeger.
- k6 baseline protocol + report queries; e2e suite; GHCR image with SBOM.
