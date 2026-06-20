# Admin Dashboard

A browser-based admin dashboard (React/TypeScript SPA) served by a Go
backends-for-frontends (BFF). The BFF holds the admin token server-side so it
never reaches the browser, authenticates humans with a shared password, issues
an HMAC-signed session cookie, then proxies an allowlist of admin routes plus one
native test-event route.

## Run it

```bash
make web-build
docker compose -f deploy/compose/docker-compose.yml up -d --build dashboard
# Open http://localhost:8085, log in with the password from dev config
```

Or in one command (builds the SPA, starts the stack, prints the URL):

```bash
make web-build && docker compose -f deploy/compose/docker-compose.yml up -d --build dashboard && echo "http://localhost:8085"
```

## Auth model

- The BFF holds `HOOKRAIL_ADMIN_TOKEN` in process memory and is the only thing
  that talks to the admin API. The browser never sees the token.
- Human login takes a single shared password and sets a self-contained,
  HMAC-signed session cookie with a TTL (no server-side session store).
- The BFF proxies only an explicit allowlist of admin routes, plus one native
  test-event route that injects a provisioned producer key.

## Environment variables

| Env | Default | Description |
|-----|---------|-------------|
| `HOOKRAIL_DASHBOARD_PASSWORD` | — | Shared password for human login (required, ≥16 chars) |
| `HOOKRAIL_DASHBOARD_SESSION_KEY` | — | HMAC signing key, ≥32 bytes (required) |
| `HOOKRAIL_DASHBOARD_SESSION_KEY_PREVIOUS` | — | Previous signing key for zero-downtime rotation |
| `HOOKRAIL_ADMIN_TOKEN` | — | Admin API bearer token for proxied admin calls (required) |
| `HOOKRAIL_PRODUCER_KEY_FILE` | — | Path to a file containing the producer key (written by the compose provisioner) |
| `HOOKRAIL_ADMIN_URL` | `http://admin:8082` | Internal admin API base URL |
| `HOOKRAIL_INGRESS_URL` | `http://api:8080` | Internal ingress API base URL |
| `HOOKRAIL_DASHBOARD_ADDR` | `:8085` | Listen address |
| `HOOKRAIL_DASHBOARD_SESSION_TTL` | `12h` | Session cookie TTL (Go duration) |
| `HOOKRAIL_DASHBOARD_INSECURE_COOKIE` | `false` | Allow cookies over plain HTTP (dev only) |

## Limits

The dashboard's documented residual risks — single shared password (no RBAC),
stateless cookie (no revocation), forgeable `next_cursor`, admin-token blast
radius, and test-event key exposure — are listed in the consolidated
[residual-risks section of the README](../README.md). They are documented
trade-offs, not bugs.
