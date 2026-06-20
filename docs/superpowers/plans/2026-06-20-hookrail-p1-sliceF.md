# Hookrail P1 Slice F — README v2 (+ docs split) Implementation Plan

> **For agentic workers:** This slice is executed **attended single-pass** (NOT the autonomous Ralph
> loop). A Claude session authors the docs task-by-task, runs the VERIFY harness, gets a Codex content
> pre-gate on M-F2/M-F3, Opus self-gates, and **Mit pushes** (Claude is the only pusher per the bridge
> gate model). Steps use checkbox (`- [ ]`) syntax. The design spec is the source of truth:
> `projectX/docs/superpowers/specs/2026-06-20-hookrail-p1-sliceF-design.md`.

**Goal:** Turn the accreted 18 KB P0 README into a coherent, accurate v2 public front door; move deep
operator detail into `docs/`; add a Python-SDK section; consolidate residual risks; fix every stale
fact — all enforced by a non-vacuous `scripts/docs-verify.sh` that asserts documented claims against the
real code.

**Architecture:** Documentation-only slice + one test-tooling script. TDD-for-docs: build the verify
harness FIRST and prove it fails on the current lying README, then fix the docs to green. Order:
M-F1 (harness + docs/ split) → M-F2 (README v2 front page) → M-F3 (SDK README + CHANGELOG + CI job +
push/gate).

**Tech Stack:** Markdown; bash + python3 for `scripts/docs-verify.sh`; `npx markdownlint-cli2@0.14.0`;
GitHub Actions (new `docs` job); `gh` for CI gating.

## Global Constraints

- **Repo is PUBLIC** (`github.com/mit112/hookrail`). Nothing private may leak (see Leak Rules below).
- **No product code changes** except the single optional one-line `api/openapi.yaml:5` banner fix
  (Task 12). Slice F touches only: `*.md`, `scripts/docs-verify.sh`, `scripts/docs_verify_lib.py`,
  `.markdownlint-cli2.jsonc`, `.github/workflows/ci.yml`, and (optionally) `api/openapi.yaml:5`.
- **Mit is the only pusher.** Gate blocks on **terminal-green across ALL workflow runs for the pushed
  SHA** — `ci` (incl. the new `docs` job) AND `python` (triggered by the SDK README / openapi edit) —
  before writing `.agent/SLICEF_DONE`. Never a partial/in-progress poll (the M-E4 trap).
- **Judge by git, not exit code; a green VERIFY is vacuous if the harness is wrong** — M-F1 proves the
  harness FAILS on the current README before trusting its green.
- **`git diff --name-status origin/main..HEAD`** before any push; reject stray artifacts (binaries,
  `.agent/`, build output).
- **Shell hygiene:** Mit's zsh aliases `du/sed/curl/awk/ps` → use `/usr/bin/*` and `/bin/ps` in scripts
  and commands.
- **Leak Rules (enforced by `docs-verify.sh` leak-scan + Codex pre-gate):** no secrets/tokens/keys/DSNs
  (placeholders only); no mention of DeepSeek / Reasonix / ralph / `.agent/` / `reasonix.toml` / the
  milestone-gating loop; no projectY / boardwatch / JobLens; no Mit personal email; no internal LAN
  hosts/IPs beyond what `docs/baseline/2026-06-11.md` already publishes; never link the design specs
  (not in this repo). `github.com/mit112/hookrail`, `ghcr.io/mit112/hookrail`,
  `ghcr.io/mit112/hookrail-dashboard` are legitimate EXACT-string references and stay.
- **Corrected facts (verified against code — the docs MUST use these exact values):**
  - `HOOKRAIL_DASHBOARD_SESSION_KEY_PREVIOUS` (NOT `..._PREV`) — `internal/dashboard/config.go:42`
  - `HOOKRAIL_DASHBOARD_SESSION_TTL` default **`12h`** (NOT 24h) — `internal/dashboard/config.go:64`
  - `RETENTION_BATCH` default **`5000`** (NOT 500) — `internal/config/config.go:31,108`
  - `RETENTION_TICK_BUDGET` default **`60s`** (NOT 30s) — `internal/config/config.go:32`
  - Grafana IS in compose (`grafana/grafana:11.1.0`, `3000:3000`, datasource provisioned); what's
    deferred to Slice D is **curated dashboards**, not Grafana itself.
  - Public TLS now exists via the Cloudflare Tunnel (Slice E shipped) — the old "TLS / public exposure
    is Slice E" deferral wording is stale; what remains deferred is *direct* in-cluster TLS / WAF.

---

## File Structure

**Created:**
- `scripts/docs-verify.sh` — bash entry: build `$DOCS` from `git ls-files`, run markdownlint, then the
  python lib; exit non-zero on any failure.
- `scripts/docs_verify_lib.py` — link-check + claims-check + leak-scan (the parsing-heavy logic).
- `.markdownlint-cli2.jsonc` — lint config (line-length off, inline-HTML allowed for badges).
- `docs/deploy/k3s.md` — the k3s + Cloudflare Tunnel runbook (moved out of README, env defaults
  corrected).
