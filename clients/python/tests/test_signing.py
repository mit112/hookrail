import json
from pathlib import Path

import pytest

from hookrail.signing import (
    MalformedSignatureError,
    SignatureError,
    SignatureTimestampError,
    verify_signature,
)

_FIX = json.loads((Path(__file__).parent / "fixtures" / "signature.json").read_text())
_SECRET = _FIX["secret"].encode()
_DID = _FIX["delivery_id"]
_BODY = _FIX["body"].encode()
_HEADER = _FIX["header"]
_NOW = float(_FIX["unix"])


def test_valid_signature_passes() -> None:
    verify_signature(_SECRET, _HEADER, _DID, _BODY, now=_NOW, tolerance=300.0)


def test_tampered_body_fails() -> None:
    with pytest.raises(SignatureError):
        verify_signature(_SECRET, _HEADER, _DID, b'{"order_id":"o_1","amount":9999}', now=_NOW)


def test_wrong_delivery_id_fails() -> None:
    with pytest.raises(SignatureError):
        verify_signature(_SECRET, _HEADER, "01JOTHER0000000000000000", _BODY, now=_NOW)


def test_out_of_tolerance_fails() -> None:
    with pytest.raises(SignatureTimestampError):
        verify_signature(_SECRET, _HEADER, _DID, _BODY, now=_NOW + 3600, tolerance=300.0)


def test_malformed_header_fails() -> None:
    with pytest.raises(MalformedSignatureError):
        verify_signature(_SECRET, "garbage", _DID, _BODY, now=_NOW)


def test_dual_secret_rotation() -> None:
    verify_signature([b"new_secret", _SECRET], _HEADER, _DID, _BODY, now=_NOW)
