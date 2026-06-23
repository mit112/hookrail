"""hookrail — Python client for the Hookrail webhook delivery service."""

__version__ = "0.1.0"

from hookrail._async_client import AsyncHookrail
from hookrail._client import Hookrail
from hookrail._transport import RetryPolicy
from hookrail.errors import (
    AuthenticationError,
    BadRequestError,
    ConflictError,
    ForbiddenError,
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
from hookrail.signing import verify_signature

__all__ = [
    "Hookrail",
    "AsyncHookrail",
    "RetryPolicy",
    "__version__",
    "Attempt",
    "Delivery",
    "DeliveryState",
    "EventAccepted",
    "EventStatus",
    "Problem",
    "HookrailError",
    "HookrailConfigError",
    "HookrailConnectionError",
    "HookrailTimeoutError",
    "HookrailAPIError",
    "BadRequestError",
    "AuthenticationError",
    "ForbiddenError",
    "NotFoundError",
    "ConflictError",
    "PayloadTooLargeError",
    "RateLimitError",
    "ServerError",
    "RetryError",
    "verify_signature",
]
