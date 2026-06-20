# Observability

The local compose stack ships a full metrics + tracing pipeline. Everything is
wired out of the box; nothing here needs configuration to try.

## What runs

| Component | Address | Purpose |
|-----------|---------|---------|
| Prometheus | `localhost:9091` | Scrapes service metrics |
| Grafana | `localhost:3000` | Dashboards (datasource provisioned; anonymous admin in dev) |
| OTel Collector | internal (`otel-collector:4318`, OTLP/HTTP) | Receives traces from the services |
| Jaeger | `localhost:16686` | Trace UI |

## Metrics

Prometheus scrapes three targets every 5 seconds:

- `api:8080` — the ingress API.
- `worker:8081` — the delivery workers.
- `scheduler:8083` — the sweeper + retention janitor.

Open Prometheus at `localhost:9091` to query raw series, or Grafana at
`localhost:3000` to chart them. The Grafana datasource is provisioned
automatically and points at Prometheus.

## Traces

Services export OTLP spans to the OTel Collector at `otel-collector:4318`
(`OTEL_EXPORTER_OTLP_ENDPOINT`), which forwards them to Jaeger. Browse traces at
`localhost:16686` — the ingest → claim → SSRF-guarded POST → classify path shows
up as a single trace per delivery.

## What's not here yet

Grafana is present with a provisioned datasource, but **curated dashboards** (the
ready-made panels for delivery rate, retry depth, DLQ growth, etc.) land in
Slice D alongside the chaos suite. Until then, build ad-hoc panels against the
Prometheus datasource.