- `docs/dashboard.md` — dashboard env-var table (corrected names/defaults) + auth model.
- `docs/observability.md` — Prom/OTel/Jaeger/Grafana wiring + ports.
- `docs/README.md` — docs index (links operator docs + baseline; NOT specs/plans).

**Modified:**
- `README.md` — rewritten to the lean v2 front door.
- `clients/python/README.md` — light voice/version refresh.
- `CHANGELOG.md` — add the P1 entry.
- `.github/workflows/ci.yml` — add the `docs` job.
- `api/openapi.yaml:5` — optional one-line banner fix (Task 12; out-of-scope-able).

---

# Milestone M-F1 — docs-verify harness + docs/ split (Tasks 1–6)

Build the test first, prove it red on the current README, then create the operator docs green.
**No Codex pre-gate.** Milestone VERIFY: `bash scripts/docs-verify.sh` is red on current `README.md`
before the docs/ split lands, and green on the new `docs/**` files (README intentionally still old at
M-F1 end → full run not yet green; that happens at M-F2).

### Task 1: markdownlint config

**Files:**
- Create: `.markdownlint-cli2.jsonc`

- [ ] **Step 1: Write the config**

```jsonc
{
  // Hookrail docs lint config. Relaxations chosen so prose + badges + wide tables pass.
  "config": {
    "default": true,
    "MD013": false,            // line length: off (prose + tables + long URLs)
    "MD033": false,            // inline HTML: allowed (badges, <br>)
    "MD041": false,            // first line need not be a top-level heading (badges block)
    "MD024": { "siblings_only": true }, // duplicate headings OK if not siblings
    "MD029": { "style": "ordered" }
  },
  "globs": [],                 // file list is passed explicitly by docs-verify.sh
  "gitignore": true
}
```

- [ ] **Step 2: Commit**

```bash
git add .markdownlint-cli2.jsonc
git commit -m "chore(docs): add markdownlint config for docs-verify"
```

---

### Task 2: docs-verify python lib (link-check + claims-check + leak-scan)

