# Changelog
All notable changes documented here. Honest history — early entries include design corrections.

## [Unreleased]
- Project bootstrap: module layout, lint, CI.
- P0 core: transactional ingest with idempotency (replay/409), durable
  deliveries state machine, Redis Streams dispatch with PG sweeper repair,
  CAS claims with lease takeover, SSRF-guarded HMAC-signed delivery,
  classification + full-jitter backoff with Retry-After, DLQ.
- Compose environment with Prometheus, Grafana, OTel Collector, Jaeger.
- k6 baseline protocol + report queries; e2e suite; GHCR image with SBOM.
