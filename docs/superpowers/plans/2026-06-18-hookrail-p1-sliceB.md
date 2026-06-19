# Hookrail P1 · Slice B — hookrail-python SDK — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking. This plan is also driven autonomously by the bridge runner (`projectX/run-sliceB.sh`); the DeepSeek loop implements, **Claude gates each milestone and is the only pusher / outward authorizer**.

**Goal:** Ship `hookrail-python`, a typed, ergonomic Python SDK (sync + async) for the Hookrail public producer surface, with retry/idempotency/error handling, a receiver-side signature verifier, tests, docs, and a trusted-publishing release workflow — published to PyPI (publish attended).

**Architecture:** A `clients/python/` package in the hookrail monorepo. Pydantic v2 models hand-written to the real server shapes and guarded by a directional contract test against `api/openapi.yaml`. A pure `RetryController` state machine owns all retry/backoff/idempotency/error-mapping logic; thin sync (`Hookrail`) and async (`AsyncHookrail`) adapters over httpx only send prebuilt bytes, parse the success body, and sleep. Release via OIDC trusted publishing through protected GitHub Environments; the autonomous loop never publishes.

**Tech Stack:** Python 3.10–3.13 · uv + hatchling · httpx · pydantic>=2 · ruff · mypy `--strict` · pytest + respx · jsonschema + pyyaml (conformance) · GitHub Actions (CI + OIDC release).

**Design doc (source of truth):** `projectX/docs/superpowers/specs/2026-06-18-hookrail-p1-sliceB-design.md` (rev 3 FINAL; two Codex rounds folded). Read it before any task.

## Global Constraints

- **Package dir:** `clients/python/`. Import name `hookrail`; dist name `hookrail`. `src/` layout. Ships `py.typed`.
- **Python floor:** `requires-python = ">=3.10"`. CI matrix: 3.10, 3.11, 3.12, 3.13.
- **Runtime deps only:** `httpx>=0.27`, `pydantic>=2.7`. Dev deps (uv `dev` group): `pytest`, `respx`, `jsonschema`, `pyyaml`, `mypy`, `ruff`.
- **Lint/type:** `ruff check` + `ruff format --check` clean; `mypy --strict` clean over `src`.
- **Server facts (verified against the real code — do NOT re-derive):** ingest `POST /v1/events` → **202**, body `{event_id, delivery_ids}` (`store.IngestResult`); duplicate (same key + identical raw bytes) → 202 + header **`Idempotent-Replay: true`**; same key + different bytes → **409**; PG down → **503**. Status `GET /v1/events/{id}` → **200**, body `{event_id, topic, deliveries:[{delivery_id, state, attempts_truncated, attempts:[{attempt_no, claim_version, status, latency_ms, http_status?, error_class?}]}]}`; any store error → **404** (known server bug, §0 of the design — out of scope here). Auth always required: missing `Authorization: Bearer hk_…` → **401**. Idempotency hash is `sha256(topic ‖ 0x00 ‖ payload)` over raw bytes — **resend byte-identical body on retry**.
- **Retry predicate (mirror `internal/domain/classify.go:30`):** retryable = transport error OR status in `{408,425,429}` OR `status >= 500`. Never retry `400/401/404/409/413`. Honor `Retry-After` (never shorter; clipped to remaining deadline).
- **Signature scheme (mirror `internal/signing/signing.go`):** header `hookrail-signature: t=<unix>,v1=hex(HMAC_SHA256(secret, "<unix>.<delivery_id>." + body))`; dual-secret; skew tolerance (default 300 s).
- **Publish boundary:** the loop NEVER publishes. `reasonix.toml` must deny every upload/publish form — `*twine upload*`, `*twine*upload*`, `*python -m twine upload*`, `uv publish*`, `*uvx*publish*`, `gh workflow*`, `pip*upload*` (Task 1) — while leaving `twine check` runnable. Real PyPI is attended (Mit tags + approves the `pypi` Environment).
- **Commit discipline:** one logical change per commit, imperative mood. All paths under `clients/python/` unless noted (`reasonix.toml`, `.github/workflows/python.yml`, `api/openapi.yaml` references are repo-root).
- **VERIFY (per milestone, run from repo root):** `make py-verify` = `cd clients/python && uv run ruff check . && uv run ruff format --check . && uv run mypy src && uv run pytest -q -m "not e2e"`. M-B4 also `make py-build`; the gate also runs `make py-e2e`.

---

## Bridge milestone map (for `.agent/MILESTONE` + the SELECT prompt)

| Milestone | Tasks | `.agent/MILESTONE` | Gate | ralph knobs |
|---|---|---|---|---|
| **M-B1** | 1–5 | `TASKS: 1-5` / `VERIFY: make py-verify` | Opus | `MAX_ITERS=10 MAX_STEPS=45 BUDGET=30` |
| **M-B2** | 6–9 | `TASKS: 6-9` / `VERIFY: make py-verify` | Opus | `MAX_ITERS=10 MAX_STEPS=45 BUDGET=30` |
| **M-B3** | 10–13 | `TASKS: 10-13` / `VERIFY: make py-verify` | **Opus + Codex pre-gate** | `MAX_ITERS=12 MAX_STEPS=50 BUDGET=35` |
| **M-B4** | 14–18 | `TASKS: 14-18` / `VERIFY: make py-verify && make py-build` | Opus (gate also runs `make py-e2e`) | `MAX_ITERS=12 MAX_STEPS=50 BUDGET=35` |

All `TASKS:` are hyphenated ranges (the M6 single-number mis-parse lesson). The Codex pre-gate runs on **M-B3** only.

**Runner/preflight are bridge infra, not SDK tasks.** `projectX/run-sliceB.sh`, `.sliceB-select-prompt.md`, `.sliceB-gate-prompt.md`, and `projectX/preflight-sliceB.sh` are created once (this planning session, mirroring the Slice A versions) and live OUTSIDE this plan's task list. `preflight-sliceB.sh` additionally **asserts the four publish-deny rules from Task 1 are present in `reasonix.toml`** and writes `.agent/SLICEB_STOP` (fail-closed) if any is missing, before the first milestone runs. The plan itself only edits `reasonix.toml` (Task 1); the preflight enforces it.

**Review provenance:** this plan was reviewed by one adversarial Codex gpt-5.5 read-only round (REWORK: 3 blockers + 4 majors) and all findings folded — retry-exhaustion now wraps in `RetryError(__cause__)` via the `Stop(wrap)` action; the Go signing fixture is generated inside the module; `make py-e2e` path fixed; conformance hardened with jsonschema + nested descent + a real negative control; Retry-After clipped to the remaining deadline; publish denies cover every `twine upload` form and the release workflow only ships real PyPI on a tag. Log: `projectX/.codex-sliceB-plan-review-output.md`.

---

# Milestone M-B1 — package foundation, config, models, errors, conformance, CI

### Task 1: Scaffold the uv package, tooling, Makefile targets, and reasonix deny rules

**Files:**
- Create: `clients/python/pyproject.toml`, `clients/python/src/hookrail/__init__.py`, `clients/python/src/hookrail/py.typed`, `clients/python/README.md` (stub), `clients/python/tests/__init__.py`, `clients/python/tests/test_smoke.py`
- Modify: `Makefile` (add `py-*` targets), `reasonix.toml` (deny rules), `.gitignore` (ignore `clients/python/dist/`, `.venv`)

**Interfaces:**
- Produces: the `hookrail` package importable via `uv run python -c "import hookrail"`; `__version__: str`; `make py-verify` / `make py-build` / `make py-e2e` targets.

- [ ] **Step 1: Create `clients/python/pyproject.toml`**

```toml
[project]
name = "hookrail"
version = "0.1.0"
description = "Python client for Hookrail — durable, at-least-once webhook delivery."
readme = "README.md"
requires-python = ">=3.10"
license = "Apache-2.0"
authors = [{ name = "mit112" }]
keywords = ["webhooks", "hookrail", "events", "delivery"]
dependencies = ["httpx>=0.27", "pydantic>=2.7"]

[project.urls]
Homepage = "https://github.com/mit112/hookrail"
Source = "https://github.com/mit112/hookrail/tree/main/clients/python"

[build-system]
requires = ["hatchling"]
build-backend = "hatchling.build"

[tool.hatch.build.targets.wheel]
packages = ["src/hookrail"]

[dependency-groups]
dev = ["pytest>=8", "respx>=0.21", "jsonschema>=4", "pyyaml>=6", "mypy>=1.10", "ruff>=0.6"]

[tool.ruff]
line-length = 100
src = ["src", "tests"]

[tool.ruff.lint]
select = ["E", "F", "I", "UP", "B", "SIM"]

[tool.mypy]
strict = true
python_version = "3.10"
files = ["src"]

[tool.pytest.ini_options]
testpaths = ["tests"]
markers = ["e2e: live tests against a running compose stack (opt-in)"]
addopts = "-ra"
```

- [ ] **Step 2: Create the package marker files**

`clients/python/src/hookrail/py.typed` → empty file.
`clients/python/src/hookrail/__init__.py`:

```python
"""hookrail — Python client for the Hookrail webhook delivery service."""

__version__ = "0.1.0"

__all__ = ["__version__"]
```

`clients/python/tests/__init__.py` → empty. `clients/python/README.md` → one-line stub `# hookrail-python` (fleshed out in Task 15).

- [ ] **Step 3: Write the smoke test** `clients/python/tests/test_smoke.py`

```python
import hookrail


def test_version_is_a_string() -> None:
    assert isinstance(hookrail.__version__, str)
    assert hookrail.__version__.count(".") == 2
```

- [ ] **Step 4: Add Makefile targets** (append to `Makefile`)

```makefile
.PHONY: py-lint py-typecheck py-test py-verify py-build py-e2e
py-lint: ; cd clients/python && uv run ruff check . && uv run ruff format --check .
py-typecheck: ; cd clients/python && uv run mypy src
py-test: ; cd clients/python && uv run pytest -q -m "not e2e"
py-verify: py-lint py-typecheck py-test
py-build: ; cd clients/python && uv build && uv run --with twine python -m twine check dist/* && bash scripts/py-install-smoke.sh
py-e2e: ; bash clients/python/scripts/py-e2e.sh
```

`twine` is invoked via `uv run --with twine python -m twine check` (ephemeral; not a project dep). The
scripts `clients/python/scripts/py-install-smoke.sh` and `clients/python/scripts/py-e2e.sh` are created in
M-B4 (Tasks 16/18); until then `py-build`/`py-e2e` are not invoked by per-iteration VERIFY (only `make
py-verify` is). Note `py-e2e` uses the explicit `clients/python/scripts/...` path (it runs from the repo
root with no `cd`).

- [ ] **Step 5: Verify the reasonix publish-deny rules are present** — `reasonix.toml` is **gitignored and bridge-managed** (added once as runner infra and asserted by `preflight-sliceB.sh` before any milestone runs, fail-closed). Confirm the `[permissions] deny = [...]` array already contains these (added during runner setup); if any is missing, add it — but do NOT commit `reasonix.toml` (it is gitignored):

```toml
  "Bash(*twine upload*)",
  "Bash(*twine*upload*)",
  "Bash(*python -m twine upload*)",
  "Bash(uv publish*)",
  "Bash(*uvx*publish*)",
  "Bash(gh workflow*)",
  "Bash(pip*upload*)",
```

