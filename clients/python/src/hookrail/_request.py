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
