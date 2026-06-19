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