These deny upload/publish in any invocation form while leaving the harmless `twine check` used by `py-build` runnable (do NOT deny bare `twine*`).

- [ ] **Step 6: Update `.gitignore`** — append:

```
clients/python/dist/
clients/python/.venv/
clients/python/**/__pycache__/
```

- [ ] **Step 7: Sync and run the smoke test**

Run: `cd clients/python && uv sync && uv run pytest -q && uv run ruff check . && uv run mypy src`
Expected: 1 passed; ruff clean; mypy `Success: no issues found`.

- [ ] **Step 8: Commit**

```bash
git add clients/python Makefile .gitignore
git commit -m "feat(py-sdk): scaffold hookrail-python package and tooling"
```

(`reasonix.toml` is gitignored — do NOT add it; its publish denies are bridge-managed and preflight-asserted.)

---

### Task 2: Error hierarchy (`errors.py`)

**Files:**
- Create: `clients/python/src/hookrail/errors.py`, `clients/python/tests/test_errors.py`

**Interfaces:**
- Produces: `HookrailError`, `HookrailConfigError`, `HookrailConnectionError`, `HookrailTimeoutError`, `HookrailAPIError(status:int, problem:Problem|None)`, `BadRequestError`, `AuthenticationError`, `NotFoundError`, `ConflictError`, `PayloadTooLargeError`, `RateLimitError(retry_after:float|None)`, `ServerError`, `RetryError`. (`Problem` imported from `models` — Task 3; to avoid a cycle, `errors.py` takes `problem: object | None` typed via `TYPE_CHECKING`.)

- [ ] **Step 1: Write the failing test** `clients/python/tests/test_errors.py`

```python
import pytest

from hookrail.errors import (
    AuthenticationError,
    HookrailAPIError,
    HookrailError,
    RateLimitError,
    RetryError,
    ServerError,
)


def test_api_errors_subclass_base_and_carry_status() -> None:
    err = AuthenticationError(status=401, problem=None)
    assert isinstance(err, HookrailAPIError)
    assert isinstance(err, HookrailError)
    assert err.status == 401


def test_rate_limit_error_carries_retry_after() -> None:
    err = RateLimitError(status=429, problem=None, retry_after=2.0)
    assert err.retry_after == 2.0


def test_server_error_is_api_error() -> None:
    assert issubclass(ServerError, HookrailAPIError)


def test_retry_error_wraps_cause() -> None:
    cause = ServerError(status=503, problem=None)
    err = RetryError("exhausted")
    err.__cause__ = cause
    assert isinstance(err, HookrailError)
    assert err.__cause__ is cause


def test_str_never_leaks_internals() -> None:
    err = HookrailAPIError(status=400, problem=None)
    assert "400" in str(err)
```

- [ ] **Step 2: Run to verify it fails** — Run: `cd clients/python && uv run pytest tests/test_errors.py -q` · Expected: FAIL (module `hookrail.errors` not found).

- [ ] **Step 3: Implement** `clients/python/src/hookrail/errors.py`

```python
"""Typed exception hierarchy for the Hookrail SDK."""

from __future__ import annotations

from typing import TYPE_CHECKING

if TYPE_CHECKING:
    from hookrail.models import Problem


class HookrailError(Exception):
    """Base class for every error raised by the SDK."""


class HookrailConfigError(ValueError, HookrailError):
    """Invalid client construction (e.g. missing api_key, bad URL/timeout)."""


class HookrailConnectionError(HookrailError):
    """The request never produced a response (connect/DNS/TLS failure)."""


class HookrailTimeoutError(HookrailConnectionError):
    """A connect/read/write timeout."""


class HookrailAPIError(HookrailError):
    """The server returned an HTTP error response."""

    def __init__(self, status: int, problem: Problem | None) -> None:
        self.status = status
        self.problem = problem
        detail = ""
        if problem is not None:
            detail = " ".join(p for p in (problem.title, problem.detail) if p)
        super().__init__(f"hookrail API error {status}: {detail}".rstrip(": ").rstrip())


class BadRequestError(HookrailAPIError):
    """400 — malformed request."""


class AuthenticationError(HookrailAPIError):
    """401 — missing or invalid producer key."""


class NotFoundError(HookrailAPIError):
    """404 — unknown event id (see design §0: may also mask a backend outage)."""


class ConflictError(HookrailAPIError):
    """409 — idempotency key reused with a different body."""


class PayloadTooLargeError(HookrailAPIError):
    """413 — payload exceeds the server limit."""


class RateLimitError(HookrailAPIError):
    """429 — too many requests."""

    def __init__(self, status: int, problem: Problem | None, retry_after: float | None = None) -> None:
        self.retry_after = retry_after
        super().__init__(status, problem)


class ServerError(HookrailAPIError):
    """5xx — transient server error."""


class RetryError(HookrailError):
    """Retries (or the total deadline) were exhausted; the last error is __cause__."""
```

- [ ] **Step 4: Run to verify it passes** — Run: `cd clients/python && uv run pytest tests/test_errors.py -q && uv run mypy src` · Expected: PASS; mypy clean.

- [ ] **Step 5: Commit**

```bash
git add clients/python/src/hookrail/errors.py clients/python/tests/test_errors.py
git commit -m "feat(py-sdk): typed error hierarchy"
```

---

### Task 3: Models (`models.py`)

**Files:**
- Create: `clients/python/src/hookrail/models.py`, `clients/python/tests/test_models.py`

**Interfaces:**
- Produces: `DeliveryState` (Enum), `Attempt`, `Delivery`, `EventAccepted`, `EventStatus`, `Problem` (Pydantic v2 `BaseModel`s). `Delivery.state` is `DeliveryState | str` (unknown values kept as raw str). `EventAccepted.replayed: bool` (excluded from serialization).

- [ ] **Step 1: Write the failing test** `clients/python/tests/test_models.py`

```python
from hookrail.models import (
    Attempt,
    Delivery,
    DeliveryState,
    EventAccepted,
    EventStatus,
    Problem,
)


def test_event_accepted_parses_and_excludes_replayed_from_dump() -> None:
    ea = EventAccepted.model_validate({"event_id": "ev_1", "delivery_ids": ["d1", "d2"]})
    assert ea.event_id == "ev_1"
    assert ea.delivery_ids == ["d1", "d2"]
    assert ea.replayed is False
    assert "replayed" not in ea.model_dump()


def test_event_status_known_state_becomes_enum() -> None:
    es = EventStatus.model_validate(
        {
            "event_id": "ev_1",
            "topic": "orders.created",
            "deliveries": [
                {
                    "delivery_id": "d1",
                    "state": "succeeded",
                    "attempts_truncated": False,
                    "attempts": [
                        {"attempt_no": 1, "claim_version": 7, "status": "succeeded", "latency_ms": 12}
                    ],
                }
            ],
        }
    )
    d = es.deliveries[0]
    assert d.state is DeliveryState.succeeded
    assert d.attempts[0].http_status is None


def test_unknown_state_is_kept_as_raw_string() -> None:
    d = Delivery.model_validate(
        {"delivery_id": "d1", "state": "quantum_superposition", "attempts_truncated": False, "attempts": []}
    )
    assert d.state == "quantum_superposition"
    assert not isinstance(d.state, DeliveryState)


def test_attempt_requires_core_fields() -> None:
    a = Attempt.model_validate(
        {"attempt_no": 2, "claim_version": 9, "status": "failed", "latency_ms": 30, "http_status": 503}
    )
    assert a.http_status == 503


def test_problem_is_lenient() -> None:
    p = Problem.model_validate({"title": "bad", "status": 400})
    assert p.status == 400
    assert p.type is None
```

- [ ] **Step 2: Run to verify it fails** — Run: `cd clients/python && uv run pytest tests/test_models.py -q` · Expected: FAIL (no module).

- [ ] **Step 3: Implement** `clients/python/src/hookrail/models.py`

```python
"""Pydantic v2 models mirroring the Hookrail producer surface (see design §3)."""

from __future__ import annotations

from enum import Enum
from typing import Annotated, Any

from pydantic import BaseModel, BeforeValidator, ConfigDict, Field


class DeliveryState(str, Enum):
    pending = "pending"
    in_flight = "in_flight"
    retry_scheduled = "retry_scheduled"
    succeeded = "succeeded"
    dead_lettered = "dead_lettered"
    cancelled = "cancelled"


def _coerce_state(value: Any) -> Any:
    """Known value -> enum member; unknown -> raw str (forward compatible)."""
    if isinstance(value, DeliveryState):
        return value
    if isinstance(value, str):
        try:
            return DeliveryState(value)
        except ValueError:
            return value
    return value


# Known states become the enum; unknown future states stay as plain strings.
StateField = Annotated[DeliveryState | str, BeforeValidator(_coerce_state)]


class Attempt(BaseModel):
    model_config = ConfigDict(extra="allow")
    attempt_no: int
    claim_version: int
    status: str
    latency_ms: int
    http_status: int | None = None
    error_class: str | None = None


class Delivery(BaseModel):
    model_config = ConfigDict(extra="allow")
    delivery_id: str
    state: StateField
    attempts_truncated: bool
    attempts: list[Attempt] = Field(default_factory=list)


class EventAccepted(BaseModel):
    model_config = ConfigDict(extra="allow")
    event_id: str
    delivery_ids: list[str] = Field(default_factory=list)
    replayed: bool = Field(default=False, exclude=True)


class EventStatus(BaseModel):
    model_config = ConfigDict(extra="allow")
    event_id: str
    topic: str
    deliveries: list[Delivery] = Field(default_factory=list)


class Problem(BaseModel):
    model_config = ConfigDict(extra="allow")
    type: str | None = None
    title: str | None = None
    status: int | None = None
    detail: str | None = None
```

- [ ] **Step 4: Run to verify it passes** — Run: `cd clients/python && uv run pytest tests/test_models.py -q && uv run mypy src` · Expected: PASS; mypy clean. (If mypy flags the `DeliveryState | str` union under `BeforeValidator`, keep the `Annotated[...]` alias — it is the supported Pydantic v2 pattern; do NOT weaken to `Any`.)

- [ ] **Step 5: Commit**

```bash
git add clients/python/src/hookrail/models.py clients/python/tests/test_models.py
git commit -m "feat(py-sdk): pydantic models with forward-compatible delivery state"
```

---

### Task 4: Client configuration (`_config.py`)

**Files:**
- Create: `clients/python/src/hookrail/_config.py`, `clients/python/tests/test_config.py`

**Interfaces:**
- Produces: `ClientConfig` dataclass with `resolve(api_key, base_url, timeout, user_agent) -> ClientConfig`; raises `HookrailConfigError` when no api_key (param or `HOOKRAIL_API_KEY`) and on bad URL/timeout. Fields: `base_url:str`, `api_key:str`, `timeout:httpx.Timeout`, `user_agent:str`. Consumed by Tasks 10/11.

- [ ] **Step 1: Write the failing test** `clients/python/tests/test_config.py`

