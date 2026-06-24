# CloudNativePG (vendored, pinned)

Postgres HA for hookrail is provided by the [CloudNativePG](https://cloudnative-pg.io)
operator. The operator install manifest is **vendored + pinned** here (not fetched from
a live URL at deploy/test time) for determinism and offline-resilience.

| Item | Value |
|---|---|
| Operator version | **v1.29.1** |
| Operator manifest | `operator-1.29.1.yaml` (from `https://github.com/cloudnative-pg/cloudnative-pg/releases/download/v1.29.1/cnpg-1.29.1.yaml`) |
| Operator image | `ghcr.io/cloudnative-pg/cloudnative-pg:1.29.1` |
| PostgreSQL operand image | `ghcr.io/cloudnative-pg/postgresql:16.10` (PG 16, matches the migrations) |
| Operator namespace | `cnpg-system` |
| Cluster CR | `../base/pg-cluster.yaml` (`Cluster/hookrail-pg`, 3 instances) |

## Replication / durability config (see design spec §3.2)

`Cluster/hookrail-pg` uses `.spec.postgresql.synchronous`:
- `method: any`, `number: 1` — quorum sync: a commit is acked once **any one** standby has it (zero RPO for accepted events; tolerant of one standby loss).
- `dataDurability: required` — block writes rather than silently drop to async if quorum can't be met.
- `failoverQuorum: true` — only promote a standby proven to hold the committed WAL (prevents a stale-node promotion losing a 202-acked event). **This field lives under `synchronous`, not at the top of `spec`.**

## Upgrading the pin

1. Download the new release manifest to `operator-<ver>.yaml`, update this README + the image refs in `scripts/k8s-e2e.sh` (pre-pull + `k3d image import`) and `scripts/pg-failover-e2e.sh`.
2. Re-validate the `Cluster` CR against the new CRD: `kubectl apply --dry-run=server -f ../base/pg-cluster.yaml`.
3. `git rm` the old `operator-<oldver>.yaml`.
