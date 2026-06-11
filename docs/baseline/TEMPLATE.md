# Hookrail P0 Baseline — <date>

**Protocol (spec §11):** payload 1KB · healthy consumers (instant 200) ·
2min warm-up + 10min sustained · generator on a separate machine.
**Hardware:** <generator machine> → <target machine, CPU/RAM> over <network>.
**Versions:** hookrail <git sha> · postgres 16 · redis 7.

| Profile | Sustained ingest (events/s) | e2e p50 | e2e p95 | e2e p99 | First-dispatch p99 | Duplicates | Stranded after drain |
|---|---|---|---|---|---|---|---|
| fan-out 1 | | | | | | | |
| fan-out 3 | | | | | | | |

Latency column = **ingest commit → consumer completion**: a conservative
proxy for §11's "202 response → consumer 2xx receipt" (the commit strictly
precedes the 202, so published numbers err larger, never smaller). All
timestamps are the target's clocks — generator time is never compared
against target time.
Duplicates come from the receiver's /stats ledger (receipts − distinct), not
from PG. Duplicate budget: 0 in steady state. Stranded must be 0 (§11).

**Honest caveats:** <anything that deviated from protocol>
