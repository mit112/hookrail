# hookrail-python

Python client for [Hookrail](https://github.com/mit112/hookrail) — durable,
at-least-once webhook delivery. Covers the public producer surface: send events,
check delivery status, and verify incoming webhook signatures.

Current release: **0.1.0**. To self-host the service, see the
[Hookrail quickstart](https://github.com/mit112/hookrail#quickstart-60-seconds).

---

## Install

```bash
pip install hookrail
# or
uv add hookrail
```

Requires Python **3.10–3.13**.

---

## Quickstart (sync)

```python
from hookrail import Hookrail

with Hookrail(api_key="hk_...", base_url="https://hooks.example.com") as client:
    accepted = client.send_event("orders.created", {"order_id": "o_1", "amount": 4200})
    print("event:", accepted.event_id, "replayed:", accepted.replayed)
    status = client.get_event(accepted.event_id)
    for d in status.deliveries:
        print(d.delivery_id, d.state)
```

## Quickstart (async)

```python
import asyncio

from hookrail import AsyncHookrail


async def main() -> None:
    async with AsyncHookrail(api_key="hk_...", base_url="https://hooks.example.com") as client:
        accepted = await client.send_event("orders.created", {"order_id": "o_1"})
        print(accepted.event_id)


asyncio.run(main())
```

---

## Error model

Every SDK error derives from `HookrailError`.

| Exception | HTTP status | Meaning |
|---|---|---|
| `HookrailConfigError` | — | Invalid client construction (missing key, bad URL, etc.) |
| `HookrailConnectionError` | — | Connect / DNS / TLS failure |
| `HookrailTimeoutError` | — | Connect / read / write timeout |
| `BadRequestError` | 400 | Malformed request body or topic |
| `AuthenticationError` | 401 | Missing or invalid `hk_…` producer key |
| `NotFoundError` | 404 | Event not found (also masks certain server outages — see caveats) |
| `ConflictError` | 409 | Idempotency key collision with different payload bytes |
| `PayloadTooLargeError` | 413 | Event payload exceeds server limit |
| `RateLimitError` | 429 | Rate limited; carries `retry_after` (seconds) |
| `ServerError` | 5xx | Server-side error |
| `RetryError` | — | All retries exhausted; wraps the last error as `__cause__` |

HTTP 4xx errors that are not retried: 400, 401, 404, 409, 413.

---

## Retry and idempotency

- **Automatic retries** on transport errors, 408, 425, 429, and all 5xx statuses.
- **Full-jitter capped exponential backoff** with configurable `RetryPolicy`. The
  `Retry-After` header is honoured as a floor (jitter applied on top).
- **Byte-identical retries**: if the server has already processed the event
  (same idempotency key + identical raw bytes), you receive a `202` with
  `replayed=True` — no error. If the bytes differ, a `409 Conflict` is returned.
- **Idempotency keys** are automatically generated per request. You do not need
  to supply one.

---

## Verifying webhook signatures

```python
from hookrail import verify_signature
from hookrail.signing import HEADER, SignatureError


def handle(headers: dict[str, str], delivery_id: str, raw_body: bytes, secret: bytes) -> bool:
    try:
        verify_signature(secret, headers[HEADER], delivery_id, raw_body)
    except SignatureError:
        return False
    return True
```

The `verify_signature` helper supports dual-secret rotation (pass a list of
`bytes`), configurable timestamp tolerance (default 300 s), and raises typed
exceptions (`SignatureError`, `SignatureTimestampError`,
`MalformedSignatureError`).

---

## Caveats / residual risk

- `get_event` returning 404 can mask a transient server outage (known server
  bug). Production code should treat a 404 from `get_event` as ambiguous — the
  event may still have been accepted.
- Per-worker rate limits apply at the server; the client respects
  `Retry-After` but does not coordinate across processes.

---

## License

Apache-2.0 — see [LICENSE](LICENSE).