```python
import httpx
import pytest

from hookrail._config import ClientConfig
from hookrail.errors import HookrailConfigError


def test_param_beats_env(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setenv("HOOKRAIL_API_KEY", "hk_env")
    monkeypatch.setenv("HOOKRAIL_BASE_URL", "http://env:8080")
    cfg = ClientConfig.resolve(api_key="hk_param", base_url="http://param:8080")
    assert cfg.api_key == "hk_param"
    assert cfg.base_url == "http://param:8080"


def test_env_used_when_no_param(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setenv("HOOKRAIL_API_KEY", "hk_env")
    monkeypatch.delenv("HOOKRAIL_BASE_URL", raising=False)
    cfg = ClientConfig.resolve()
    assert cfg.api_key == "hk_env"
    assert cfg.base_url == "http://localhost:8080"


def test_missing_key_raises(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.delenv("HOOKRAIL_API_KEY", raising=False)
    with pytest.raises(HookrailConfigError):
        ClientConfig.resolve()


def test_bad_base_url_raises(monkeypatch: pytest.MonkeyPatch) -> None:
    with pytest.raises(HookrailConfigError):
        ClientConfig.resolve(api_key="hk_x", base_url="notaurl")


def test_user_agent_default_contains_version() -> None:
    cfg = ClientConfig.resolve(api_key="hk_x")
    assert cfg.user_agent.startswith("hookrail-python/")
```

- [ ] **Step 2: Run to verify it fails** — Run: `cd clients/python && uv run pytest tests/test_config.py -q` · Expected: FAIL (no module).

- [ ] **Step 3: Implement** `clients/python/src/hookrail/_config.py`

```python
"""Client configuration with param > env > default resolution (design §2.1)."""

from __future__ import annotations

import os
from dataclasses import dataclass

import httpx

from hookrail import __version__
from hookrail.errors import HookrailConfigError

_DEFAULT_BASE_URL = "http://localhost:8080"
_DEFAULT_TIMEOUT = httpx.Timeout(connect=5.0, read=10.0, write=10.0, pool=5.0)


@dataclass(frozen=True)
class ClientConfig:
    base_url: str
    api_key: str
    timeout: httpx.Timeout
    user_agent: str

    @classmethod
    def resolve(
        cls,
        api_key: str | None = None,
        base_url: str | None = None,
        timeout: httpx.Timeout | None = None,
        user_agent: str | None = None,
    ) -> ClientConfig:
        key = api_key or os.environ.get("HOOKRAIL_API_KEY")
        if not key:
            raise HookrailConfigError(
                "api_key is required (pass api_key=... or set HOOKRAIL_API_KEY)"
            )
        url = (base_url or os.environ.get("HOOKRAIL_BASE_URL") or _DEFAULT_BASE_URL).rstrip("/")
        parsed = httpx.URL(url)
        if parsed.scheme not in ("http", "https") or not parsed.host:
            raise HookrailConfigError(f"invalid base_url: {url!r}")
        return cls(
            base_url=url,
            api_key=key,
            timeout=timeout or _DEFAULT_TIMEOUT,
            user_agent=user_agent or f"hookrail-python/{__version__}",
        )
```

- [ ] **Step 4: Run to verify it passes** — Run: `cd clients/python && uv run pytest tests/test_config.py -q && uv run mypy src` · Expected: PASS; mypy clean.

- [ ] **Step 5: Commit**

```bash
git add clients/python/src/hookrail/_config.py clients/python/tests/test_config.py
git commit -m "feat(py-sdk): client config resolution and validation"
```

---

### Task 5: Directional contract conformance test + Python CI workflow

**Files:**
- Create: `clients/python/tests/test_conformance.py`, `.github/workflows/python.yml`

**Interfaces:**
- Consumes: `models` (Task 3). Produces: a CI job that runs `make py-verify`-equivalent on the 3.10–3.13 matrix.

- [ ] **Step 1: Write the conformance test** `clients/python/tests/test_conformance.py`

```python
"""Directional + schema-validating contract test. Every field the OpenAPI contract declares for a
producer response must exist on the SDK model (model may be richer than the stale doc), a representative
model dump must validate against the contract schema, an invalid dump must FAIL it (non-vacuous), and the
SDK must carry the documented server-only fields (attempts_truncated / structured attempts)."""

from __future__ import annotations

from pathlib import Path

import jsonschema
import pytest
import yaml

from hookrail.models import (
    Attempt,
    Delivery,
    EventAccepted,
    EventStatus,
)

_OPENAPI = Path(__file__).parents[3] / "api" / "openapi.yaml"


def _load() -> dict:
    if not _OPENAPI.exists():
        pytest.skip(f"openapi.yaml not found at {_OPENAPI} (standalone install)")
    return yaml.safe_load(_OPENAPI.read_text())


def _schema(spec: dict, path: str, method: str, code: str) -> dict:
    return spec["paths"][path][method]["responses"][code]["content"]["application/json"]["schema"]


def _props(schema: dict) -> set[str]:
    return set(schema.get("properties", {}))


def test_event_accepted_covers_and_validates_against_202() -> None:
    schema = _schema(_load(), "/v1/events", "post", "202")
    missing = _props(schema) - set(EventAccepted.model_fields)
    assert not missing, f"EventAccepted is missing contract fields: {missing}"
    instance = EventAccepted(event_id="ev1", delivery_ids=["d1"]).model_dump()
    jsonschema.validate(instance, schema)  # representative dump must satisfy the contract


def test_event_status_covers_and_descends_into_deliveries() -> None:
    spec = _load()
    schema = _schema(spec, "/v1/events/{id}", "get", "200")
    assert not _props(schema) - set(EventStatus.model_fields)
    # nested: every field the contract declares for a delivery item must exist on Delivery
    item_schema = schema["properties"]["deliveries"]["items"]
    assert not _props(item_schema) - set(Delivery.model_fields)
    instance = EventStatus(
        event_id="ev1",
        topic="orders.created",
        deliveries=[
            Delivery(delivery_id="d1", state="succeeded", attempts_truncated=False,
                     attempts=[Attempt(attempt_no=1, claim_version=2, status="succeeded", latency_ms=5)])
        ],
    ).model_dump()
    jsonschema.validate(instance, schema)


def test_negative_control_invalid_dump_fails_schema() -> None:
    schema = _schema(_load(), "/v1/events", "post", "202")
    with pytest.raises(jsonschema.ValidationError):
        jsonschema.validate({"event_id": 123, "delivery_ids": "not-a-list"}, schema)  # wrong types


def test_models_carry_documented_server_only_fields() -> None:
    # The live server emits these even though the ingress OpenAPI schema is stale (design §2 divergence).
    assert "attempts_truncated" in Delivery.model_fields
    for field in ("attempt_no", "claim_version", "status", "latency_ms"):
        assert field in Attempt.model_fields
```

Add `jsonschema` to the `[dependency-groups] dev` list in `pyproject.toml` if not already present (Task 1 included it).

- [ ] **Step 2: Run to verify it passes** — Run: `cd clients/python && uv run pytest tests/test_conformance.py -q` · Expected: PASS (4 passed). If a KeyError occurs, the OpenAPI path/response keys differ — read `api/openapi.yaml` (paths `/v1/events` 202 and `/v1/events/{id}` 200) and fix the lookup; do NOT weaken the assertion. If jsonschema rejects an OpenAPI 3.0 keyword (e.g. a stray `nullable`), strip OpenAPI-only keys before validating rather than skipping the check.

- [ ] **Step 3: Create `.github/workflows/python.yml`**

```yaml
name: python
on:
  push:
    branches: [main]
    paths: ["clients/python/**", "api/openapi.yaml", "Makefile", "internal/signing/**", ".github/workflows/python.yml"]
  pull_request:
    paths: ["clients/python/**", "api/openapi.yaml", "Makefile", "internal/signing/**", ".github/workflows/python.yml"]
jobs:
  python:
    runs-on: ubuntu-latest
    strategy:
      matrix:
        python-version: ["3.10", "3.11", "3.12", "3.13"]
    defaults:
      run:
        working-directory: clients/python
    steps:
      - uses: actions/checkout@v4
      - uses: astral-sh/setup-uv@v5
      - run: uv python install ${{ matrix.python-version }}
      - run: uv sync --python ${{ matrix.python-version }}
      - run: uv run ruff check .
      - run: uv run ruff format --check .
      - run: uv run mypy src
      - run: uv run pytest -q -m "not e2e"
```

- [ ] **Step 4: Verify the whole milestone** — Run: `make py-verify` (from repo root) · Expected: ruff clean, mypy clean, all tests pass.

- [ ] **Step 5: Commit**

```bash
git add clients/python/tests/test_conformance.py .github/workflows/python.yml
git commit -m "feat(py-sdk): directional openapi conformance test and python CI"
```

**M-B1 VERIFY:** `make py-verify`.

---

# Milestone M-B2 — the retry/idempotency state machine (pure core)

### Task 6: `RetryPolicy` (`_transport.py` part 1)

**Files:**
- Create: `clients/python/src/hookrail/_transport.py`, `clients/python/tests/test_transport.py`

**Interfaces:**
- Produces: `RetryPolicy` frozen dataclass (`max_retries:int=3, base:float=0.2, cap:float=10.0, max_elapsed:float=30.0, respect_retry_after:bool=True`), validating finite/positive + `cap>=base` + `max_elapsed>=cap` in `__post_init__`, raising `HookrailConfigError`.

- [ ] **Step 1: Write the failing test** (start `clients/python/tests/test_transport.py`)

```python
import math

import pytest

from hookrail._transport import RetryPolicy
from hookrail.errors import HookrailConfigError


def test_defaults_are_valid() -> None:
    p = RetryPolicy()
    assert p.max_retries == 3 and p.base == 0.2 and p.cap == 10.0 and p.max_elapsed == 30.0


@pytest.mark.parametrize(
    "kwargs",
    [
        {"max_retries": -1},
        {"base": 0},
        {"cap": 0.1, "base": 0.2},  # cap < base
        {"max_elapsed": 1.0, "cap": 10.0},  # max_elapsed < cap
        {"base": math.nan},
        {"cap": math.inf},
    ],
)
def test_invalid_policies_raise(kwargs: dict) -> None:
    with pytest.raises(HookrailConfigError):
        RetryPolicy(**kwargs)
```

- [ ] **Step 2: Run to verify it fails** — Run: `cd clients/python && uv run pytest tests/test_transport.py -q` · Expected: FAIL (no module).

- [ ] **Step 3: Implement** (start `clients/python/src/hookrail/_transport.py`)

```python
"""Pure retry/idempotency/error-mapping state machine (design §4)."""

from __future__ import annotations

import math
from dataclasses import dataclass

from hookrail.errors import HookrailConfigError


def _finite_positive(name: str, value: float) -> None:
    if not math.isfinite(value) or value <= 0:
        raise HookrailConfigError(f"{name} must be finite and > 0, got {value!r}")


@dataclass(frozen=True)
class RetryPolicy:
    max_retries: int = 3
    base: float = 0.2
    cap: float = 10.0
    max_elapsed: float = 30.0
    respect_retry_after: bool = True

    def __post_init__(self) -> None:
        if self.max_retries < 0:
            raise HookrailConfigError(f"max_retries must be >= 0, got {self.max_retries}")
        _finite_positive("base", self.base)
        _finite_positive("cap", self.cap)
        _finite_positive("max_elapsed", self.max_elapsed)
        if self.cap < self.base:
            raise HookrailConfigError(f"cap ({self.cap}) must be >= base ({self.base})")
        if self.max_elapsed < self.cap:
            raise HookrailConfigError(
                f"max_elapsed ({self.max_elapsed}) must be >= cap ({self.cap})"
            )
```

