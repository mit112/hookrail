"""Directional + schema-validating contract test.

Every field the OpenAPI contract declares for a producer response must exist on the
SDK model (model may be richer than the stale doc), a representative model dump must
validate against the contract schema, an invalid dump must FAIL it (non-vacuous), and
the SDK must carry the documented server-only fields (attempts_truncated / structured
attempts).
"""

from __future__ import annotations

from pathlib import Path

import jsonschema
import pytest
import yaml

from hookrail.models import (
    Attempt,
    Delivery,
    EventAccepted,
    EventStatus,
)

_OPENAPI = Path(__file__).parents[3] / "api" / "openapi.yaml"


def _load() -> dict:
    if not _OPENAPI.exists():
        pytest.skip(f"openapi.yaml not found at {_OPENAPI} (standalone install)")
    return yaml.safe_load(_OPENAPI.read_text())


def _schema(spec: dict, path: str, method: str, code: str) -> dict:
    return spec["paths"][path][method]["responses"][code]["content"]["application/json"]["schema"]


def _props(schema: dict) -> set[str]:
    return set(schema.get("properties", {}))


def test_event_accepted_covers_and_validates_against_202() -> None:
    schema = _schema(_load(), "/v1/events", "post", "202")
    missing = _props(schema) - set(EventAccepted.model_fields)
    assert not missing, f"EventAccepted is missing contract fields: {missing}"
    instance = EventAccepted(event_id="ev1", delivery_ids=["d1"]).model_dump()
    jsonschema.validate(instance, schema)  # representative dump must satisfy the contract


def test_event_status_covers_and_descends_into_deliveries() -> None:
    spec = _load()
    schema = _schema(spec, "/v1/events/{id}", "get", "200")
    assert not _props(schema) - set(EventStatus.model_fields)
    # nested: every field the contract declares for a delivery item must exist on Delivery
    item_schema = schema["properties"]["deliveries"]["items"]
    assert not _props(item_schema) - set(Delivery.model_fields)
    instance = EventStatus(
        event_id="ev1",
        topic="orders.created",
        deliveries=[
            Delivery(
                delivery_id="d1",
                state="succeeded",
                attempts_truncated=False,
                attempts=[Attempt(attempt_no=1, claim_version=2, status="succeeded", latency_ms=5)],
            )
        ],
    ).model_dump()
    jsonschema.validate(instance, schema)


def test_negative_control_invalid_dump_fails_schema() -> None:
    schema = _schema(_load(), "/v1/events", "post", "202")
    with pytest.raises(jsonschema.ValidationError):
        jsonschema.validate({"event_id": 123, "delivery_ids": "not-a-list"}, schema)  # wrong types


def test_models_carry_documented_server_only_fields() -> None:
    # The live server emits these even though the ingress OpenAPI schema
    # is stale (design §2 divergence).
    assert "attempts_truncated" in Delivery.model_fields
    for field in ("attempt_no", "claim_version", "status", "latency_ms"):
        assert field in Attempt.model_fields
