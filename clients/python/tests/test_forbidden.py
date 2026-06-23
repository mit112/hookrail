from hookrail import ForbiddenError, HookrailAPIError
from hookrail._transport import is_retryable, map_to_error


def test_403_maps_to_forbidden_error():
    err = map_to_error(403, None, None)
    assert isinstance(err, ForbiddenError)
    assert isinstance(err, HookrailAPIError)
    assert err.status == 403


def test_403_is_not_retryable():
    assert is_retryable(403) is False
