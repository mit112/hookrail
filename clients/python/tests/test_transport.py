import math
import random
from datetime import datetime, timezone
from email.utils import format_datetime

import pytest

from hookrail._transport import RetryPolicy, compute_delay, is_retryable, parse_retry_after
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


@pytest.mark.parametrize(
    "status,expected",
    [
        (200, False),
        (400, False),
        (401, False),
        (404, False),
        (409, False),
        (413, False),
        (408, True),
        (425, True),
        (429, True),
        (500, True),
        (502, True),
        (503, True),
        (504, True),
        (599, True),
    ],
)
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