- [ ] **Step 4: Run to verify it passes** — Run: `cd clients/python && uv run pytest tests/test_transport.py -q && uv run mypy src` · Expected: PASS; mypy clean.

- [ ] **Step 5: Commit**

```bash
git add clients/python/src/hookrail/_transport.py clients/python/tests/test_transport.py
git commit -m "feat(py-sdk): RetryPolicy with strict validation"
```

---

### Task 7: `is_retryable` + `parse_retry_after`

**Files:**
- Modify: `clients/python/src/hookrail/_transport.py`, `clients/python/tests/test_transport.py`

**Interfaces:**
- Produces: `is_retryable(status: int | None) -> bool`; `parse_retry_after(value: str | None, now: float) -> float | None`.

- [ ] **Step 1: Add failing tests** (append to `tests/test_transport.py`)

```python
from email.utils import format_datetime
from datetime import datetime, timezone

from hookrail._transport import is_retryable, parse_retry_after


@pytest.mark.parametrize("status,expected", [
    (200, False), (400, False), (401, False), (404, False), (409, False), (413, False),
    (408, True), (425, True), (429, True), (500, True), (502, True), (503, True), (504, True), (599, True),
])
def test_is_retryable(status: int, expected: bool) -> None:
    assert is_retryable(status) is expected


def test_retry_after_seconds() -> None:
    assert parse_retry_after("2", now=1000.0) == 2.0


def test_retry_after_garbage_is_none() -> None:
    assert parse_retry_after("soon", now=1000.0) is None
    assert parse_retry_after(None, now=1000.0) is None


def test_retry_after_http_date_uses_injected_now() -> None:
    future = datetime(2030, 1, 1, 0, 0, 30, tzinfo=timezone.utc)
    now = datetime(2030, 1, 1, 0, 0, 0, tzinfo=timezone.utc).timestamp()
    secs = parse_retry_after(format_datetime(future, usegmt=True), now=now)
    assert secs is not None and abs(secs - 30.0) < 1.0


def test_retry_after_past_date_clamps_to_zero() -> None:
    past = datetime(2000, 1, 1, tzinfo=timezone.utc)
    now = datetime(2030, 1, 1, tzinfo=timezone.utc).timestamp()
    assert parse_retry_after(format_datetime(past, usegmt=True), now=now) == 0.0
```

- [ ] **Step 2: Run to verify it fails** — Run: `cd clients/python && uv run pytest tests/test_transport.py -k "retryable or retry_after" -q` · Expected: FAIL.

- [ ] **Step 3: Implement** (append to `_transport.py`)

```python
from email.utils import parsedate_to_datetime


def is_retryable(status: int | None) -> bool:
    """Mirror internal/domain/classify.go:30 — 408/425/429 and any 5xx are retryable."""
    if status is None:
        return True  # transport-level failure (no response)
    if status in (408, 425, 429):
        return True
    return status >= 500


def parse_retry_after(value: str | None, now: float) -> float | None:
    """RFC 7231 Retry-After: integer seconds, or an HTTP-date -> seconds from `now` (>= 0)."""
    if value is None:
        return None
    value = value.strip()
    if value.isdigit():
        return float(value)
    try:
        when = parsedate_to_datetime(value)
    except (TypeError, ValueError):
        return None
    if when is None:
        return None
    return max(0.0, when.timestamp() - now)
```

- [ ] **Step 4: Run to verify it passes** — Run: `cd clients/python && uv run pytest tests/test_transport.py -q && uv run mypy src` · Expected: PASS; mypy clean.

- [ ] **Step 5: Commit**

```bash
git add clients/python/src/hookrail/_transport.py clients/python/tests/test_transport.py
git commit -m "feat(py-sdk): retry predicate and Retry-After parsing"
```

---

### Task 8: `compute_delay`

**Files:**
- Modify: `clients/python/src/hookrail/_transport.py`, `clients/python/tests/test_transport.py`

**Interfaces:**
- Produces: `compute_delay(attempt: int, policy: RetryPolicy, retry_after: float | None, rng: random.Random) -> float`.

- [ ] **Step 1: Add failing tests**

```python
import random

from hookrail._transport import compute_delay


def test_full_jitter_within_capped_backoff() -> None:
    p = RetryPolicy(base=0.2, cap=10.0)
    rng = random.Random(0)
    for attempt in range(0, 6):
        d = compute_delay(attempt, p, retry_after=None, rng=rng)
        assert 0.0 <= d <= 10.0


def test_huge_attempt_does_not_overflow() -> None:
    p = RetryPolicy()
    d = compute_delay(100000, p, retry_after=None, rng=random.Random(1))
    assert math.isfinite(d) and 0.0 <= d <= p.cap


def test_retry_after_is_a_floor_not_shortened() -> None:
    p = RetryPolicy(base=0.2, cap=10.0)
    d = compute_delay(0, p, retry_after=5.0, rng=random.Random(2))
    assert d >= 5.0
```

- [ ] **Step 2: Run to verify it fails** — Run: `cd clients/python && uv run pytest tests/test_transport.py -k compute_delay -q` · Expected: FAIL.

- [ ] **Step 3: Implement** (append to `_transport.py`; add `import random` at top)

```python
def compute_delay(
    attempt: int, policy: RetryPolicy, retry_after: float | None, rng: random.Random
) -> float:
    """Full-jitter exponential backoff, capped. Retry-After is a floor, never shortened.
    The deadline clip lives in RetryController (this returns the raw desired delay)."""
    exp = min(attempt, 30)  # bound the exponent -> no OverflowError
    backoff = min(policy.cap, policy.base * (2**exp))
    delay = rng.uniform(0.0, backoff)
    if retry_after is not None and policy.respect_retry_after:
        delay = retry_after + rng.uniform(0.0, policy.base)  # honor server, add jitter on top
    return max(0.0, delay)
```

- [ ] **Step 4: Run to verify it passes** — Run: `cd clients/python && uv run pytest tests/test_transport.py -q && uv run mypy src` · Expected: PASS; mypy clean.

- [ ] **Step 5: Commit**

```bash
git add clients/python/src/hookrail/_transport.py clients/python/tests/test_transport.py
git commit -m "feat(py-sdk): numerically-safe backoff with Retry-After floor"
```

---

### Task 9: `map_to_error` + `RetryController` state machine

**Files:**
- Modify: `clients/python/src/hookrail/_transport.py`, `clients/python/tests/test_transport.py`

**Interfaces:**
- Produces: `map_to_error(status:int, problem:Problem|None, retry_after:float|None) -> HookrailAPIError|None`; action types `Return`, `Retry(delay:float)`, `Stop(wrap:bool)`; `RetryController(policy, start)` with `decide(attempt:int, status:int|None, retry_after:float|None, now:float, rng) -> Action`. The client builds the error object and, on `Stop`, raises the typed error (`wrap=False`) or `RetryError(...) from last` (`wrap=True`).

- [ ] **Step 1: Add failing tests**

```python
from hookrail._transport import Retry, RetryController, Stop, map_to_error
from hookrail.errors import (
    AuthenticationError,
    ConflictError,
    RateLimitError,
    ServerError,
)


def test_map_to_error_table() -> None:
    assert map_to_error(200, None, None) is None
    assert isinstance(map_to_error(401, None, None), AuthenticationError)
    assert isinstance(map_to_error(409, None, None), ConflictError)
    assert isinstance(map_to_error(503, None, None), ServerError)
    rl = map_to_error(429, None, retry_after=2.0)
    assert isinstance(rl, RateLimitError) and rl.retry_after == 2.0


def test_controller_retries_then_exhausts_with_wrap() -> None:
    pol = RetryPolicy(max_retries=2, base=0.01, cap=0.02, max_elapsed=10.0)
    c = RetryController(policy=pol, start=0.0)
    rng = random.Random(0)
    assert isinstance(c.decide(0, 503, None, now=0.0, rng=rng), Retry)
    assert isinstance(c.decide(1, 503, None, now=0.1, rng=rng), Retry)
    final = c.decide(2, 503, None, now=0.2, rng=rng)  # attempts exhausted (retryable)
    assert isinstance(final, Stop) and final.wrap is True  # -> client raises RetryError(from last)


def test_controller_permanent_409_is_unwrapped() -> None:
    c = RetryController(policy=RetryPolicy(), start=0.0)
    out = c.decide(0, 409, None, now=0.0, rng=random.Random(0))
    assert isinstance(out, Stop) and out.wrap is False  # -> client raises the typed ConflictError


def test_controller_stops_when_retry_after_floor_exceeds_deadline() -> None:
    pol = RetryPolicy(max_retries=5, base=0.2, cap=10.0, max_elapsed=10.0)
    c = RetryController(policy=pol, start=0.0)
    out = c.decide(0, 503, retry_after=999.0, now=5.0, rng=random.Random(0))
    assert isinstance(out, Stop) and out.wrap is True  # floor (999s) won't fit remaining 5s


def test_controller_retries_when_retry_after_fits_remaining() -> None:
    pol = RetryPolicy(max_retries=5, base=0.2, cap=10.0, max_elapsed=10.0)
    c = RetryController(policy=pol, start=0.0)
    out = c.decide(0, 503, retry_after=3.0, now=5.0, rng=random.Random(0))  # 3s fits remaining 5s
    assert isinstance(out, Retry) and out.delay <= 5.0  # clipped to remaining budget
```

- [ ] **Step 2: Run to verify it fails** — Run: `cd clients/python && uv run pytest tests/test_transport.py -k "map_to_error or controller" -q` · Expected: FAIL.

- [ ] **Step 3: Implement** (append to `_transport.py`)

```python
from dataclasses import dataclass as _dataclass

from hookrail.errors import (
    AuthenticationError,
    BadRequestError,
    ConflictError,
    HookrailAPIError,
    NotFoundError,
    PayloadTooLargeError,
    RateLimitError,
    RetryError,
    ServerError,
)
from hookrail.models import Problem

_STATUS_ERRORS: dict[int, type[HookrailAPIError]] = {
    400: BadRequestError,
    401: AuthenticationError,
    404: NotFoundError,
    409: ConflictError,
    413: PayloadTooLargeError,
}


def map_to_error(
    status: int, problem: Problem | None, retry_after: float | None
) -> HookrailAPIError | None:
    if 200 <= status < 300:
        return None
    if status == 429:
        return RateLimitError(status=status, problem=problem, retry_after=retry_after)
    cls = _STATUS_ERRORS.get(status)
    if cls is not None:
        return cls(status=status, problem=problem)
    if status >= 500:
        return ServerError(status=status, problem=problem)
    return HookrailAPIError(status=status, problem=problem)


@_dataclass(frozen=True)
class Return:
    pass  # the caller already has a 2xx response to parse


@_dataclass(frozen=True)
class Retry:
    delay: float


@_dataclass(frozen=True)
class Stop:
    # wrap=True  -> the error was retryable but exhausted/over-budget; client raises RetryError(from last)
    # wrap=False -> permanent error (4xx); client raises the typed API error directly
    wrap: bool


Action = Return | Retry | Stop


class RetryController:
    """Owns retry/classify/delay/exhaustion. Immutable per call; pure given `now`/`rng`.
    The client builds the actual error object; the controller only decides the action so that
    `__cause__` (transport error vs typed API error) is set by the adapter that owns it."""

    def __init__(self, policy: RetryPolicy, start: float) -> None:
        self._policy = policy
        self._deadline = start + policy.max_elapsed

    def decide(
        self,
        attempt: int,
        status: int | None,
        retry_after: float | None,
        now: float,
        rng: random.Random,
    ) -> Action:
        if status is not None and 200 <= status < 300:
            return Return()
        if not is_retryable(status):
            return Stop(wrap=False)  # permanent 4xx -> raise the typed error as-is
        if attempt >= self._policy.max_retries:
            return Stop(wrap=True)  # retryable but no attempts left -> RetryError
        remaining = self._deadline - now
        if remaining <= 0:
            return Stop(wrap=True)
        if retry_after is not None and retry_after > remaining:
            return Stop(wrap=True)  # even the bare Retry-After floor won't fit the budget
        delay = min(compute_delay(attempt, self._policy, retry_after, rng), remaining)
        return Retry(delay)  # clipped to the remaining budget; never overshoots max_elapsed
```

