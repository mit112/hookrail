import os
import time

import pytest

from hookrail import AsyncHookrail, Hookrail
from hookrail.models import DeliveryState

pytestmark = pytest.mark.e2e

_BASE = os.environ.get("HOOKRAIL_BASE_URL", "http://localhost:8080")


def _poll_succeeded(client: Hookrail, event_id: str, deadline: float = 30.0) -> bool:
    end = time.monotonic() + deadline
    while time.monotonic() < end:
        st = client.get_event(event_id)
        if any(d.state is DeliveryState.succeeded for d in st.deliveries):
            return True
        time.sleep(1)
    return False


def test_sync_send_and_deliver() -> None:
    with Hookrail(base_url=_BASE) as c:  # HOOKRAIL_API_KEY from env
        accepted = c.send_event("demo.python.orders", {"id": "e2e-sync"})
        assert accepted.event_id
        assert _poll_succeeded(c, accepted.event_id), "delivery never reached succeeded"


@pytest.mark.asyncio
async def test_async_send() -> None:
    async with AsyncHookrail(base_url=_BASE) as c:
        accepted = await c.send_event("demo.python.orders", {"id": "e2e-async"})
        assert accepted.event_id
