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

# ports: every cited port (localhost:P, bare :P, host:P URL) must appear somewhere in the deploy
# stack files (compose published/internal, OR the k8s manifests — e.g. the Sentinel :26379 is a
# k8s-only port, never in compose). Lenient on WHICH side, strict that a wholly-bogus port (e.g.
# :9999) fails. Folds Codex "port-check too narrow".
deploy_blob = ""
for pat in ("deploy/compose/*.yml", "deploy/k8s/**/*.yaml"):
    for cf in glob.glob(os.path.join(ROOT, pat), recursive=True):
        with open(cf, encoding="utf-8") as fh:
            deploy_blob += fh.read() + "\n"
valid_ports = set(re.findall(r'\b(\d{2,5})\b', deploy_blob))
CITE_RE = re.compile(r'(?:localhost|[a-z][a-z0-9.-]*):(\d{2,5})\b|(?<![\d.]):(\d{2,5})\b')
for f, text in docs_text.items():
    for i, line in enumerate(text.splitlines(), 1):
        for m in CITE_RE.finditer(line):
            port = m.group(1) or m.group(2)
            if port and port not in valid_ports:
                err(f, i, f"cites port :{port} but no such port in deploy/compose or deploy/k8s")

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

# ---- 2d: repo-wide public-tree leak guard ----
# The authored-set scan (2c) only covers the docs this slice writes. A green verify must also
# mean the ENTIRE tracked public .md surface carries no build-process/private references — so a
# stray committed plan/notes file can never sit in the public repo behind a green check.
REPO_LEAK = [
    (re.compile(r'\b(deepseek|reasonix|ralph|boardwatch|joblens|projecty)\b', re.I), "build-process/private ref"),
    (re.compile(r'\.agent/'), ".agent/ process path"),
    (re.compile(r'\breasonix\.toml\b', re.I), "loop config ref"),
]
tracked_md = subprocess.run(["git", "ls-files", "*.md"], cwd=ROOT,
                            capture_output=True, text=True, check=True).stdout.split()
for f in tracked_md:
    try:
        text = read(f)
    except OSError:
        continue
    for i, raw in enumerate(text.splitlines(), 1):
        line = mask_allowed(raw)
        for rx, label in REPO_LEAK:
            for m in rx.finditer(line):
                err(f, i, f"public-tree leak-scan: {label} -> {m.group(0)!r}")

if errors:
    print("DOCS-VERIFY FAIL:")
    for e in errors:
        print("  " + e)
    sys.exit(1)
print(f"docs-verify OK ({len(DOC_FILES)} files)")