- [ ] **Step 4: Run to verify it passes** — Run: `cd clients/python && uv run pytest tests/test_transport.py -q && uv run mypy src` · Expected: PASS; mypy clean.

- [ ] **Step 5: Commit**

```bash
git add clients/python/src/hookrail/_transport.py clients/python/tests/test_transport.py
git commit -m "feat(py-sdk): error mapping and RetryController state machine"
```

**M-B2 VERIFY:** `make py-verify`. **(M-B3 next — Codex pre-gate before the Opus gate.)**

---

# Milestone M-B3 — sync + async clients (Codex pre-gate)

### Task 10: Shared request builder + the sync `Hookrail` client

**Files:**
- Create: `clients/python/src/hookrail/_client.py`, `clients/python/src/hookrail/_request.py`, `clients/python/tests/test_client_sync.py`

**Interfaces:**
- Produces: `_request.build_send(topic, payload, idempotency_key) -> SendRequest(body:bytes, headers:dict[str,str])`; `_request.parse_problem(response) -> Problem|None`; `Hookrail(api_key=..., base_url=..., timeout=..., http_client=...)` with `send_event`, `get_event`, `close`, context-manager.

- [ ] **Step 1: Write the failing test** `clients/python/tests/test_client_sync.py`

```python
import httpx
import pytest
import respx

from hookrail import Hookrail
from hookrail.errors import BadRequestError, ConflictError, RateLimitError, RetryError
from hookrail.models import DeliveryState


@respx.mock
def test_send_event_success() -> None:
    route = respx.post("http://t:8080/v1/events").mock(
        return_value=httpx.Response(202, json={"event_id": "ev1", "delivery_ids": ["d1"]})
    )
    with Hookrail(api_key="hk_x", base_url="http://t:8080") as c:
        out = c.send_event("orders.created", {"id": 1})
    assert out.event_id == "ev1" and out.delivery_ids == ["d1"]
    assert route.calls.last.request.headers["authorization"] == "Bearer hk_x"
    assert "hookrail-python/" in route.calls.last.request.headers["user-agent"]
    assert route.calls.last.request.headers["idempotency-key"]


@respx.mock
def test_send_event_retries_503_with_identical_bytes_and_key() -> None:
    route = respx.post("http://t:8080/v1/events").mock(
        side_effect=[
            httpx.Response(503, json={"title": "down"}),
            httpx.Response(202, json={"event_id": "ev1", "delivery_ids": []}),
        ]
    )
    with Hookrail(api_key="hk_x", base_url="http://t:8080") as c:
        c.send_event("orders.created", {"id": 1}, idempotency_key="key-123")
    first, second = route.calls[0].request, route.calls[1].request
    assert first.content == second.content  # byte-identical body
    assert first.headers["idempotency-key"] == second.headers["idempotency-key"] == "key-123"


@respx.mock
def test_send_event_does_not_retry_409() -> None:
    route = respx.post("http://t:8080/v1/events").mock(
        return_value=httpx.Response(409, json={"title": "idempotency conflict"})
    )
    with Hookrail(api_key="hk_x", base_url="http://t:8080") as c:
        with pytest.raises(ConflictError):
            c.send_event("orders.created", {"id": 1})
    assert route.call_count == 1


@respx.mock
def test_replayed_header_sets_flag() -> None:
    respx.post("http://t:8080/v1/events").mock(
        return_value=httpx.Response(
            202, json={"event_id": "ev1", "delivery_ids": []}, headers={"Idempotent-Replay": "true"}
        )
    )
    with Hookrail(api_key="hk_x", base_url="http://t:8080") as c:
        out = c.send_event("orders.created", {"id": 1})
    assert out.replayed is True


@respx.mock
def test_persistent_503_raises_retry_error_wrapping_server_error() -> None:
    from hookrail.errors import ServerError

    respx.post("http://t:8080/v1/events").mock(return_value=httpx.Response(503, json={"title": "down"}))
    with Hookrail(api_key="hk_x", base_url="http://t:8080", retries=2) as c:
        with pytest.raises(RetryError) as exc:
            c.send_event("orders.created", {"id": 1})
    assert isinstance(exc.value.__cause__, ServerError)  # last typed error preserved as cause


@respx.mock
def test_get_event_parses_status() -> None:
    respx.get("http://t:8080/v1/events/ev1").mock(
        return_value=httpx.Response(
            200,
            json={
                "event_id": "ev1",
                "topic": "orders.created",
                "deliveries": [
                    {"delivery_id": "d1", "state": "succeeded", "attempts_truncated": False, "attempts": []}
                ],
            },
        )
    )
    with Hookrail(api_key="hk_x", base_url="http://t:8080") as c:
        st = c.get_event("ev1")
    assert st.deliveries[0].state is DeliveryState.succeeded
```

- [ ] **Step 2: Run to verify it fails** — Run: `cd clients/python && uv run pytest tests/test_client_sync.py -q` · Expected: FAIL (no `Hookrail`).

- [ ] **Step 3: Implement the request builder** `clients/python/src/hookrail/_request.py`

```python
"""Shared, side-effect-free request construction used by both clients."""

from __future__ import annotations

import json
import uuid
from dataclasses import dataclass

import httpx

from hookrail.models import Problem


@dataclass(frozen=True)
class SendRequest:
    body: bytes
    headers: dict[str, str]
    idempotency_key: str


def build_send(topic: str, payload: object, idempotency_key: str | None) -> SendRequest:
    if not isinstance(topic, str) or not topic:
        from hookrail.errors import HookrailConfigError

        raise HookrailConfigError("topic must be a non-empty string")
    key = idempotency_key or str(uuid.uuid4())
    body = json.dumps({"topic": topic, "payload": payload}, separators=(",", ":")).encode("utf-8")
    headers = {"Content-Type": "application/json", "Idempotency-Key": key}
    return SendRequest(body=body, headers=headers, idempotency_key=key)


def parse_problem(response: httpx.Response) -> Problem | None:
    try:
        data = response.json()
    except (ValueError, json.JSONDecodeError):
        return None
    if not isinstance(data, dict):
        return None
    return Problem.model_validate(data)
```

- [ ] **Step 4: Implement the sync client** `clients/python/src/hookrail/_client.py`

```python
"""Synchronous Hookrail client (design §2/§4)."""

from __future__ import annotations

import random
import time
from types import TracebackType

import httpx

from hookrail._config import ClientConfig
from hookrail._request import build_send, parse_problem
from hookrail._transport import (
    RetryController,
    RetryPolicy,
    Retry,
    Stop,
    map_to_error,
    parse_retry_after,
)
from hookrail.errors import HookrailConnectionError, HookrailTimeoutError, RetryError
from hookrail.models import EventAccepted, EventStatus


class Hookrail:
    def __init__(
        self,
        api_key: str | None = None,
        base_url: str | None = None,
        *,
        timeout: httpx.Timeout | None = None,
        retries: int | None = None,
        policy: RetryPolicy | None = None,
        http_client: httpx.Client | None = None,
    ) -> None:
        self._cfg = ClientConfig.resolve(api_key=api_key, base_url=base_url, timeout=timeout)
        self._policy = policy or RetryPolicy(max_retries=retries if retries is not None else 3)
        self._rng = random.Random()
        self._owns_client = http_client is None
        self._client = http_client or httpx.Client(timeout=self._cfg.timeout)

    def __enter__(self) -> "Hookrail":
        return self

    def __exit__(self, exc_type: type[BaseException] | None, exc: BaseException | None,
                 tb: TracebackType | None) -> None:
        self.close()

    def close(self) -> None:
        if self._owns_client:
            self._client.close()

    def _auth_headers(self, extra: dict[str, str]) -> dict[str, str]:
        return {"Authorization": f"Bearer {self._cfg.api_key}",
                "User-Agent": self._cfg.user_agent, **extra}

    def send_event(self, topic: str, payload: object, *, idempotency_key: str | None = None,
                   timeout: httpx.Timeout | None = None) -> EventAccepted:
        req = build_send(topic, payload, idempotency_key)
        url = f"{self._cfg.base_url}/v1/events"
        ctrl = RetryController(self._policy, start=time.monotonic())
        last: Exception = RetryError("no attempt made")
        for attempt in range(self._policy.max_retries + 1):
            status: int | None = None
            retry_after: float | None = None
            try:
                resp = self._client.post(url, content=req.body,
                                         headers=self._auth_headers(req.headers), timeout=timeout)
                status = resp.status_code
                if 200 <= status < 300:
                    ea = EventAccepted.model_validate(resp.json())
                    ea.replayed = resp.headers.get("Idempotent-Replay") == "true"
                    return ea
                retry_after = parse_retry_after(resp.headers.get("Retry-After"), now=time.time())
                mapped = map_to_error(status, parse_problem(resp), retry_after)
                assert mapped is not None
                last = mapped
            except httpx.TimeoutException as e:
                status, last = None, HookrailTimeoutError(str(e))
            except httpx.HTTPError as e:
                status, last = None, HookrailConnectionError(str(e))
            action = ctrl.decide(attempt, status, retry_after, now=time.monotonic(), rng=self._rng)
            if isinstance(action, Stop):
                if action.wrap:
                    raise RetryError(f"hookrail: giving up after retries: {last}") from last
                raise last
            if isinstance(action, Retry):  # Return is unreachable here (2xx returns above); narrows for mypy
                time.sleep(action.delay)
        raise RetryError(f"hookrail: retries exhausted: {last}") from last

    def get_event(self, event_id: str, *, timeout: httpx.Timeout | None = None) -> EventStatus:
        url = f"{self._cfg.base_url}/v1/events/{event_id}"
        ctrl = RetryController(self._policy, start=time.monotonic())
        last: Exception = RetryError("no attempt made")
        for attempt in range(self._policy.max_retries + 1):
            status: int | None = None
            retry_after: float | None = None
            try:
                resp = self._client.get(url, headers=self._auth_headers({}), timeout=timeout)
                status = resp.status_code
                if 200 <= status < 300:
                    return EventStatus.model_validate(resp.json())
                retry_after = parse_retry_after(resp.headers.get("Retry-After"), now=time.time())
                mapped = map_to_error(status, parse_problem(resp), retry_after)
                assert mapped is not None
                last = mapped
            except httpx.TimeoutException as e:
                status, last = None, HookrailTimeoutError(str(e))
            except httpx.HTTPError as e:
                status, last = None, HookrailConnectionError(str(e))
            action = ctrl.decide(attempt, status, retry_after, now=time.monotonic(), rng=self._rng)
            if isinstance(action, Stop):
                if action.wrap:
                    raise RetryError(f"hookrail: giving up after retries: {last}") from last
                raise last
            if isinstance(action, Retry):  # Return is unreachable here (2xx returns above); narrows for mypy
                time.sleep(action.delay)
        raise RetryError(f"hookrail: retries exhausted: {last}") from last
```

