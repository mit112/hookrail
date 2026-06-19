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
