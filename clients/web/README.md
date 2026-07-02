# Hookrail Dashboard (SPA)

The browser admin dashboard for Hookrail: a React + TypeScript single-page app
built with Vite, styled with the hand-authored **"Signal"** design system
(indigo accent, monospaced ULIDs, delivery-state pills). It renders live delivery
state, per-attempt timelines, and dead-letter replay/skip.

The SPA never talks to the admin API directly. It is served by a Go
backends-for-frontends (BFF) that holds role-scoped admin tokens server-side,
authenticates per-user accounts, and proxies an allowlist of admin routes. UI
role gating is cosmetic — the BFF and upstream admin API are the real boundary.
For the auth model, session cookies, and RBAC roles, see
[`../../docs/dashboard.md`](../../docs/dashboard.md).

## Develop

```bash
npm ci
npm run dev        # Vite dev server
npm run typecheck  # tsc --noEmit
npm run lint       # eslint (zero warnings tolerated)
npm run test       # vitest (unit/component)
npm run e2e        # playwright end-to-end
npm run build      # tsc -b && vite build → dist/
```

From the repo root, `make web-verify` runs typecheck + lint + test + build the
same way CI does, and `make dashboard-assets` copies the built `dist/` into
`internal/dashboard/dist` for the BFF to embed.
