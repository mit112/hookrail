import httpx
import pytest
import respx

from hookrail import Hookrail
from hookrail.errors import ConflictError, RetryError
from hookrail.models import DeliveryState


@respx.mock
def test_send_event_success() -> None:
    route = respx.post("http://t:8080/v1/events").mock(
        return_value=httpx.Response(202, json={"event_id": "ev1", "delivery_ids": ["d1"]})
    )
    with Hookrail(api_key="hk_x", base_url="http://t:8080") as c:
        out = c.send_event("orders.created", {"id": 1})
    assert out.event_id == "ev1" and out.delivery_ids == ["d1"]
    assert route.calls.last.request.headers["authorization"] == "Bearer hk_x"
    assert "hookrail-python/" in route.calls.last.request.headers["user-agent"]
    assert route.calls.last.request.headers["idempotency-key"]


@respx.mock
def test_send_event_retries_503_with_identical_bytes_and_key() -> None:
    route = respx.post("http://t:8080/v1/events").mock(
        side_effect=[
            httpx.Response(503, json={"title": "down"}),
            httpx.Response(202, json={"event_id": "ev1", "delivery_ids": []}),
        ]
    )
    with Hookrail(api_key="hk_x", base_url="http://t:8080") as c:
        c.send_event("orders.created", {"id": 1}, idempotency_key="key-123")
    first, second = route.calls[0].request, route.calls[1].request
    assert first.content == second.content  # byte-identical body
    assert first.headers["idempotency-key"] == second.headers["idempotency-key"] == "key-123"


@respx.mock
def test_send_event_does_not_retry_409() -> None:
    route = respx.post("http://t:8080/v1/events").mock(
        return_value=httpx.Response(409, json={"title": "idempotency conflict"})
    )
    with Hookrail(api_key="hk_x", base_url="http://t:8080") as c, pytest.raises(ConflictError):
        c.send_event("orders.created", {"id": 1})
    assert route.call_count == 1


@respx.mock
def test_replayed_header_sets_flag() -> None:
    respx.post("http://t:8080/v1/events").mock(
        return_value=httpx.Response(
            202, json={"event_id": "ev1", "delivery_ids": []}, headers={"Idempotent-Replay": "true"}
        )
    )
    with Hookrail(api_key="hk_x", base_url="http://t:8080") as c:
        out = c.send_event("orders.created", {"id": 1})
    assert out.replayed is True


@respx.mock
def test_persistent_503_raises_retry_error_wrapping_server_error() -> None:
    from hookrail.errors import ServerError

    respx.post("http://t:8080/v1/events").mock(
        return_value=httpx.Response(503, json={"title": "down"})
    )
    with Hookrail(api_key="hk_x", base_url="http://t:8080", retries=2) as c:  # noqa: SIM117
        with pytest.raises(RetryError) as exc:
            c.send_event("orders.created", {"id": 1})
    assert isinstance(exc.value.__cause__, ServerError)  # last typed error preserved as cause


@respx.mock
def test_get_event_parses_status() -> None:
    respx.get("http://t:8080/v1/events/ev1").mock(
        return_value=httpx.Response(
            200,
            json={
                "event_id": "ev1",
                "topic": "orders.created",
                "deliveries": [
                    {
                        "delivery_id": "d1",
                        "state": "succeeded",
                        "attempts_truncated": False,
                        "attempts": [],
                    }
                ],
            },
        )
    )
    with Hookrail(api_key="hk_x", base_url="http://t:8080") as c:
        st = c.get_event("ev1")
    assert st.deliveries[0].state is DeliveryState.succeeded