- [ ] **Step 5: Run to verify it passes** — Run: `cd clients/python && uv run pytest tests/test_client_sync.py -q && uv run mypy src` · Expected: PASS; mypy clean. (`EventAccepted.replayed` is a normal field set after validation; Pydantic v2 allows attribute assignment by default — `exclude=True` only affects serialization, not assignment.)

- [ ] **Step 6: Commit**

```bash
git add clients/python/src/hookrail/_request.py clients/python/src/hookrail/_client.py clients/python/tests/test_client_sync.py
git commit -m "feat(py-sdk): synchronous Hookrail client with retry/idempotency"
```

---

### Task 11: Async `AsyncHookrail` client

**Files:**
- Create: `clients/python/src/hookrail/_async_client.py`, `clients/python/tests/test_client_async.py`

**Interfaces:**
- Produces: `AsyncHookrail` with async `send_event`/`get_event`/`aclose` and async context-manager; reuses `_request` + `RetryController`.

- [ ] **Step 1: Write the failing test** `clients/python/tests/test_client_async.py`

```python
import httpx
import pytest
import respx

from hookrail import AsyncHookrail
from hookrail.errors import ConflictError, RetryError


@respx.mock
@pytest.mark.asyncio
async def test_async_send_success() -> None:
    respx.post("http://t:8080/v1/events").mock(
        return_value=httpx.Response(202, json={"event_id": "ev1", "delivery_ids": []})
    )
    async with AsyncHookrail(api_key="hk_x", base_url="http://t:8080") as c:
        out = await c.send_event("orders.created", {"id": 1})
    assert out.event_id == "ev1"


@respx.mock
@pytest.mark.asyncio
async def test_async_retry_identical_bytes() -> None:
    route = respx.post("http://t:8080/v1/events").mock(
        side_effect=[httpx.Response(503, json={"title": "d"}),
                     httpx.Response(202, json={"event_id": "ev1", "delivery_ids": []})]
    )
    async with AsyncHookrail(api_key="hk_x", base_url="http://t:8080") as c:
        await c.send_event("orders.created", {"id": 1}, idempotency_key="k1")
    assert route.calls[0].request.content == route.calls[1].request.content


@respx.mock
@pytest.mark.asyncio
async def test_async_no_retry_409() -> None:
    route = respx.post("http://t:8080/v1/events").mock(
        return_value=httpx.Response(409, json={"title": "conflict"})
    )
    async with AsyncHookrail(api_key="hk_x", base_url="http://t:8080") as c:
        with pytest.raises(ConflictError):
            await c.send_event("orders.created", {"id": 1})
    assert route.call_count == 1
```

Add to `pyproject.toml` `[dependency-groups] dev` the entry `"pytest-asyncio>=0.23"`, and to `[tool.pytest.ini_options]` add `asyncio_mode = "auto"`.

- [ ] **Step 2: Run to verify it fails** — Run: `cd clients/python && uv sync && uv run pytest tests/test_client_async.py -q` · Expected: FAIL (no `AsyncHookrail`).

- [ ] **Step 3: Implement** `clients/python/src/hookrail/_async_client.py`

```python
"""Asynchronous Hookrail client — same logic as the sync client, asyncio sleep."""

from __future__ import annotations

import asyncio
import random
import time
from types import TracebackType

import httpx

from hookrail._config import ClientConfig
from hookrail._request import build_send, parse_problem
from hookrail._transport import (
    RetryController,
    RetryPolicy,
    Retry,
    Stop,
    map_to_error,
    parse_retry_after,
)
from hookrail.errors import HookrailConnectionError, HookrailTimeoutError, RetryError
from hookrail.models import EventAccepted, EventStatus


class AsyncHookrail:
    def __init__(
        self,
        api_key: str | None = None,
        base_url: str | None = None,
        *,
        timeout: httpx.Timeout | None = None,
        retries: int | None = None,
        policy: RetryPolicy | None = None,
        http_client: httpx.AsyncClient | None = None,
    ) -> None:
        self._cfg = ClientConfig.resolve(api_key=api_key, base_url=base_url, timeout=timeout)
        self._policy = policy or RetryPolicy(max_retries=retries if retries is not None else 3)
        self._rng = random.Random()
        self._owns_client = http_client is None
        self._client = http_client or httpx.AsyncClient(timeout=self._cfg.timeout)

    async def __aenter__(self) -> "AsyncHookrail":
        return self

    async def __aexit__(self, exc_type: type[BaseException] | None, exc: BaseException | None,
                        tb: TracebackType | None) -> None:
        await self.aclose()

    async def aclose(self) -> None:
        if self._owns_client:
            await self._client.aclose()

    def _auth_headers(self, extra: dict[str, str]) -> dict[str, str]:
        return {"Authorization": f"Bearer {self._cfg.api_key}",
                "User-Agent": self._cfg.user_agent, **extra}

    async def send_event(self, topic: str, payload: object, *, idempotency_key: str | None = None,
                         timeout: httpx.Timeout | None = None) -> EventAccepted:
        req = build_send(topic, payload, idempotency_key)
        url = f"{self._cfg.base_url}/v1/events"
        ctrl = RetryController(self._policy, start=time.monotonic())
        last: Exception = RetryError("no attempt made")
        for attempt in range(self._policy.max_retries + 1):
            status: int | None = None
            retry_after: float | None = None
            try:
                resp = await self._client.post(url, content=req.body,
                                               headers=self._auth_headers(req.headers), timeout=timeout)
                status = resp.status_code
                if 200 <= status < 300:
                    ea = EventAccepted.model_validate(resp.json())
                    ea.replayed = resp.headers.get("Idempotent-Replay") == "true"
                    return ea
                retry_after = parse_retry_after(resp.headers.get("Retry-After"), now=time.time())
                mapped = map_to_error(status, parse_problem(resp), retry_after)
                assert mapped is not None
                last = mapped
            except httpx.TimeoutException as e:
                status, last = None, HookrailTimeoutError(str(e))
            except httpx.HTTPError as e:
                status, last = None, HookrailConnectionError(str(e))
            action = ctrl.decide(attempt, status, retry_after, now=time.monotonic(), rng=self._rng)
            if isinstance(action, Stop):
                if action.wrap:
                    raise RetryError(f"hookrail: giving up after retries: {last}") from last
                raise last
            if isinstance(action, Retry):  # Return is unreachable here (2xx returns above); narrows for mypy
                await asyncio.sleep(action.delay)
        raise RetryError(f"hookrail: retries exhausted: {last}") from last

    async def get_event(self, event_id: str, *, timeout: httpx.Timeout | None = None) -> EventStatus:
        url = f"{self._cfg.base_url}/v1/events/{event_id}"
        ctrl = RetryController(self._policy, start=time.monotonic())
        last: Exception = RetryError("no attempt made")
        for attempt in range(self._policy.max_retries + 1):
            status: int | None = None
            retry_after: float | None = None
            try:
                resp = await self._client.get(url, headers=self._auth_headers({}), timeout=timeout)
                status = resp.status_code
                if 200 <= status < 300:
                    return EventStatus.model_validate(resp.json())
                retry_after = parse_retry_after(resp.headers.get("Retry-After"), now=time.time())
                mapped = map_to_error(status, parse_problem(resp), retry_after)
                assert mapped is not None
                last = mapped
            except httpx.TimeoutException as e:
                status, last = None, HookrailTimeoutError(str(e))
            except httpx.HTTPError as e:
                status, last = None, HookrailConnectionError(str(e))
            action = ctrl.decide(attempt, status, retry_after, now=time.monotonic(), rng=self._rng)
            if isinstance(action, Stop):
                if action.wrap:
                    raise RetryError(f"hookrail: giving up after retries: {last}") from last
                raise last
            if isinstance(action, Retry):  # Return is unreachable here (2xx returns above); narrows for mypy
                await asyncio.sleep(action.delay)
        raise RetryError(f"hookrail: retries exhausted: {last}") from last
```

- [ ] **Step 4: Run to verify it passes** — Run: `cd clients/python && uv run pytest tests/test_client_async.py -q && uv run mypy src` · Expected: PASS; mypy clean.

- [ ] **Step 5: Commit**

```bash
git add clients/python/src/hookrail/_async_client.py clients/python/tests/test_client_async.py clients/python/pyproject.toml
git commit -m "feat(py-sdk): asynchronous AsyncHookrail client"
```

---

### Task 12: Public exports + cross-cutting client tests

**Files:**
- Modify: `clients/python/src/hookrail/__init__.py`, `clients/python/tests/test_client_sync.py`

**Interfaces:**
- Produces: top-level exports `Hookrail`, `AsyncHookrail`, all model + error classes, `__version__` (and `verify_signature` re-export is added in Task 14).

- [ ] **Step 1: Add the failing test** (append to `tests/test_client_sync.py`)

```python
@respx.mock
def test_injected_client_not_closed_by_close() -> None:
    respx.post("http://t:8080/v1/events").mock(
        return_value=httpx.Response(202, json={"event_id": "e", "delivery_ids": []})
    )
    external = httpx.Client()
    c = Hookrail(api_key="hk_x", base_url="http://t:8080", http_client=external)
    c.send_event("orders.created", {"id": 1})
    c.close()
    assert not external.is_closed  # SDK must not close a client it does not own
    external.close()


def test_public_exports_present() -> None:
    import hookrail

    for name in ["Hookrail", "AsyncHookrail", "EventAccepted", "EventStatus",
                 "HookrailError", "RateLimitError", "__version__"]:
        assert hasattr(hookrail, name)
```

- [ ] **Step 2: Run to verify it fails** — Run: `cd clients/python && uv run pytest tests/test_client_sync.py -k "injected or exports" -q` · Expected: FAIL (`AsyncHookrail`/exports not on package).

- [ ] **Step 3: Implement** `clients/python/src/hookrail/__init__.py`

