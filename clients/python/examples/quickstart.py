from hookrail import Hookrail

with Hookrail(api_key="hk_...", base_url="https://hooks.example.com") as client:
    accepted = client.send_event("orders.created", {"order_id": "o_1", "amount": 4200})
    print("event:", accepted.event_id, "replayed:", accepted.replayed)
    status = client.get_event(accepted.event_id)
    for d in status.deliveries:
        print(d.delivery_id, d.state)
