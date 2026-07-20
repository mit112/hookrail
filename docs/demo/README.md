# Demo

A ~30-second terminal recording of Hookrail accepting an event and delivering it,
plus a pointer to the live dashboard where retries and dead-letters play out.

> **Recording not committed yet.** Run the steps below to generate
> `hookrail-demo.gif` in this folder, then embed it here as a standard markdown
> image pointing at that file. It shows the send → `202` → delivered path; the
> **retry** and **dead-letter** stories are best seen live in the dashboard, where
> the quickstart's seeder continuously produces traffic across a healthy, a flaky,
> and a dead endpoint.

## Regenerate it

Requires [VHS](https://github.com/charmbracelet/vhs) (`brew install vhs`), Docker,
and `jq`.

```bash
export HOOKRAIL_MASTER_KEY=$(openssl rand -hex 32)
docker compose -f deploy/compose/quickstart.yml up -d
sleep 30                          # let migrations + services settle
vhs docs/demo/hookrail.tape       # writes docs/demo/hookrail-demo.gif
docker compose -f deploy/compose/quickstart.yml down -v
```

Tune the `Sleep` values in [`hookrail.tape`](hookrail.tape) to your machine's
timing, and confirm `hookrail-ctl seed` prints `producer_key=…` (the tape parses
that line).

## Live dashboard

For the full visual — succeeded deliveries, scheduled retries climbing, and dead
letters accumulating — open <http://localhost:8085> while the quickstart stack is
up. It auto-logs you in as a read-only viewer.
