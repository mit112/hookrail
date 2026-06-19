"""Receiver-side verification of the Hookrail webhook signature (mirrors internal/signing).

Header: hookrail-signature: t=<unix>,v1=hex(HMAC_SHA256(secret, "<unix>.<delivery_id>." + body))
"""

from __future__ import annotations

import hashlib
import hmac
import time
from collections.abc import Sequence

from hookrail.errors import HookrailError

HEADER = "hookrail-signature"


class MalformedSignatureError(HookrailError):
    """The signature header could not be parsed."""


class SignatureTimestampError(HookrailError):
    """The signature timestamp is outside the allowed tolerance."""


class SignatureError(HookrailError):
    """No provided secret produced a matching signature."""


def _mac(secret: bytes, unix: int, delivery_id: str, body: bytes) -> bytes:
    m = hmac.new(secret, digestmod=hashlib.sha256)
    m.update(f"{unix}.{delivery_id}.".encode())
    m.update(body)
    return m.digest()


def verify_signature(
    secrets: bytes | Sequence[bytes],
    header: str,
    delivery_id: str,
    body: bytes,
    *,
    now: float | None = None,
    tolerance: float = 300.0,
) -> None:
    """Raise on failure; return None on success. `secrets` may be a single key or several
    (dual-secret rotation). `tolerance` is in seconds."""
    secret_list = [secrets] if isinstance(secrets, (bytes, bytearray)) else list(secrets)
    unix: int | None = None
    sig_hex = ""
    for part in header.split(","):
        k, _, v = part.strip().partition("=")
        if not _:
            continue
        if k == "t":
            try:
                unix = int(v)
            except ValueError:
                raise MalformedSignatureError("bad timestamp in signature header") from None
        elif k == "v1":
            sig_hex = v
    if unix is None or not sig_hex:
        raise MalformedSignatureError("missing t= or v1= in signature header")
    try:
        got = bytes.fromhex(sig_hex)
    except ValueError:
        raise MalformedSignatureError("v1 is not valid hex") from None
    if len(got) != hashlib.sha256().digest_size:
        raise MalformedSignatureError("v1 has wrong length")
    current = time.time() if now is None else now
    if abs(current - unix) > tolerance:
        raise SignatureTimestampError("signature timestamp outside tolerance")
    for secret in secret_list:
        if hmac.compare_digest(got, _mac(bytes(secret), unix, delivery_id, body)):
            return
    raise SignatureError("no matching signature")
