"""Pydantic v2 models mirroring the Hookrail producer surface (see design §3)."""

from __future__ import annotations

from enum import Enum
from typing import Annotated, Any

from pydantic import BaseModel, BeforeValidator, ConfigDict, Field


class DeliveryState(str, Enum):
    pending = "pending"
    in_flight = "in_flight"
    retry_scheduled = "retry_scheduled"
    succeeded = "succeeded"
    dead_lettered = "dead_lettered"
    cancelled = "cancelled"


def _coerce_state(value: Any) -> Any:
    """Known value -> enum member; unknown -> raw str (forward compatible)."""
    if isinstance(value, DeliveryState):
        return value
    if isinstance(value, str):
        try:
            return DeliveryState(value)
        except ValueError:
            return value
    return value


# Known states become the enum; unknown future states stay as plain strings.
StateField = Annotated[DeliveryState | str, BeforeValidator(_coerce_state)]


class Attempt(BaseModel):
    model_config = ConfigDict(extra="allow")
    attempt_no: int
    claim_version: int
    status: str
    latency_ms: int
    http_status: int | None = None
    error_class: str | None = None


class Delivery(BaseModel):
    model_config = ConfigDict(extra="allow")
    delivery_id: str
    state: StateField
    attempts_truncated: bool
    attempts: list[Attempt] = Field(default_factory=list)


class EventAccepted(BaseModel):
    model_config = ConfigDict(extra="allow")
    event_id: str
    delivery_ids: list[str] = Field(default_factory=list)
    replayed: bool = Field(default=False, exclude=True)


class EventStatus(BaseModel):
    model_config = ConfigDict(extra="allow")
    event_id: str
    topic: str
    deliveries: list[Delivery] = Field(default_factory=list)


class Problem(BaseModel):
    model_config = ConfigDict(extra="allow")
    type: str | None = None
    title: str | None = None
    status: int | None = None
    detail: str | None = None
