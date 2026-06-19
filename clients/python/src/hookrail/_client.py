"""Synchronous Hookrail client (design §2/§4)."""

from __future__ import annotations

import random
import time
from types import TracebackType

import httpx

from hookrail._config import ClientConfig
from hookrail._request import build_send, parse_problem
from hookrail._transport import (
    Retry,
    RetryController,
    RetryPolicy,
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

    def __enter__(self) -> Hookrail:
        return self

    def __exit__(
        self,
        exc_type: type[BaseException] | None,
        exc: BaseException | None,
        tb: TracebackType | None,
    ) -> None:
        self.close()

    def close(self) -> None:
        if self._owns_client:
            self._client.close()

    def _auth_headers(self, extra: dict[str, str]) -> dict[str, str]:
        return {
            "Authorization": f"Bearer {self._cfg.api_key}",
            "User-Agent": self._cfg.user_agent,
            **extra,
        }

    def send_event(
        self,
        topic: str,
        payload: object,
        *,
        idempotency_key: str | None = None,
        timeout: httpx.Timeout | None = None,
    ) -> EventAccepted:
        req = build_send(topic, payload, idempotency_key)
        url = f"{self._cfg.base_url}/v1/events"
        ctrl = RetryController(self._policy, start=time.monotonic())
        last: Exception = RetryError("no attempt made")
        for attempt in range(self._policy.max_retries + 1):
            status: int | None = None
            retry_after: float | None = None
            try:
                resp = self._client.post(
                    url, content=req.body, headers=self._auth_headers(req.headers), timeout=timeout
                )
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
            if isinstance(action, Retry):  # 2xx returns above; narrows for mypy
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
            if isinstance(action, Retry):  # 2xx returns above; narrows for mypy
                time.sleep(action.delay)
        raise RetryError(f"hookrail: retries exhausted: {last}") from last
