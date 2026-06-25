# Hookrail — Live Cutover Demo (attended, 2026-06-24)

This is a captured record of an **attended end-to-end cutover**: the full Hookrail
stack — including both data-plane HA tiers (CloudNativePG Postgres + Redis Sentinel)
— deployed to a real Kubernetes cluster and made **publicly reachable over the
internet** through a Cloudflare Tunnel, with the complete delivery pipeline proven
from a public URL.

It is the practical counterpart to the CI suite: CI proves the manifests and chaos
oracles on ephemeral targets; this proves the operator-run cutover and public
exposure path that CI never executes.

## Topology (what actually ran)

Single-node [k3d](https://k3d.io) cluster (k3s v1.35.5), all pods `Running`:

| Tier | Workload |
|---|---|
| App (HA, 2 replicas + PDB each) | `api`, `worker`, `scheduler`, `admin`, `dashboard` |
| Datastore HA | `hookrail-pg` — CloudNativePG 3-instance synchronous-quorum cluster (`hookrail-pg-1/2/3`), `FailoverQuorum` resource active, primary = `hookrail-pg-1` |
| Queue HA | `redis-0` (master) + `redis-1` (replica) behind a 3-node Sentinel quorum (`redis-sentinel-0/1/2`); Sentinel-reported master = `redis-0` |
| Observability | `otel-collector`, `prometheus`, `jaeger` |
| Public edge | 2× `cloudflared` quick-tunnel pods (label `app: cloudflared`) → `api:8080` / `dashboard:8085` |
| Test sink | `test-receiver` (in-cluster delivery target) |

Deploy: `deploy/k8s/overlays/demo` — the CI-proven `ephemeral` job machinery
(`dev-secret`, `db-bootstrap` for app-role login, `dashboard-keygen` for the
producer key + RBAC R2 role tokens + users hash) repointed to the **public GHCR
`:main` images**, plus the quick-tunnel pods and the prod `cloudflared → app`
NetworkPolicy.

## Public smoke results (through Cloudflare → tunnel → cluster)

| Check | Request | Result |
|---|---|---|
| Ingest (authed) | `POST /v1/events` w/ producer key | **202** `{event_id, delivery_ids}` |
| Ingest (no auth) | `POST /v1/events` no bearer | **401** (auth enforced) |
| Dashboard | `GET /` | **200** (login page) |
| Dashboard login | RBAC R2 `admin` / argon2 user | **succeeds**, lands on `/endpoints` |
| Admin plane | (no tunnel route; `ClusterIP` only) | **not publicly reachable** by construction |

### Full delivery proven end-to-end via the public URL

```text
endpoint  → http://test-receiver:9090/succeed   (created via admin API)
sub       → topic e2e.*
ingest    → POST <public-ingest>/v1/events {"topic":"e2e.test",...}  → 202
poll      → GET  <public-ingest>/v1/events/<id>  → deliveries[0].state = "succeeded"
```

Public ingest → Redis queue → scheduler → worker → HTTP delivery → `succeeded`,
status queried back through the public API. First poll already `succeeded`.

## Screenshots

| | |
|---|---|
| Endpoints — live endpoint, role badge, rail nav | [`dashboard-endpoints.png`](./dashboard-endpoints.png) |
| Deliveries — the `● succeeded` state pill (signature element) | [`dashboard-deliveries.png`](./dashboard-deliveries.png) |

> **Note on the look.** During the live cutover the cluster ran the *pre-restyle*
> GHCR image, which rendered the dashboard as unstyled HTML — the SPA shipped with
> Tailwind wired up but **zero utility classes in the components**, so the build
> emitted only Tailwind's reset layer (it was never a tunnel/asset problem). The
> dashboard has since been given a real visual identity — the **"Signal"** design
> system (indigo accent, monospace ULIDs/timestamps, color-coded delivery-state
> pills). The screenshots above are from that restyled build (`clients/web`, dev
> server against the live BFF); the new look ships in the deployed image on the
> next CI build. All 56 web tests + lint stay green.

## Honest bounds (this was a demo, not production)

- **Single node** → *process-level* failover only. The CNPG quorum and Redis
  Sentinel survive a **pod/process** loss + promotion, but **not node/disk loss**
  or the host sleeping — all pods share one k3d node. Real hardware-fault
  tolerance needs a second physical node.
- **Ephemeral tunnel URLs** — `*.trycloudflare.com` hostnames are random and
  vanish when the `cloudflared` pods/cluster stop. Used because no domain was on
  Cloudflare; the committed prod path (`deploy/k8s/overlays/prod/cloudflared.yaml`)
  uses a **named tunnel + stable hostnames + token**.
- **Throwaway dev secrets** — `dev-admin-token-001`, `dev-dashboard-pw`, dev master
  key, etc. (from the `ephemeral` `dev-secret`). Demo-grade only.

## Reproduce / tear down

```bash
# bring up
k3d cluster create hookrail
kubectl apply --server-side -f deploy/k8s/cnpg/operator-1.29.1.yaml
kubectl wait --for=condition=established --timeout=120s crd/clusters.postgresql.cnpg.io
kubectl -n cnpg-system rollout status deploy/cnpg-controller-manager --timeout=240s
kubectl apply -k deploy/k8s/overlays/demo
# wait CNPG Ready + migrate/db-bootstrap/dashboard-keygen jobs, then create the 3
# dashboard secrets from the keygen log (see scripts/k8s-e2e.sh lines 60-75 for the
# exact extraction), then read the tunnel URLs:
kubectl -n hookrail logs deploy/cloudflared-ingest    | grep trycloudflare.com
kubectl -n hookrail logs deploy/cloudflared-dashboard | grep trycloudflare.com

# tear down
k3d cluster delete hookrail
```

For the **production** (stable-domain) cutover, follow `docs/deploy/k3s.md` — the
named-tunnel path with real hostnames and operator-held secrets.
