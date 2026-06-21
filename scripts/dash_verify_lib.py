"""Non-vacuous Grafana dashboard verifier.

For every panel query, the referenced metric must be (a) DECLARED in metrics.go and
(b) EMITTED at a real obs.<Metric> call site; every panel datasource uid must exist in
grafana-datasources.yaml. The metric set is DERIVED from source so the check fails on
drift, not just on invalid JSON.
"""
import json
import re
import sys
from pathlib import Path

REPO = Path(__file__).resolve().parents[1]
METRICS_SRC = REPO / "internal" / "obs" / "metrics.go"
OBS_DIR = REPO / "internal"
DS_YAML = REPO / "deploy" / "compose" / "grafana-datasources.yaml"
DASH_DIR = REPO / "deploy" / "compose" / "grafana" / "dashboards"

HIST_SUFFIXES = ("_bucket", "_sum", "_count")
METRIC_RE = re.compile(r"\bhookrail_[a-z0-9_]+")


def declared_metrics(src: Path) -> dict:
    """Map metric promName -> Go var name (e.g. hookrail_ingest_events_total -> IngestEventsTotal)."""
    text = src.read_text()
    out = {}
    for m in re.finditer(r"(\w+)\s*=\s*promauto\.New\w+\(", text):
        var = m.group(1)
        tail = text[m.end():m.end() + 400]
        nm = re.search(r'Name:\s*"(hookrail_[a-z0-9_]+)"', tail)
        if nm:
            out[nm.group(1)] = var
    if not out:
        raise SystemExit(f"no metrics parsed from {src} — oracle would be vacuous")
    return out


def emitted_vars(internal: Path) -> set:
    """obs.<Var> references anywhere in internal/ EXCEPT the declaration file."""
    refs = set()
    for f in internal.rglob("*.go"):
        if f == METRICS_SRC or f.name.endswith("_test.go"):
            continue
        for m in re.finditer(r"\bobs\.([A-Z]\w+)\b", f.read_text()):
            refs.add(m.group(1))
    return refs


def datasource_uids(yaml_path: Path) -> set:
    return set(re.findall(r"uid:\s*([A-Za-z0-9_-]+)", yaml_path.read_text()))


def metric_base(token: str) -> str:
    for suf in HIST_SUFFIXES:
        if token.endswith(suf):
            return token[: -len(suf)]
    return token


def verify(dash_dir, declared, emitted, ds_uids):
    errors, files = [], sorted(dash_dir.glob("*.json"))
    if not files:
        raise SystemExit(f"no dashboards in {dash_dir} — nothing verified")
    for f in files:
        dash = json.loads(f.read_text())
        panels = dash.get("panels", [])
        if not panels:
            errors.append(f"{f.name}: no panels (vacuous)")
        for p in panels:
            ds = (p.get("datasource") or {}).get("uid")
            if ds and ds not in ds_uids:
                errors.append(f"{f.name}: panel {p.get('title')!r}: datasource uid {ds!r} not in {DS_YAML.name}")
            n = 0
            for tgt in p.get("targets", []):
                if "expr" not in tgt:
                    continue
                n += 1
                for tok in METRIC_RE.findall(tgt["expr"]):
                    base = metric_base(tok)
                    if base not in declared:
                        errors.append(f"{f.name}: panel {p.get('title')!r}: undeclared metric {tok!r}")
                    elif declared[base] not in emitted:
                        errors.append(f"{f.name}: panel {p.get('title')!r}: metric {base!r} declared but never emitted")
            if p.get("targets") and n == 0:
                errors.append(f"{f.name}: panel {p.get('title')!r}: no expr targets")
    return errors


def main():
    dash_dir = Path(sys.argv[1]) if len(sys.argv) > 1 else DASH_DIR
    errors = verify(dash_dir, declared_metrics(METRICS_SRC), emitted_vars(OBS_DIR), datasource_uids(DS_YAML))
    if errors:
        print("\n".join(errors))
        sys.exit(1)
    print("dash-verify OK: panels map to declared+emitted metrics and real datasource uids")


if __name__ == "__main__":
    main()
