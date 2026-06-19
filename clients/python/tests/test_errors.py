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
