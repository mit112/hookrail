import pytest

from hookrail._config import ClientConfig
from hookrail.errors import HookrailConfigError


def test_param_beats_env(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setenv("HOOKRAIL_API_KEY", "hk_env")
    monkeypatch.setenv("HOOKRAIL_BASE_URL", "http://env:8080")
    cfg = ClientConfig.resolve(api_key="hk_param", base_url="http://param:8080")
    assert cfg.api_key == "hk_param"
    assert cfg.base_url == "http://param:8080"


def test_env_used_when_no_param(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setenv("HOOKRAIL_API_KEY", "hk_env")
    monkeypatch.delenv("HOOKRAIL_BASE_URL", raising=False)
    cfg = ClientConfig.resolve()
    assert cfg.api_key == "hk_env"
    assert cfg.base_url == "http://localhost:8080"


def test_missing_key_raises(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.delenv("HOOKRAIL_API_KEY", raising=False)
    with pytest.raises(HookrailConfigError):
        ClientConfig.resolve()


def test_bad_base_url_raises(monkeypatch: pytest.MonkeyPatch) -> None:
    with pytest.raises(HookrailConfigError):
        ClientConfig.resolve(api_key="hk_x", base_url="notaurl")


def test_user_agent_default_contains_version() -> None:
    cfg = ClientConfig.resolve(api_key="hk_x")
    assert cfg.user_agent.startswith("hookrail-python/")
