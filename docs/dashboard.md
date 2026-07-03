# Admin Dashboard

A browser-based admin dashboard (React/TypeScript SPA) served by a Go
backends-for-frontends (BFF). The BFF holds role-scoped admin tokens server-side
so they never reach the browser, authenticates **per-user accounts** mapped to a
role, issues an HMAC-signed session cookie, then proxies an allowlist of admin
routes plus one native test-event route.

## Run it

```bash
docker compose -f deploy/compose/docker-compose.yml up -d --build dashboard
# Open http://localhost:8085, log in as user "admin" / password "dev-dashboard-pw"
```

The compose provisioner mints three role-scoped tokens and writes an `admin`
users file automatically. The `dashboard` image is multi-stage and builds the SPA
itself.

## Public demo landing page

The SPA defaults `/` to `/overview`. That read-only overview is intentionally
small: it summarizes the live demo traffic with three panels for the success,
retry, and dead-letter paths, then keeps a compact recent-events table below.
It reuses the existing endpoints, deliveries, and DLQ list APIs; there is no
separate demo-only dashboard endpoint. The demo compose overlay seeds
human-readable receiver aliases (`orders-service`, `payments-service`,
`analytics-service`) so the overview can lead with service names instead of raw
endpoint IDs.

## Auth model (RBAC R2)

- **Per-user accounts.** Users come from a mounted secret file
  (`HOOKRAIL_DASHBOARD_USERS_FILE`), one `username:argon2id-hash:role` per line.
  Login takes a username + password; passwords are verified with argon2id and a
  uniform failure path (no username enumeration).
- **Role in the request, not the cookie.** The session cookie carries only a
  signed `sub` (username); the role is resolved **per request** from the live
  user file. Deleting or downgrading a user takes effect on their next request
  after a reload (see Revocation).
- **Three enforcement layers:** (1) the SPA hides controls by role (cosmetic);
  (2) the BFF enforces a per-route minimum role locally; (3) the BFF forwards a
  **role-matched** admin token and the upstream admin API re-enforces the role.
- **Role tiers** (`viewer < operator < admin`): viewer = read-only; operator =
  viewer plus replay/skip and send-test-event; admin = operator plus create/
  edit/delete endpoints & subscriptions and rotate secrets.

## Users file

One account per line; blank lines and `#` comments are ignored:

```text
alice:$argon2id$v=19$m=65536,t=2,p=4$<salt>$<hash>:admin
bob:$argon2id$v=19$m=65536,t=2,p=4$<salt>$<hash>:operator
```

Generate an entry with the `hookrail-dash-hash` helper (argon2id, canonical cost):

```bash
hookrail-dash-hash -user alice -role admin            # prompts for the password
hookrail-dash-hash -hash-only -password 's3cret'      # just the hash
```

## Role-tokens file

The BFF forwards a role-matched upstream token chosen by the caller's live role.
Provide one **minted** admin token (`hkadm_` + 48 hex) per role via
`HOOKRAIL_DASHBOARD_ROLE_TOKENS_FILE` — all three roles are required:

```text
viewer:hkadm_<token>
operator:hkadm_<token>
admin:hkadm_<token>
```

Mint tokens with the admin API (`POST /v1/admin-tokens`) or, for bootstrap, the
CLI against the database directly:

```bash
hookrail-ctl create-admin-token -role operator -label dashboard
```

### Attestation (fail-closed)

At startup and on a fixed interval the BFF **attests** each role token against
the admin API's `GET /v1/whoami` and only serves with a snapshot whose tokens
each resolve to their declared role. If a token is mislabeled, revoked, or the
admin API is unreachable, proxied `/v1` requests return `503` and `/readyz` is
not ready — the dashboard never forwards an unverified token. It recovers
automatically once attestation succeeds.

## Revocation & reload

- The BFF hot-reloads both secret files on **SIGHUP**, atomically (both files
  must parse) — users swap immediately (revocation), role tokens are re-attested
  before they go active. A reload that fails to attest leaves the dashboard
  fail-closed but keeps re-trying the new tokens.
- In Kubernetes, change the secret then `kubectl rollout restart deploy/dashboard`
  (or `kubectl exec … -- kill -HUP 1`).
- To kill **all** outstanding sessions immediately, rotate
  `HOOKRAIL_DASHBOARD_SESSION_KEY` without setting `…_SESSION_KEY_PREVIOUS`.

## Environment variables

| Env | Default | Description |
|-----|---------|-------------|
| `HOOKRAIL_DASHBOARD_USERS_FILE` | — | Path to the users file (`username:argon2id-hash:role` per line), required |
| `HOOKRAIL_DASHBOARD_ROLE_TOKENS_FILE` | — | Path to the role-tokens file (one `hkadm_` token per role), required |
| `HOOKRAIL_DASHBOARD_SESSION_KEY` | — | HMAC signing key, ≥32 bytes (required) |
| `HOOKRAIL_DASHBOARD_SESSION_KEY_PREVIOUS` | — | Previous signing key for zero-downtime rotation |
| `HOOKRAIL_PRODUCER_KEY_FILE` | — | Path to a file containing the producer key (test-event) |
| `HOOKRAIL_ADMIN_URL` | `http://admin:8082` | Internal admin API base URL |
| `HOOKRAIL_INGRESS_URL` | `http://api:8080` | Internal ingress API base URL |
| `HOOKRAIL_DASHBOARD_ADDR` | `:8085` | Listen address |
| `HOOKRAIL_DASHBOARD_SESSION_TTL` | `12h` | Session cookie TTL (Go duration) |
| `HOOKRAIL_DASHBOARD_ATTEST_INTERVAL` | `60s` | Role-token re-attestation interval (must be > 0) |
| `HOOKRAIL_DASHBOARD_INSECURE_COOKIE` | `false` | Allow cookies over plain HTTP (dev only) |

> **Breaking change (R2):** the single shared-password variable and the
> dashboard's `HOOKRAIL_ADMIN_TOKEN` are removed. Provision the users and
> role-tokens files instead. Emergency direct access to the admin API still uses
> that API's env break-glass admin token (used here only to mint the role tokens).

## Admin API roles (RBAC R1)

The admin API (`:8082`) enforces the same `viewer < operator < admin` hierarchy
on every `/v1` route; a valid token below the minimum gets `403`, a missing or
revoked token `401`. Scoped tokens are minted via `POST /v1/admin-tokens`
(returns the plaintext `hkadm_…` once, `no-store`), listed via
`GET /v1/admin-tokens`, and revoked via `DELETE /v1/admin-tokens/{id}`. Only the
SHA-256 of each token is stored; `GET /v1/whoami` returns the caller's role.

## Limits

UI role gating is cosmetic; the BFF `requireRole` check plus the upstream admin
API are the real enforcement boundary. Other documented residual risks
(forgeable `next_cursor`, test-event key exposure) are listed in the
[residual-risks section of the README](../README.md). They are documented
trade-offs, not bugs.
