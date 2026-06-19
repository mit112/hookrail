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