```python
"""hookrail — Python client for the Hookrail webhook delivery service."""

from hookrail._async_client import AsyncHookrail
from hookrail._client import Hookrail
from hookrail._transport import RetryPolicy
from hookrail.errors import (
    AuthenticationError,
    BadRequestError,
    ConflictError,
    HookrailAPIError,
    HookrailConfigError,
    HookrailConnectionError,
    HookrailError,
    HookrailTimeoutError,
    NotFoundError,
    PayloadTooLargeError,
    RateLimitError,
    RetryError,
    ServerError,
)
from hookrail.models import (
    Attempt,
    Delivery,
    DeliveryState,
    EventAccepted,
    EventStatus,
    Problem,
)

__version__ = "0.1.0"

__all__ = [
    "Hookrail", "AsyncHookrail", "RetryPolicy", "__version__",
    "Attempt", "Delivery", "DeliveryState", "EventAccepted", "EventStatus", "Problem",
    "HookrailError", "HookrailConfigError", "HookrailConnectionError", "HookrailTimeoutError",
    "HookrailAPIError", "BadRequestError", "AuthenticationError", "NotFoundError",
    "ConflictError", "PayloadTooLargeError", "RateLimitError", "ServerError", "RetryError",
]
```

(Note: `_config.py`/`__init__.py` import order — `__version__` is defined here and imported by `_config`; keep `__version__` assignment before any submodule that reads it, or have `_config` import it lazily. To avoid a cycle, `_config.py` already does `from hookrail import __version__`; since `__init__` sets `__version__` after importing submodules, move the `__version__ = "0.1.0"` line ABOVE the imports in this file.)

- [ ] **Step 4: Fix the version-ordering** — edit `__init__.py` so `__version__ = "0.1.0"` is the FIRST statement (before the submodule imports), preventing an import cycle with `_config.py`.

- [ ] **Step 5: Run to verify it passes** — Run: `cd clients/python && uv run pytest -q -m "not e2e" && uv run mypy src` · Expected: all pass; mypy clean.

- [ ] **Step 6: Commit**

```bash
git add clients/python/src/hookrail/__init__.py clients/python/tests/test_client_sync.py
git commit -m "feat(py-sdk): public exports and client-ownership semantics"
```

---

### Task 13: Retry-After honored end-to-end (timing) test

**Files:**
- Modify: `clients/python/tests/test_client_sync.py`

**Interfaces:** none new — hardens the retry path with a deterministic timing assertion.

- [ ] **Step 1: Add the failing test** (append)

```python
@respx.mock
def test_retry_after_header_is_honored(monkeypatch: pytest.MonkeyPatch) -> None:
    slept: list[float] = []
    monkeypatch.setattr("hookrail._client.time.sleep", lambda s: slept.append(s))
    respx.post("http://t:8080/v1/events").mock(
        side_effect=[
            httpx.Response(429, json={"title": "slow down"}, headers={"Retry-After": "3"}),
            httpx.Response(202, json={"event_id": "e", "delivery_ids": []}),
        ]
    )
    with Hookrail(api_key="hk_x", base_url="http://t:8080") as c:
        c.send_event("orders.created", {"id": 1})
    assert slept and slept[0] >= 3.0  # never shorter than Retry-After
```

- [ ] **Step 2: Run to verify it fails or passes** — Run: `cd clients/python && uv run pytest tests/test_client_sync.py -k retry_after_header -q` · Expected: PASS (the implementation already honors it; this locks the behavior). If it FAILS, fix `_client.send_event` so `Retry-After` flows into `compute_delay`.

- [ ] **Step 3: Commit**

```bash
git add clients/python/tests/test_client_sync.py
git commit -m "test(py-sdk): lock Retry-After honoring on the send path"
```

**M-B3 VERIFY:** `make py-verify`. **GATE: mandatory Codex gpt-5.5 read-only pre-gate** (`codex exec --sandbox read-only -C <repo>`) reviewing `_transport.py`/`_request.py`/`_client.py`/`_async_client.py` against the design's retry/idempotency rules and the real server facts, BEFORE the Opus gate. Fold blockers/majors via a foreground rework loop.

---

# Milestone M-B4 — signing helper, docs, build, release, e2e

### Task 14: `verify_signature` receiver helper + Go-fixture round-trip

**Files:**
- Create: `clients/python/src/hookrail/signing.py`, `clients/python/tests/test_signing.py`, `clients/python/tests/fixtures/signature.json`
- Modify: `clients/python/src/hookrail/__init__.py` (export `verify_signature`)

**Interfaces:**
- Produces: `verify_signature(secrets, header, delivery_id, body, *, now=None, tolerance=300.0) -> None` (raises `SignatureError`/`SignatureTimestampError`/`MalformedSignatureError`, all under `HookrailError`). `secrets`: `bytes | Sequence[bytes]`. `body`: `bytes`.

- [ ] **Step 1: Generate the fixture from the REAL Go `Sign`** — `internal/signing` is a Go **internal** package, so the generator MUST live inside the module tree to import it (a file under `/tmp` cannot). Commit the generator source as a `.go.txt` for reproducibility, run a copy from inside the repo (under the gitignored `.agent/`), capture the fixture, then remove the runnable copy so the Go build stays clean.

First create `clients/python/tests/fixtures/gen_fixture.go.txt` (committed, NOT compiled):

```go
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/mit112/hookrail/internal/signing"
)

func main() {
	secret := []byte("whsec_test_secret")
	body := []byte(`{"order_id":"o_1","amount":42}`)
	did := "01JXAMPLEDELIVERYID0000000"
	t := time.Unix(1781092800, 0).UTC()
	h := signing.Sign(secret, t, did, body)
	_ = json.NewEncoder(os.Stdout).Encode(map[string]string{
		"secret": string(secret), "delivery_id": did, "body": string(body),
		"header": h, "unix": fmt.Sprint(t.Unix()),
	})
}
```

Then run it from inside the module and capture the fixture:

```bash
cd /Users/mitsheth/Documents/projectX/hookrail
mkdir -p .agent/tmp && cp clients/python/tests/fixtures/gen_fixture.go.txt .agent/tmp/genfix.go
go run ./.agent/tmp/genfix.go > clients/python/tests/fixtures/signature.json
rm -rf .agent/tmp/genfix.go
```

