# Admin Dashboard

A browser-based admin dashboard (React/TypeScript SPA) served by a Go
backends-for-frontends (BFF). The BFF holds the admin token server-side so it
never reaches the browser, authenticates humans with a shared password, issues
an HMAC-signed session cookie, then proxies an allowlist of admin routes plus one
native test-event route.

## Run it

```bash
docker compose -f deploy/compose/docker-compose.yml up -d --build dashboard
# Open http://localhost:8085, log in with the password from dev config
```

The `dashboard` image is multi-stage and builds the SPA itself, so no separate
`make web-build` is needed for the Docker path (it is only needed to run the Go
binary outside Docker). One-liner that starts the stack and prints the URL:

```bash
docker compose -f deploy/compose/docker-compose.yml up -d --build dashboard && echo "http://localhost:8085"
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
| `HOOKRAIL_PRODUCER_KEY_FILE` | — | Path to a file containing the producer key (compose writes it via the provisioner; k3s mounts it from a Secret) |
| `HOOKRAIL_ADMIN_URL` | `http://admin:8082` | Internal admin API base URL |
| `HOOKRAIL_INGRESS_URL` | `http://api:8080` | Internal ingress API base URL |
| `HOOKRAIL_DASHBOARD_ADDR` | `:8085` | Listen address |
| `HOOKRAIL_DASHBOARD_SESSION_TTL` | `12h` | Session cookie TTL (Go duration) |
| `HOOKRAIL_DASHBOARD_INSECURE_COOKIE` | `false` | Allow cookies over plain HTTP (dev only) |

## Admin API roles (RBAC R1)

The admin API (`:8082`) supports three role-scoped bearer tokens, ranked
`viewer < operator < admin`:

- **viewer** — read-only: list/get endpoints, subscriptions, deliveries, DLQ,
  and ordered keys.
- **operator** — viewer plus day-2 operations: replay a dead letter and skip a
  delivery.
- **admin** — operator plus configuration and secrets: create/update/delete
  endpoints and subscriptions, rotate secrets, and manage admin tokens.

Each route enforces a minimum role; a valid token below it gets `403`. A
missing, invalid, or revoked token gets `401`.

### Tokens

Scoped tokens are minted by an existing **admin** caller against the admin API
directly — the dashboard does not proxy token management in R1:

- `POST /v1/admin-tokens` with `{ "role": "operator", "label": "ci-replayer" }`
  returns the plaintext `hkadm_…` token **once** (the response is `no-store`).
- `GET /v1/admin-tokens` lists token metadata (never the secret).
- `DELETE /v1/admin-tokens/{id}` revokes a token; it stops working immediately.

Only the SHA-256 of each token is stored. The number of active tokens is bounded
to roughly 256 (a best-effort anti-sprawl cap; concurrent creates may admit a few
beyond it).

### Break-glass

`HOOKRAIL_ADMIN_TOKEN` remains valid as a full-**admin** bootstrap/break-glass
credential, independent of the database. The dashboard forwards this token, so
every dashboard user has admin rights until per-user dashboard roles arrive in a
later slice.

## Limits

The dashboard's documented residual risks — single shared password (no RBAC),
stateless cookie (no revocation), forgeable `next_cursor`, admin-token blast
radius, and test-event key exposure — are listed in the consolidated
[residual-risks section of the README](../README.md). They are documented
trade-offs, not bugs.
