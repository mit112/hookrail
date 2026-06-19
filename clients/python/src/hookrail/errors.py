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

    def __init__(
        self, status: int, problem: Problem | None, retry_after: float | None = None
    ) -> None:
        self.retry_after = retry_after
        super().__init__(status, problem)


class ServerError(HookrailAPIError):
    """5xx — transient server error."""


class RetryError(HookrailError):
    """Retries (or the total deadline) were exhausted; the last error is __cause__."""
