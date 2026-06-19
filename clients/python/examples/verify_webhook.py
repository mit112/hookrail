from hookrail import verify_signature
from hookrail.signing import HEADER, SignatureError


# In your webhook receiver (e.g. a Flask/FastAPI route):
def handle(headers: dict[str, str], delivery_id: str, raw_body: bytes, secret: bytes) -> bool:
    try:
        verify_signature(secret, headers[HEADER], delivery_id, raw_body)
    except SignatureError:
        return False
    return True