(`.agent/` is gitignored and inside the module, so `go run` there can import `internal/signing` and nothing extra lands in the Go build. Commit only `gen_fixture.go.txt` + `signature.json`. The loop's Python VERIFY never invokes Go — `test_signing.py` reads the committed `signature.json`.)

- [ ] **Step 2: Write the failing test** `clients/python/tests/test_signing.py`

```python
import json
from pathlib import Path

import pytest

from hookrail.signing import (
    MalformedSignatureError,
    SignatureError,
    SignatureTimestampError,
    verify_signature,
)

_FIX = json.loads((Path(__file__).parent / "fixtures" / "signature.json").read_text())
_SECRET = _FIX["secret"].encode()
_DID = _FIX["delivery_id"]
_BODY = _FIX["body"].encode()
_HEADER = _FIX["header"]
_NOW = float(_FIX["unix"])


def test_valid_signature_passes() -> None:
    verify_signature(_SECRET, _HEADER, _DID, _BODY, now=_NOW, tolerance=300.0)


def test_tampered_body_fails() -> None:
    with pytest.raises(SignatureError):
        verify_signature(_SECRET, _HEADER, _DID, b'{"order_id":"o_1","amount":9999}', now=_NOW)


def test_wrong_delivery_id_fails() -> None:
    with pytest.raises(SignatureError):
        verify_signature(_SECRET, _HEADER, "01JOTHER0000000000000000", _BODY, now=_NOW)


def test_out_of_tolerance_fails() -> None:
    with pytest.raises(SignatureTimestampError):
        verify_signature(_SECRET, _HEADER, _DID, _BODY, now=_NOW + 3600, tolerance=300.0)


def test_malformed_header_fails() -> None:
    with pytest.raises(MalformedSignatureError):
        verify_signature(_SECRET, "garbage", _DID, _BODY, now=_NOW)


def test_dual_secret_rotation() -> None:
    verify_signature([b"new_secret", _SECRET], _HEADER, _DID, _BODY, now=_NOW)
```

- [ ] **Step 3: Run to verify it fails** — Run: `cd clients/python && uv run pytest tests/test_signing.py -q` · Expected: FAIL (no module).

- [ ] **Step 4: Implement** `clients/python/src/hookrail/signing.py`

```python
"""Receiver-side verification of the Hookrail webhook signature (mirrors internal/signing).

Header: hookrail-signature: t=<unix>,v1=hex(HMAC_SHA256(secret, "<unix>.<delivery_id>." + body))
"""

from __future__ import annotations

import hashlib
import hmac
import time
from collections.abc import Sequence

from hookrail.errors import HookrailError

HEADER = "hookrail-signature"


class MalformedSignatureError(HookrailError):
    """The signature header could not be parsed."""


class SignatureTimestampError(HookrailError):
    """The signature timestamp is outside the allowed tolerance."""


class SignatureError(HookrailError):
    """No provided secret produced a matching signature."""


def _mac(secret: bytes, unix: int, delivery_id: str, body: bytes) -> bytes:
    m = hmac.new(secret, digestmod=hashlib.sha256)
    m.update(f"{unix}.{delivery_id}.".encode("utf-8"))
    m.update(body)
    return m.digest()


def verify_signature(
    secrets: bytes | Sequence[bytes],
    header: str,
    delivery_id: str,
    body: bytes,
    *,
    now: float | None = None,
    tolerance: float = 300.0,
) -> None:
    """Raise on failure; return None on success. `secrets` may be a single key or several
    (dual-secret rotation). `tolerance` is in seconds."""
    secret_list = [secrets] if isinstance(secrets, (bytes, bytearray)) else list(secrets)
    unix: int | None = None
    sig_hex = ""
    for part in header.split(","):
        k, _, v = part.strip().partition("=")
        if not _:
            continue
        if k == "t":
            try:
                unix = int(v)
            except ValueError:
                raise MalformedSignatureError("bad timestamp in signature header") from None
        elif k == "v1":
            sig_hex = v
    if unix is None or not sig_hex:
        raise MalformedSignatureError("missing t= or v1= in signature header")
    try:
        got = bytes.fromhex(sig_hex)
    except ValueError:
        raise MalformedSignatureError("v1 is not valid hex") from None
    if len(got) != hashlib.sha256().digest_size:
        raise MalformedSignatureError("v1 has wrong length")
    current = time.time() if now is None else now
    if abs(current - unix) > tolerance:
        raise SignatureTimestampError("signature timestamp outside tolerance")
    for secret in secret_list:
        if hmac.compare_digest(got, _mac(bytes(secret), unix, delivery_id, body)):
            return
    raise SignatureError("no matching signature")
```

- [ ] **Step 5: Export it** — add to `__init__.py`: `from hookrail.signing import verify_signature` and append `"verify_signature"` to `__all__`.

- [ ] **Step 6: Run to verify it passes** — Run: `cd clients/python && uv run pytest tests/test_signing.py -q && uv run mypy src` · Expected: PASS; mypy clean.

- [ ] **Step 7: Commit**

```bash
git add clients/python/src/hookrail/signing.py clients/python/tests/test_signing.py clients/python/tests/fixtures/signature.json clients/python/src/hookrail/__init__.py
git commit -m "feat(py-sdk): receiver-side verify_signature with Go-fixture round-trip"
```

---

### Task 15: README, examples, CHANGELOG, LICENSE

**Files:**
- Create/replace: `clients/python/README.md`; create `clients/python/CHANGELOG.md`, `clients/python/LICENSE`, `clients/python/examples/quickstart.py`, `clients/python/examples/async_quickstart.py`, `clients/python/examples/verify_webhook.py`

- [ ] **Step 1: Copy the license** — Run: `cp LICENSE clients/python/LICENSE` (Apache-2.0 from the repo root).

- [ ] **Step 2: Write `clients/python/examples/quickstart.py`**

```python
from hookrail import Hookrail

with Hookrail(api_key="hk_...", base_url="https://hooks.example.com") as client:
    accepted = client.send_event("orders.created", {"order_id": "o_1", "amount": 4200})
    print("event:", accepted.event_id, "replayed:", accepted.replayed)
    status = client.get_event(accepted.event_id)
    for d in status.deliveries:
        print(d.delivery_id, d.state)
```

- [ ] **Step 3: Write `clients/python/examples/async_quickstart.py`**

```python
import asyncio

from hookrail import AsyncHookrail


async def main() -> None:
    async with AsyncHookrail(api_key="hk_...", base_url="https://hooks.example.com") as client:
        accepted = await client.send_event("orders.created", {"order_id": "o_1"})
        print(accepted.event_id)


asyncio.run(main())
```

- [ ] **Step 4: Write `clients/python/examples/verify_webhook.py`**

```python
from hookrail import verify_signature
from hookrail.signing import HEADER, SignatureError

# In your webhook receiver (e.g. a Flask/FastAPI route):
def handle(headers: dict[str, str], delivery_id: str, raw_body: bytes, secret: bytes) -> bool:
    try:
        verify_signature(secret, headers[HEADER], delivery_id, raw_body)
    except SignatureError:
        return False
    return True
```

- [ ] **Step 5: Write `clients/python/CHANGELOG.md`**

```markdown
# Changelog

## 0.1.0 (unreleased)
- Initial release: sync + async producer client (`send_event`, `get_event`),
  automatic idempotency keys, retry/backoff with `Retry-After`, typed RFC-7807 errors,
  and a receiver-side `verify_signature` helper.
```

- [ ] **Step 6: Write `clients/python/README.md`** — a real quickstart: install (`pip install hookrail` / `uv add hookrail`), the sync + async snippets above, the error model, the retry/idempotency behavior, the `verify_signature` example, supported Python versions, and a "this client wraps the public producer surface; admin is internal-only" note. Include the design's residual-risk honesty (get_event 404 caveat, per-worker rate limiting). Keep it ~120 lines.

- [ ] **Step 7: Verify docs don't break lint** — Run: `make py-verify` · Expected: green (examples are not under `src`, so mypy `files=["src"]` ignores them; ruff checks them — keep them clean).

- [ ] **Step 8: Commit**

```bash
git add clients/python/README.md clients/python/CHANGELOG.md clients/python/LICENSE clients/python/examples
git commit -m "docs(py-sdk): README, examples, changelog, license"
```

---

### Task 16: Build + twine check + clean-venv install smoke

**Files:**
- Create: `clients/python/scripts/py-install-smoke.sh`

**Interfaces:**
- Produces: `make py-build` runs end-to-end (build → `twine check` → install the wheel into a throwaway venv → import + smoke).

- [ ] **Step 1: Write `clients/python/scripts/py-install-smoke.sh`**

```bash
#!/usr/bin/env bash
# Build artifacts must install cleanly into a fresh environment and import.
set -euo pipefail
cd "$(dirname "$0")/.."
WHEEL="$(ls -1 dist/*.whl | head -1)"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
uv venv "$TMP/venv"
VPY="$TMP/venv/bin/python"
"$TMP/venv/bin/python" -m ensurepip >/dev/null 2>&1 || true
uv pip install --python "$VPY" "$WHEEL"
"$VPY" -c "import hookrail; from hookrail import Hookrail, AsyncHookrail, verify_signature; print('smoke ok', hookrail.__version__)"
```

Make it executable: `chmod +x clients/python/scripts/py-install-smoke.sh`. Update the `Makefile` `py-build` target (from Task 1) to: `cd clients/python && uv build && uv run --with twine python -m twine check dist/* && bash scripts/py-install-smoke.sh`.

- [ ] **Step 2: Run** — Run: `make py-build` · Expected: builds an sdist+wheel, `twine check` PASSED for both, "smoke ok 0.1.0".

- [ ] **Step 3: Commit**

```bash
git add clients/python/scripts/py-install-smoke.sh Makefile
git commit -m "build(py-sdk): wheel build, twine check, and install smoke"
```

---

### Task 17: Release workflow (OIDC trusted publishing, protected environments) + actionlint

**Files:**
- Create: `.github/workflows/release.yml`

- [ ] **Step 1: Write `.github/workflows/release.yml`**

```yaml
name: release-python
on:
  push:
    tags: ["python-v*.*.*"]   # real PyPI — attended (Mit pushes the tag, approves the pypi environment)
  workflow_dispatch: {}        # manual run -> TestPyPI only (never real PyPI)
jobs:
  build:
    runs-on: ubuntu-latest
    defaults:
      run:
        working-directory: clients/python
    steps:
      - uses: actions/checkout@v4
      - uses: astral-sh/setup-uv@v5
      - run: uv build
      - uses: actions/upload-artifact@v4
        with:
          name: dist
          path: clients/python/dist/
  publish-testpypi:
    if: github.event_name == 'workflow_dispatch'
    needs: build
    runs-on: ubuntu-latest
    environment: testpypi
    permissions:
      id-token: write
    steps:
      - uses: actions/download-artifact@v4
        with: { name: dist, path: dist }
      - uses: pypa/gh-action-pypi-publish@release/v1
        with:
          repository-url: https://test.pypi.org/legacy/
  publish-pypi:
    if: startsWith(github.ref, 'refs/tags/python-v')
    needs: build
    runs-on: ubuntu-latest
    environment: pypi
    permissions:
      id-token: write
    steps:
      - uses: actions/download-artifact@v4
        with: { name: dist, path: dist }
      - uses: pypa/gh-action-pypi-publish@release/v1
```

- [ ] **Step 2: Lint the workflows (HARD gate)** — Run via the pinned Docker image (Docker is present):

```bash
docker run --rm -v "$(pwd):/repo" -w /repo rhysd/actionlint:1.7.7 -color
```

Expected: no errors for `.github/workflows/python.yml` and `release.yml`. If actionlint reports issues, fix them; this is not advisory.

- [ ] **Step 3: Commit**

```bash
git add .github/workflows/release.yml
git commit -m "ci(py-sdk): OIDC trusted-publishing release workflow with protected environments"
```

---

### Task 18: Live e2e against the compose stack

**Files:**
- Create: `clients/python/scripts/py-e2e.sh`, `clients/python/tests/test_e2e.py`

**Interfaces:**
- Produces: `make py-e2e` brings up the stack, seeds + exports a real `hk_` producer key, runs the `e2e`-marked tests, and tears down.

- [ ] **Step 1: Write `clients/python/scripts/py-e2e.sh`** (mirror `scripts/e2e.sh`)

```bash
#!/usr/bin/env bash
# Bring up the compose stack, seed a producer key, run the Python SDK e2e, tear down.
set -euo pipefail
cd "$(dirname "$0")/../../.."   # repo root
COMPOSE="docker compose -f deploy/compose/docker-compose.yml"
export HOOKRAIL_MASTER_KEY="${HOOKRAIL_MASTER_KEY:-000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f}"

$COMPOSE up -d --build
trap '$COMPOSE down -v' EXIT
for _ in $(seq 1 60); do
  curl -fsS http://localhost:8080/readyz >/dev/null 2>&1 && break
  sleep 1
done
KEY="$($COMPOSE run --rm api hookrail-ctl seed -url http://test-receiver:9090/succeed -topic 'demo.python.*' \
       | awk -F= '/^producer_key=/{print $2}')"
export HOOKRAIL_API_KEY="$KEY"
export HOOKRAIL_BASE_URL="http://localhost:8080"
cd clients/python && uv run pytest -q -m e2e
```

Make executable: `chmod +x clients/python/scripts/py-e2e.sh`.

- [ ] **Step 2: Write `clients/python/tests/test_e2e.py`**

```python
import os
import time

import pytest

from hookrail import AsyncHookrail, Hookrail
from hookrail.models import DeliveryState

pytestmark = pytest.mark.e2e

_BASE = os.environ.get("HOOKRAIL_BASE_URL", "http://localhost:8080")


def _poll_succeeded(client: Hookrail, event_id: str, deadline: float = 30.0) -> bool:
    end = time.monotonic() + deadline
    while time.monotonic() < end:
        st = client.get_event(event_id)
        if any(d.state is DeliveryState.succeeded for d in st.deliveries):
            return True
        time.sleep(1)
    return False


def test_sync_send_and_deliver() -> None:
    with Hookrail(base_url=_BASE) as c:  # HOOKRAIL_API_KEY from env
        accepted = c.send_event("demo.python.orders", {"id": "e2e-sync"})
        assert accepted.event_id
        assert _poll_succeeded(c, accepted.event_id), "delivery never reached succeeded"


@pytest.mark.asyncio
async def test_async_send() -> None:
    async with AsyncHookrail(base_url=_BASE) as c:
        accepted = await c.send_event("demo.python.orders", {"id": "e2e-async"})
        assert accepted.event_id
```

- [ ] **Step 3: Run the e2e (gate-only; needs Docker)** — Run: `make py-e2e` · Expected: stack up, `/readyz` ok, both tests pass (sync delivery reaches `succeeded`), stack torn down. (Not part of per-iteration `make py-verify`.)

- [ ] **Step 4: Commit**

```bash
git add clients/python/scripts/py-e2e.sh clients/python/tests/test_e2e.py
git commit -m "test(py-sdk): live e2e against the compose stack"
```

**M-B4 VERIFY:** `make py-verify && make py-build`; the gate additionally runs `make py-e2e`. On a green gate + green CI, this is the final Slice B milestone → write `.agent/SLICEB_DONE`. **Publishing to real PyPI is attended (Mit pushes `python-v0.1.0` + approves the `pypi` environment); the loop never publishes.**

---

## Self-Review

- **Spec coverage:** D-B1 location (Task 1 dir); D-B2 producer ops + verify helper (Tasks 10/11/14); D-B3 models + directional conformance (Tasks 3/5); D-B4 sync+async over shared controller (Tasks 9/10/11); D-B5 tooling (Task 1) + matrix CI (Task 5); D-B6 publish boundary (Task 1 denies + Task 17 envs); D-B7 Opus gates + M-B3 Codex pre-gate (milestone map + Task 13 note). Server caveats (404, hk_ prefix, byte-exact idempotency, 202) all encoded in Global Constraints + the relevant tasks. Signing scheme mirrored (Task 14).
- **Placeholder scan:** every code step carries complete code; the README (Task 15 Step 6) is described with explicit required content rather than pasted in full (acceptable — it is prose, not logic).
- **Type consistency:** `RetryController.decide(...)` signature, `Action = Return|Raise|Retry`, `map_to_error(status, problem, retry_after)`, `build_send(...) -> SendRequest`, `verify_signature(secrets, header, delivery_id, body, *, now, tolerance)`, `EventAccepted.replayed` (exclude + `object.__setattr__`) are used identically across tasks 9–14.
- **Known follow-ups (NOT this slice):** the `GET /v1/events/{id}` 404-masks-outage server bug (attended Go fix / Slice E); optional `openapi.yaml` extension to document `attempts_truncated`/`Idempotent-Replay`.