**Files:**
- Create: `scripts/docs_verify_lib.py`
- Test: manual run (this task's deliverable is proven by Task 3 running it against the repo)

**Interfaces:**
- Produces: a CLI `python3 scripts/docs_verify_lib.py <doc-file> [<doc-file> ...]` that reads the named
  doc files plus the repo sources and exits non-zero with `file:line` diagnostics on any failure.

- [ ] **Step 1: Write the library**

```python
#!/usr/bin/env python3
"""Non-vacuous docs verifier for Hookrail. Asserts documented claims against real code.
Checks: (2a) internal links resolve, (2b) claims-check (env names+defaults, admin routes,
make targets, ports, key paths), (2c) leak-scan. Exit non-zero on any failure."""
import glob, os, re, subprocess, sys

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))  # repo root
errors = []

def err(f, ln, msg): errors.append(f"{f}:{ln}: {msg}")

def read(path):
    with open(os.path.join(ROOT, path), encoding="utf-8") as fh:
        return fh.read()

def doc_set():
    """Tracked public doc set — single source of truth, portable (no bash globbing)."""
    files = []
    for p in ("README.md", "CHANGELOG.md", "clients/python/README.md"):
        if os.path.isfile(os.path.join(ROOT, p)):
            files.append(p)
    out = subprocess.run(["git", "ls-files", "docs"], cwd=ROOT,
                         capture_output=True, text=True, check=True).stdout
    for line in out.splitlines():
        if line.endswith(".md") and not line.startswith("docs/superpowers/plans/"):
            files.append(line)
    return files

# `--list` prints the doc set (docs-verify.sh consumes it); otherwise args are the files to check.
if "--list" in sys.argv[1:]:
    print("\n".join(doc_set()))
    sys.exit(0)
DOC_FILES = [a for a in sys.argv[1:] if not a.startswith("-")] or doc_set()

# ---- 2a: internal link check (strip #fragments; accept file OR directory) ----
LINK_RE = re.compile(r'\[[^\]]*\]\(([^)]+)\)')
for f in DOC_FILES:
    base = os.path.dirname(f)
    for i, line in enumerate(read(f).splitlines(), 1):
        for target in LINK_RE.findall(line):
            t = target.strip()
            if t.startswith(("http://", "https://", "mailto:", "#")):
                continue
            t = t.split("#", 1)[0]            # strip fragment
            if not t:
                continue
            resolved = os.path.normpath(os.path.join(ROOT, base, t))
            if os.path.isfile(resolved) or os.path.isdir(resolved):
                continue
            err(f, i, f"broken internal link -> {target}")

# ---- 2b: claims-check ----
docs_text = {f: read(f) for f in DOC_FILES}
all_docs = "\n".join(docs_text.values())

# env var NAME existence: every HOOKRAIL_*/RETENTION_* token in the docs must exist in config source
cfg_sources = read("internal/config/config.go") + read("internal/dashboard/config.go")
ENV_RE = re.compile(r'\b((?:HOOKRAIL|RETENTION)_[A-Z0-9_]+)\b')
known_env = set(ENV_RE.findall(cfg_sources))
for f, text in docs_text.items():
    for i, line in enumerate(text.splitlines(), 1):
        for name in ENV_RE.findall(line):
            if name not in known_env:
                err(f, i, f"documents unknown env var {name} (not in config source)")

# env DEFAULTS oracle — DERIVED from code (folds Codex "expand the oracle / derive from code").
# (a) auto-parse the `// VAR, default VALUE` doc-comments in internal/config/config.go;
# (b) merge dashboard defaults (config.go uses literals, no doc-comments).
# EXCLUDED from value-enforcement: vars whose docs use a different unit presentation than the code
# duration (e.g. RETENTION_EVENT_PAYLOAD_DAYS doc "30" vs code "30d", *_HOURS) — name-existence still
# applies to them; only the value compare is skipped to avoid false-positives on equivalent values.
EXCLUDE_VALUE = {"RETENTION_EVENT_PAYLOAD_DAYS", "RETENTION_ATTEMPT_DAYS", "RETENTION_IDEM_HOURS"}
DEFAULTS = {}
for m in re.finditer(r'\b([A-Z][A-Z0-9_]+),\s*default\s+([^\s(]+)', read("internal/config/config.go")):
    DEFAULTS[m.group(1)] = m.group(2).rstrip('.')
DEFAULTS.update({                                   # dashboard literals (internal/dashboard/config.go)
    "HOOKRAIL_DASHBOARD_SESSION_TTL": "12h",        # :64  c.SessionTTL = 12 * time.Hour
    "HOOKRAIL_DASHBOARD_ADDR": ":8085",             # :63
    "HOOKRAIL_DASHBOARD_INSECURE_COOKIE": "false",  # default off
    "HOOKRAIL_ADMIN_URL": "http://admin:8082",
    "HOOKRAIL_INGRESS_URL": "http://api:8080",
})
for v in EXCLUDE_VALUE:
    DEFAULTS.pop(v, None)
# parse markdown table rows: | `VAR` | `default` | ... |  (default may be backticked or plain)
ROW_RE = re.compile(r'^\|\s*`([A-Z0-9_]+)`\s*\|\s*`?([^|`]+?)`?\s*\|')
for f, text in docs_text.items():
    for i, line in enumerate(text.splitlines(), 1):
        m = ROW_RE.match(line.strip())
        if not m:
            continue
        var, claimed = m.group(1), m.group(2).strip()
        if var in DEFAULTS and claimed not in ("—", "-") and claimed != DEFAULTS[var]:
            err(f, i, f"{var} default documented as '{claimed}', code default is '{DEFAULTS[var]}'")

# admin routes: every "METHOD /v1/..." row in the docs must appear in openapi.yaml
openapi = read("api/openapi.yaml")
ROUTE_RE = re.compile(r'\b(GET|POST|PATCH|PUT|DELETE)\b[^\n|]*?(/v1/[A-Za-z0-9/{}_-]+)')
# build set of (method, normalized-path) present in openapi
oapi_paths = set()
cur_path = None
for line in openapi.splitlines():
    pm = re.match(r'^\s{2}(/v1/[^\s:]+):', line)
    if pm:
        cur_path = pm.group(1)
    mm = re.match(r'^\s{4}(get|post|patch|put|delete):', line)
    if mm and cur_path:
        oapi_paths.add((mm.group(1).upper(), re.sub(r'\{[^}]+\}', '{}', cur_path)))
def norm(p): return re.sub(r'\{[^}]+\}', '{}', p)
for f, text in docs_text.items():
    for i, line in enumerate(text.splitlines(), 1):
        if "| `/v1/" in line or "`/v1/" in line:
            m = re.search(r'\|\s*(GET|POST|PATCH|PUT|DELETE)\s*\|\s*`(/v1/[^`]+)`', line)
            if m and (m.group(1), norm(m.group(2))) not in oapi_paths:
                err(f, i, f"admin route {m.group(1)} {m.group(2)} not in api/openapi.yaml")

# make targets: every `make X` cited must exist in Makefile
makefile = read("Makefile")
make_targets = set(re.findall(r'^([a-zA-Z0-9_-]+):', makefile, re.M))
for f, text in docs_text.items():
    for i, line in enumerate(text.splitlines(), 1):
        for tgt in re.findall(r'\bmake\s+([a-z][a-z0-9-]+)\b', line):
            if tgt not in make_targets:
                err(f, i, f"cites 'make {tgt}' but no such Makefile target")

# ports: every cited port (localhost:P, bare :P, host:P URL) must appear somewhere in the compose
# stack files (published OR container/internal — e.g. scheduler:8083 is internal-only). Lenient on
# WHICH side, strict that a wholly-bogus port (e.g. :9999) fails. Folds Codex "port-check too narrow".
compose_blob = ""
for cf in glob.glob(os.path.join(ROOT, "deploy/compose/*.yml")):
    with open(cf, encoding="utf-8") as fh:
        compose_blob += fh.read() + "\n"
valid_ports = set(re.findall(r'\b(\d{2,5})\b', compose_blob))
CITE_RE = re.compile(r'(?:localhost|[a-z][a-z0-9.-]*):(\d{2,5})\b|(?<![\d.]):(\d{2,5})\b')
for f, text in docs_text.items():
    for i, line in enumerate(text.splitlines(), 1):
        for m in CITE_RE.finditer(line):
            port = m.group(1) or m.group(2)
            if port and port not in valid_ports:
                err(f, i, f"cites port :{port} but no such port in deploy/compose/*.yml")

# key paths exist
for p in ("deploy/k8s/overlays/prod", "docs/baseline", "clients/python"):
    if not os.path.exists(os.path.join(ROOT, p)):
        err("(paths)", 0, f"referenced path missing from repo: {p}")

# ---- 2c: leak-scan (fail-closed) ----
EXACT_ALLOW = ("github.com/mit112/hookrail", "ghcr.io/mit112/hookrail",
               "ghcr.io/mit112/hookrail-dashboard")
# per-example documented exemptions for legitimately-quoted dev defaults:
EXEMPT_SUBSTRINGS = ("dev-admin-token",)  # illustrative dev value if quoted in docs
FORBIDDEN = [
    (re.compile(r'\b(deepseek|reasonix|ralph|boardwatch|joblens|projecty)\b', re.I), "build-process/private ref"),
    (re.compile(r'\.agent/'), ".agent/ process path"),
    (re.compile(r'\breasonix\.toml\b', re.I), "loop config ref"),
    (re.compile(r'[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}'), "email address"),
    (re.compile(r'\bhk_[A-Za-z0-9]{20,}\b'), "real-looking producer key"),
    (re.compile(r'\b[0-9a-f]{64}\b'), "64-hex secret"),
    (re.compile(r'Bearer\s+(?!<)[A-Za-z0-9._-]{20,}'), "real-looking bearer token"),
]
def mask_allowed(line):
    """Blank out ONLY the allowlisted exact strings / exempt examples (per-span, NOT line-wide), so a
    real leak elsewhere on the same line still fires. Folds Codex 'line-wide allowlist is a hole'."""
    for a in EXACT_ALLOW:
        line = line.replace(a, " " * len(a))
    for e in EXEMPT_SUBSTRINGS:
        line = line.replace(e, " " * len(e))
    return line
for f, text in docs_text.items():
    for i, raw in enumerate(text.splitlines(), 1):
        line = mask_allowed(raw)
        for rx, label in FORBIDDEN:
            for m in rx.finditer(line):
                err(f, i, f"leak-scan: {label} -> {m.group(0)!r}")

if errors:
    print("DOCS-VERIFY FAIL:")
    for e in errors:
        print("  " + e)
    sys.exit(1)
print(f"docs-verify OK ({len(DOC_FILES)} files)")
```

- [ ] **Step 2: Commit**

```bash
git add scripts/docs_verify_lib.py
git commit -m "test(docs): add docs-verify claims/link/leak library"
```

---

### Task 3: docs-verify.sh entry + prove it RED on the current README

**Files:**
- Create: `scripts/docs-verify.sh`

**Interfaces:**
- Consumes: `scripts/docs_verify_lib.py`, `.markdownlint-cli2.jsonc`
- Produces: `bash scripts/docs-verify.sh` → runs md-lint + the python lib over the tracked doc set.

- [ ] **Step 1: Write the script**

```bash
#!/usr/bin/env bash
# Non-vacuous docs verifier. Deterministic, no Docker, CI-safe. Portable (no mapfile / bash4).
# NOTE: deliberately NOT `set -e` — both stages must run and report even if the first fails.
set -uo pipefail
cd "$(dirname "$0")/.."

# Doc set assembled by the python lib (single source of truth, portable — no bash globbing of `**`).
DOCS="$(python3 scripts/docs_verify_lib.py --list)"
echo "==> docs set:"; printf '   %s\n' $DOCS

echo "==> markdownlint"
# shellcheck disable=SC2086  # paths have no spaces; intentional word-split
npx --yes markdownlint-cli2@0.14.0 $DOCS; lint=$?

echo "==> claims/link/leak"
python3 scripts/docs_verify_lib.py $DOCS; checks=$?

if [ "$lint" -ne 0 ] || [ "$checks" -ne 0 ]; then
  echo "docs-verify: FAIL (markdownlint=$lint claims/link/leak=$checks)"
  exit 1
fi
echo "docs-verify: ALL GREEN"
```

- [ ] **Step 2: Make executable; prove the claims-check is RED on the CURRENT README — independently of lint**

The current README already fails markdownlint (e.g. `MD040`), so a whole-script red doesn't prove the
*claims-check* works. Run the python verifier directly against the current README to prove the
load-bearing check is non-vacuous:

Run: `chmod +x scripts/docs-verify.sh && python3 scripts/docs_verify_lib.py README.md`
Expected: **FAIL** with the current README's real lies, e.g.
`README.md:204: documents unknown env var HOOKRAIL_DASHBOARD_SESSION_KEY_PREV (not in config source)`,
`README.md:133: RETENTION_BATCH default documented as '500', code default is '5000'`,
`README.md:134: RETENTION_TICK_BUDGET default documented as '30s', code default is '60s'`,
`README.md:210: HOOKRAIL_DASHBOARD_SESSION_TTL default documented as '24h', code default is '12h'`.
Then run the full `bash scripts/docs-verify.sh` and confirm it also reports `FAIL` (lint + claims). This
two-step proof is the M-F1 non-vacuity gate.

- [ ] **Step 3: Commit**

```bash
git add scripts/docs-verify.sh
git commit -m "test(docs): add docs-verify entrypoint (red on current README, by design)"
```

---

### Task 4: `docs/deploy/k3s.md` — move the runbook out

**Files:**
- Create: `docs/deploy/k3s.md`
- Modify: (README runbook removed in M-F2 Task 7 — not here, to keep link targets stable)

- [ ] **Step 1: Create the runbook doc** — move the current README "Deploy to k3s (Slice E)" section
  (the 12 numbered steps + "Residual risks" list, current README lines ~249–439) verbatim into
  `docs/deploy/k3s.md` under a `# Deploy Hookrail to k3s` title. **Preserve all placeholders**
  (`INGEST_HOSTNAME`, `DASHBOARD_HOSTNAME`, `<digest>`, `<app-pw>`, `<admin-token>`, etc.). Keep the
  attended-cutover framing. The k3s residual-risks list stays here (the README's consolidated section
  will summarize + link here).

- [ ] **Step 2: Verify this file alone**

Run: `npx --yes markdownlint-cli2@0.14.0 docs/deploy/k3s.md && python3 scripts/docs_verify_lib.py docs/deploy/k3s.md`
Expected: PASS (placeholders are not real secrets; any `make`/port cited must exist).

- [ ] **Step 3: Commit**

```bash
git add docs/deploy/k3s.md
git commit -m "docs: extract k3s + Cloudflare Tunnel runbook to docs/deploy/k3s.md"
```

---

### Task 5: `docs/dashboard.md` + `docs/observability.md`

**Files:**
- Create: `docs/dashboard.md`, `docs/observability.md`

- [ ] **Step 1: Write `docs/dashboard.md`** — the full dashboard env-var table + auth model from the
  current README "Dashboard (P1 Slice C)" section, **with corrected env names/defaults**:
  `HOOKRAIL_DASHBOARD_SESSION_KEY_PREVIOUS` (not `_PREV`), `HOOKRAIL_DASHBOARD_SESSION_TTL` default
  `12h` (not `24h`). Keep the BFF/HMAC-cookie/allowlist-proxy explanation and the run instructions.

- [ ] **Step 2: Write `docs/observability.md`** — Prometheus (`localhost:9091`), Grafana
  (`localhost:3000`, datasource provisioned; "curated dashboards land in Slice D"), OTel collector,
  Jaeger (`localhost:16686`); what's scraped (api/worker/scheduler) and where traces go. Source:
  `deploy/compose/docker-compose.yml`.

- [ ] **Step 3: Verify**

Run: `python3 scripts/docs_verify_lib.py docs/dashboard.md docs/observability.md && npx --yes markdownlint-cli2@0.14.0 docs/dashboard.md docs/observability.md`
Expected: PASS (env names known; ports exist in compose; no leaks).

- [ ] **Step 4: Commit**

```bash
git add docs/dashboard.md docs/observability.md
git commit -m "docs: add dashboard + observability operator docs (corrected env defaults)"
```

---

### Task 6: `docs/README.md` index + M-F1 gate

**Files:**
- Create: `docs/README.md`

- [ ] **Step 1: Write the index** — a short docs index linking `deploy/k3s.md`, `dashboard.md`,
  `observability.md`, and `baseline/` (the directory link is valid). **Do NOT link** the design specs
  (not in this repo) or `superpowers/plans/` (build-process artifacts).

- [ ] **Step 2: Verify the new docs set passes (README still old → run on docs/ subset)**

Run: `python3 scripts/docs_verify_lib.py docs/README.md docs/deploy/k3s.md docs/dashboard.md docs/observability.md && npx --yes markdownlint-cli2@0.14.0 docs/README.md`
Expected: PASS (all internal links resolve, incl. the `baseline/` directory link).

- [ ] **Step 3: Confirm the harness is still RED on the full set (README not yet fixed)**

Run: `bash scripts/docs-verify.sh || echo "expected-red-until-M-F2"`
Expected: still FAIL on `README.md` claims (correct — M-F2 fixes it). This confirms M-F1 didn't make
the gate vacuously green.

- [ ] **Step 4: Commit**

```bash
git add docs/README.md
git commit -m "docs: add docs/ index"
```

**M-F1 gate (Opus, no Codex pre-gate):** confirm the new `docs/**` pass their checks, the harness is
provably RED on the current README, tree clean, `git diff --name-status origin/main..HEAD` shows only
the 7 intended new files. Do NOT push mid-slice unless Mit wants incremental pushes; default is to push
once at M-F3. (If pushing M-F1 separately: the `docs` CI job doesn't exist yet, so `ci` would not run
docs-verify — acceptable; full enforcement lands M-F3.)

---

# Milestone M-F2 — README v2 front page (Tasks 7–9)

Rewrite `README.md` into the lean front door; make the FULL harness green. **Codex content pre-gate
REQUIRED.** Milestone VERIFY: `bash scripts/docs-verify.sh` ALL GREEN.

### Task 7: README v2 rewrite

**Files:**
- Modify: `README.md` (full rewrite, target ~250 lines)

- [ ] **Step 1: Rewrite README** with these sections (design §3), each from its pinned source-of-truth,
  applying every Global-Constraints corrected fact:
  1. **Hero + status + badges** — replace the stale "Status: P0…" line with: "P0 core + P1: backend
     admin surface, Python SDK (PyPI), admin dashboard, and k3s deploy shipped; chaos/Grafana suite
     (Slice D) in progress." Badges: CI, PyPI `hookrail`, Apache-2.0.
  2. **Architecture** — keep the ASCII diagram; add `hookrail-admin` (:8082) + dashboard BFF (:8085).
  3. **Honest semantics** — unchanged.
  4. **Quickstart (60s)** — `make up && make seed`, curl, receiver poll; observability line worded as
     "Grafana :3000 (datasource provisioned; curated dashboards land in Slice D) · Jaeger :16686 ·
     Prometheus :9091".
  5. **Producer SDK (Python)** — `pip install hookrail`, ONE `send_event` example, link to PyPI 0.1.0
     and `clients/python/README.md`.
  6. **Dashboard** — 4–6 lines + link to `docs/dashboard.md`.
  7. **Admin API & retention** — keep the 15-row endpoint table (verified against `api/openapi.yaml`) +
     3-line retention summary; link `docs/dashboard.md` / `docs/observability.md` for depth.
  8. **Deploy (k3s)** — 6–8 lines (Cloudflare Tunnel, admin not exposed) + link `docs/deploy/k3s.md`.
  9. **Observability** — short; link `docs/observability.md`.
  10. **Security** — keep SSRF + signing + key-storage prose.
  11. **Honest limitations & residual risks (CONSOLIDATED)** — one section grouped Delivery / Admin &
      dashboard / k3s deploy; preserve every existing honesty item verbatim-in-substance; the k3s items
      may be summarized with a link to `docs/deploy/k3s.md` for the full list.
  12. **Measured baseline** — 2 lines + link `docs/baseline/`.
  13. **Roadmap & versioning** — P0 ✓; P1 A ✓ B ✓ C ✓ E ✓; D = next (chaos+Grafana); F ✓ (this);
      SemVer note; SDK `hookrail` 0.1.0; service pre-1.0.
  14. **Development / Contributing** — list each target as its own command so the claims-check validates
      every one (NOT the slashed shorthand `make test/itest/e2e/lint`): `make test`, `make itest`,
      `make e2e`, `make lint`, `make py-verify`, `make web-verify`; conventional commits; green CI
      required.
  15. **License** — Apache-2.0.
  Remove the now-duplicated inline runbook + verbose env tables (they live in `docs/` now).

- [ ] **Step 2: Run the FULL harness — expect GREEN**

Run: `bash scripts/docs-verify.sh`
Expected: `docs-verify: ALL GREEN` — md-lint clean, all internal links resolve, claims-check passes
(env names + the 4 corrected defaults + admin routes + make targets + ports), leak-scan clean.

- [ ] **Step 3: Commit**

```bash
git add README.md
git commit -m "docs: rewrite README as v2 lean front door (fixes stale status/env/Grafana/TLS facts)"
```

### Task 8: Codex content pre-gate (README) + fold

- [ ] **Step 1: Run Codex adversarial content review** (read-only, high effort, gtimeout-wrapped) over
  the new `README.md` + `docs/**` against the real repo. Prompt focus: factual accuracy vs code,
  leaks (§ Leak Rules), broken/forward claims, residual-risk completeness, marketing honesty. Log to
  `projectX/.codex-sliceF-content-review-{prompt,output}.md`.

Run (from `projectX`):
```bash
gtimeout 900 codex exec --skip-git-repo-check -c model_reasoning_effort=high \
  "$(cat .codex-sliceF-content-review-prompt.md)" > .codex-sliceF-content-review-output.md 2>&1; echo "EXIT=$?"
```
(If Codex hangs — alive but ~0 CPU growth over minutes per `/bin/ps -o pid,stat,%cpu,time,etime` — kill
and retry once.)

- [ ] **Step 2: Fold all valid findings** into README/docs; re-run `bash scripts/docs-verify.sh` green;
  amend or add a fix commit.

### Task 9: M-F2 Opus gate

- [ ] **Step 1:** Clean-worktree run of `bash scripts/docs-verify.sh` from a detached worktree of HEAD
  (`git worktree add --detach /tmp/gate-sliceF HEAD && cd /tmp/gate-sliceF && bash scripts/docs-verify.sh`)
  — confirms the harness passes on exactly what's committed (no dirty-tree masking). Clean up the
  worktree after.
- [ ] **Step 2:** `git diff --name-status origin/main..HEAD` shows only intended files. No push yet
  (push is M-F3 after CI job exists).

---

# Milestone M-F3 — SDK README + CHANGELOG + CI job + push/gate (Tasks 10–14)

**Codex content pre-gate REQUIRED (final sweep).** Milestone VERIFY: full `docs-verify.sh` + the new
`docs` CI job + the `python` matrix all terminal-green for the pushed SHA; attended quickstart + SDK
install verified.

### Task 10: SDK README refresh + CHANGELOG P1 entry

**Files:**
- Modify: `clients/python/README.md`, `CHANGELOG.md`

- [ ] **Step 1:** Light-refresh `clients/python/README.md` for voice/version consistency with v2 (keep
  0.1.0, the PyPI link, examples; ensure the `get_event` 404-masking caveat + per-worker rate-limit
  caveat remain). It's already in good shape — minimal edits.
- [ ] **Step 2:** Add a `CHANGELOG.md` entry under a new `## [P1]` heading summarizing: backend admin
  surface (A), Python SDK on PyPI (B), admin dashboard (C), k3s deploy + ops-hardening (E), docs v2 (F).
  Keep the "honest history" tone.
- [ ] **Step 3:** `bash scripts/docs-verify.sh` green (now includes the refreshed SDK README + CHANGELOG).
- [ ] **Step 4: Commit**

```bash
git add clients/python/README.md CHANGELOG.md
git commit -m "docs: refresh SDK README + add P1 CHANGELOG entry"
```

### Task 11: add the `docs` CI job

**Files:**
- Modify: `.github/workflows/ci.yml`

- [ ] **Step 1:** Add a `docs` job (ubuntu-latest, node already available via `actions/setup-node`)
  that runs `bash scripts/docs-verify.sh`. Mirror the existing jobs' checkout/setup style. The job must
  run on `push`/`pull_request` like the others (no `if: main` gate — docs-verify is hermetic/fast).

```yaml
  docs:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-node@v4
        with: { node-version: "20" }
      - run: bash scripts/docs-verify.sh
```

- [ ] **Step 2:** Validate YAML locally (`python3 -c "import yaml,sys; yaml.safe_load(open('.github/workflows/ci.yml'))"`).
- [ ] **Step 3: Commit**

```bash
git add .github/workflows/ci.yml
git commit -m "ci: add docs job running scripts/docs-verify.sh"
```

### Task 12: (optional) OpenAPI banner fix

**Files:**
- Modify: `api/openapi.yaml:5`

- [ ] **Step 1:** If Mit approves the one product-file edit (§6 #11), change the `info.description`
  "Ingress surface (P0). Admin CRUD lands in P1." → "Ingress + admin surface. Errors are RFC 7807
  problem+json." (Admin CRUD shipped in Slice A.) Otherwise SKIP and leave the banner (mark
  out-of-scope). **Note:** editing this file triggers the `python` workflow — the gate already accounts
  for it.
- [ ] **Step 2:** `make test` still green (conformance test checks schemas, not the banner) — quick
  sanity `go test ./internal/admin/ -run Conformance -count=1` if time permits.
- [ ] **Step 3: Commit** (if done)

```bash
git add api/openapi.yaml
git commit -m "docs(openapi): correct stale 'Admin CRUD lands in P1' banner"
```

### Task 13: Codex final content pre-gate + attended real-run VERIFY

- [ ] **Step 1:** Final Codex adversarial content review over the full slice diff (README + all docs +
  SDK README + CHANGELOG + openapi banner) — leak + accuracy sweep. Reuse the Task-8 prompt; log to
  `projectX/.codex-sliceF-final-review-{prompt,output}.md`. Fold valid findings; re-run docs-verify.
- [ ] **Step 2: Attended quickstart actually runs** (needs Docker): `make up && make seed`, then the
  documented `curl` POST returns `202` and the receiver poll shows the delivery. `make down -v` after.
- [ ] **Step 3: SDK install is real** (needs network): in a fresh venv, `pip install hookrail` → import
  → assert `hookrail.__version__ == "0.1.0"` (against real PyPI; published in Slice B).
- [ ] **Step 4:** Record both runs' outcomes for the Session Log.

### Task 14: push + gate on ALL workflows + DONE

- [ ] **Step 1:** Clean-worktree `bash scripts/docs-verify.sh` from a detached worktree of HEAD = green.
- [ ] **Step 2:** `git diff --name-status origin/main..HEAD` = only the Slice-F file set (no binaries,
  no `.agent/`, no build output). Strip anything stray.
- [ ] **Step 3:** **Mit/Claude pushes** `git push origin main`.
- [ ] **Step 4:** Block on terminal-green across **ALL push-triggered workflow runs for the pushed
  SHA**. Filter to `event=push` so a pre-existing PR run for the same SHA can't be mistaken for the
  post-push run (folds Codex "stale-run" MAJOR):

```bash
SHA=$(git rev-parse HEAD)
# list ONLY the push runs for this SHA (expect exactly: ci + python)
gh run list --commit "$SHA" --event push \
  --json workflowName,databaseId,status,conclusion -q '.[] | "\(.workflowName) \(.databaseId) \(.status) \(.conclusion)"'
# watch each push run id to terminal success:
gh run watch <ci-push-run-id>     --exit-status
gh run watch <python-push-run-id> --exit-status
```
Expected: the `push` runs of `ci` (all jobs incl. the new `docs` job) **and** `python` (4-version
matrix) both terminal `success`. Confirm BOTH workflows actually triggered for this SHA (the SDK
README / openapi edit triggers `python`). A flake on `proxy.golang.org`/npx → `gh run rerun --failed`
once. Do NOT write `.agent/SLICEF_DONE` until both push runs are terminal `success`.

- [ ] **Step 5:** Only after BOTH workflows are terminal-green, write the marker + Session Log.

```bash
echo "Slice F complete @ $(git rev-parse --short HEAD)" > .agent/SLICEF_DONE
```
- [ ] **Step 6:** Append the outcome to the bridge Session Log
  (`projectX/docs/reasonix/hookrail-bridge.md`) and update memory. **Slice F = last P1 slice → P1
  documentation complete.**

---

## Self-Review

**Spec coverage:** every design §3 README section → Task 7; §4 docs/ split → Tasks 4–6; §5 leak rules →
Task 2 leak-scan + Codex pre-gates (Tasks 8, 13); §6 factual fixes #1–#11 → Global Constraints +
Tasks 4/5/7/12; §7 VERIFY (md-lint/link/claims/leak) → Tasks 1–3, gate-only quickstart/SDK → Task 13;
§8 milestones → M-F1/M-F2/M-F3; gate model (all-workflows terminal-green) → Task 14. Covered.

**Placeholder scan:** no TBD/TODO; the verify harness is provided in full runnable form; corrected
values are exact; prose tasks specify source-of-truth + exact fixes + the verifying command (prose is
authored attended, gated by the harness + Codex — appropriate for a docs slice).

**Type consistency:** `scripts/docs-verify.sh` gets the doc set from
`python3 scripts/docs_verify_lib.py --list` and runs both markdownlint and the same lib on `$DOCS`;
`.markdownlint-cli2.jsonc` config name matches; the `docs` CI job runs the same entrypoint; the env
defaults oracle (`DEFAULTS`, auto-derived from `internal/config/config.go` doc-comments + a dashboard
literal map) matches the Global-Constraints corrected values.

**Folded from the Codex plan-review (2 BLOCKER + 4 MAJOR + 1 MINOR, all valid):** (B1) Task 3 proves the
claims-check RED independently of markdownlint (current README fails `MD040`); (B2) `docs-verify.sh` is
bash-3.2-portable — no `mapfile`, doc set assembled in Python, `set -e` dropped so both stages report;
(M1) defaults oracle derived from code + expanded (covers `RETENTION_ENABLED`, `HOOKRAIL_DASHBOARD_ADDR`,
`..._INSECURE_COOKIE`, `HOOKRAIL_ADMIN_URL`, `HOOKRAIL_INGRESS_URL`; unit-presentation vars
`*_DAYS`/`*_HOURS` excluded from value-compare, name-checked only); (M2) port-check handles bare `:port`
+ `host:port` URLs against all `deploy/compose/*.yml` ports; (M3) leak allowlist masks only the matched
span, not the whole line; (M4) Task 14 gates on `--event push` runs for the SHA; (Min) dev section spells
each `make` target separately. Log: `projectX/.codex-sliceF-plan-review-{prompt,output}.md`.

**Known harness caveats for the executor (not blockers):** the env/route/port regexes are pragmatic —
if the harness emits a false-positive on legitimately-formatted prose, tighten the regex in
`docs_verify_lib.py` (don't weaken a real assertion); record the change. `DEFAULTS` is a code-derived
oracle — if `internal/config` doc-comments or the dashboard literals change later, the auto-parse +
the small dashboard map keep it honest; verify it still matches after any config edit.
