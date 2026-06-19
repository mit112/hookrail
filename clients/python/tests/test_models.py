from hookrail.models import (
    Attempt,
    Delivery,
    DeliveryState,
    EventAccepted,
    EventStatus,
    Problem,
)


def test_event_accepted_parses_and_excludes_replayed_from_dump() -> None:
    ea = EventAccepted.model_validate({"event_id": "ev_1", "delivery_ids": ["d1", "d2"]})
    assert ea.event_id == "ev_1"
    assert ea.delivery_ids == ["d1", "d2"]
    assert ea.replayed is False
    assert "replayed" not in ea.model_dump()


def test_event_status_known_state_becomes_enum() -> None:
    es = EventStatus.model_validate(
        {
            "event_id": "ev_1",
            "topic": "orders.created",
            "deliveries": [
                {
                    "delivery_id": "d1",
                    "state": "succeeded",
                    "attempts_truncated": False,
                    "attempts": [
                        {
                            "attempt_no": 1,
                            "claim_version": 7,
                            "status": "succeeded",
                            "latency_ms": 12,
                        }
                    ],
                }
            ],
        }
    )
    d = es.deliveries[0]
    assert d.state is DeliveryState.succeeded
    assert d.attempts[0].http_status is None


def test_unknown_state_is_kept_as_raw_string() -> None:
    d = Delivery.model_validate(
        {
            "delivery_id": "d1",
            "state": "quantum_superposition",
            "attempts_truncated": False,
            "attempts": [],
        }
    )
    assert d.state == "quantum_superposition"
    assert not isinstance(d.state, DeliveryState)


def test_attempt_requires_core_fields() -> None:
    a = Attempt.model_validate(
        {
            "attempt_no": 2,
            "claim_version": 9,
            "status": "failed",
            "latency_ms": 30,
            "http_status": 503,
        }
    )
    assert a.http_status == 503


def test_problem_is_lenient() -> None:
    p = Problem.model_validate({"title": "bad", "status": 400})
    assert p.status == 400
    assert p.type is None
