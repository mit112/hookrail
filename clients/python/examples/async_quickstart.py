import asyncio

from hookrail import AsyncHookrail


async def main() -> None:
    async with AsyncHookrail(api_key="hk_...", base_url="https://hooks.example.com") as client:
        accepted = await client.send_event("orders.created", {"order_id": "o_1"})
        print(accepted.event_id)


asyncio.run(main())
