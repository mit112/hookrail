# Contributing to Hookrail

Thanks for your interest. Hookrail is pre-1.0 and Apache-2.0 licensed. This guide
covers the local dev loop and the bar a change has to clear to merge.

## Where to start

- **New here?** Browse
  [`good first issue`](https://github.com/mit112/hookrail/labels/good%20first%20issue)
  and [`help wanted`](https://github.com/mit112/hookrail/labels/help%20wanted).
  Comment on one to claim it before you start.
- **See it run first:** `docker compose -f deploy/compose/quickstart.yml up -d`,
  then open <http://localhost:8085>. Then read [SPEC.md](SPEC.md) for the design
  and delivery guarantees.
- **Found a bug or have an idea?** Open an issue with the template. For security
  issues, use [private reporting](.github/SECURITY.md) instead.

Small, focused PRs are easiest to review and merge.

## Prerequisites

- Go (see `go.mod` for the toolchain version) and Docker (for integration, e2e,
  and chaos work).
- Node 20+ only if you're touching the dashboard SPA under `clients/web`.
- `HOOKRAIL_MASTER_KEY` must be set to run the stack — `export HOOKRAIL_MASTER_KEY=$(openssl rand -hex 32)`.

## Dev loop

The `Makefile` is the source of truth for every check CI runs. The common targets:

```bash
make build         # go build ./...
make test          # unit tests (go test -race)
make itest         # integration tests (needs Docker)
make e2e           # full-stack end-to-end
make lint          # golangci-lint
make up            # bring the compose stack up (api, workers, scheduler, PG, Redis, obs)
make seed          # mint a producer key + endpoint secret for local use
make down          # tear the stack down (drops volumes)
make chaos         # infrastructure-chaos suite (Docker; ~20-30 min)
make py-verify     # Python SDK: ruff + mypy + pytest
make web-verify    # dashboard SPA: typecheck + lint + test + build
make dash-verify   # validate the Grafana dashboards
```

Run the target(s) that cover your change locally before opening a PR. If you
change delivery semantics, the state machine, backoff, signing, or SSRF policy,
add or update the tests in the corresponding `internal/` package first.

## Commit messages

This repo uses [Conventional Commits](https://www.conventionalcommits.org/)
(`feat:`, `fix:`, `docs:`, `refactor:`, `test:`, `chore:`, ...). Keep one logical
change per commit and write the subject in the imperative mood.

## Pull requests

- **Merges require green CI.** CI runs lint, vet, and unit + integration tests on
  every PR; e2e and the image build run on `main`; chaos runs nightly. A PR does
  not merge until its checks are green.
- Keep changes focused and explain the "why" for any non-obvious decision.
- New behavior needs tests; design trade-offs that affect delivery guarantees
  should be reflected in [`SPEC.md`](SPEC.md) and the README's honest-limitations
  section.
