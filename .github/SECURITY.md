# Security Policy

Hookrail delivers HTTP requests to user-supplied URLs, so its security posture
matters. Thank you for helping keep it sound.

## Reporting a vulnerability

**Please do not open a public issue for security problems.**

Report privately via GitHub's
[private vulnerability reporting](https://github.com/mit112/hookrail/security/advisories/new)
(Security → Report a vulnerability). If that is unavailable, email
**sheth.mit@northeastern.edu** with the details and a way to reach you.

Please include:

- a description of the issue and its impact,
- steps to reproduce (a minimal PoC if you have one),
- affected version / commit.

You'll get an acknowledgement, and I'll keep you updated as it's triaged and
fixed. I'll credit reporters who want it once a fix ships.

## Supported versions

Hookrail is **pre-1.0**; only the latest tagged release and `main` receive
security fixes. There are no backports to older tags yet.

## Scope

The delivery data path (ingest, worker, scheduler, admin API, dashboard BFF) and
the SSRF/signing/auth code in `internal/` are in scope. The compose and k3s
deploys ship with **development** secrets and permissive settings for local use —
production hardening is documented in [docs/deploy/k3s.md](../docs/deploy/k3s.md).
Reports about the committed dev credentials in the local compose files are
expected behavior, not vulnerabilities.
