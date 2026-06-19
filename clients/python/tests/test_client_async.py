import httpx
import pytest
import respx

from hookrail import AsyncHookrail
from hookrail.errors import ConflictError


@respx.mock
@pytest.mark.asyncio
async def test_async_send_success() -> None:
    respx.post("http://t:8080/v1/events").mock(
        return_value=httpx.Response(202, json={"event_id": "ev1", "delivery_ids": []})
    )
    async with AsyncHookrail(api_key="hk_x", base_url="http://t:8080") as c:
        out = await c.send_event("orders.created", {"id": 1})
    assert out.event_id == "ev1"


@respx.mock
@pytest.mark.asyncio
async def test_async_retry_identical_bytes() -> None:
    route = respx.post("http://t:8080/v1/events").mock(
        side_effect=[
            httpx.Response(503, json={"title": "d"}),
            httpx.Response(202, json={"event_id": "ev1", "delivery_ids": []}),
        ]
    )
    async with AsyncHookrail(api_key="hk_x", base_url="http://t:8080") as c:
        await c.send_event("orders.created", {"id": 1}, idempotency_key="k1")
    assert route.calls[0].request.content == route.calls[1].request.content


@respx.mock
@pytest.mark.asyncio
async def test_async_no_retry_409() -> None:
    route = respx.post("http://t:8080/v1/events").mock(
        return_value=httpx.Response(409, json={"title": "conflict"})
    )
    async with AsyncHookrail(api_key="hk_x", base_url="http://t:8080") as c:
        with pytest.raises(ConflictError):
            await c.send_event("orders.created", {"id": 1})
    assert route.call_count == 1
