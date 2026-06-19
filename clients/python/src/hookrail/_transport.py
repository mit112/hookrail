"""Pure retry/idempotency/error-mapping state machine (design §4)."""

from __future__ import annotations

import math
import random
from dataclasses import dataclass
from email.utils import parsedate_to_datetime

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
