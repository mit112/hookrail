import hookrail


def test_version_is_a_string() -> None:
    assert isinstance(hookrail.__version__, str)
    assert hookrail.__version__.count(".") == 2
