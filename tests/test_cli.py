from __future__ import annotations

from pathlib import Path

import httpx
from typer.testing import CliRunner

from bazarrbulksync.cli import app
from bazarrbulksync.client import BazarrClient


ROOT = Path(__file__).resolve().parents[1]


def _write_config(tmp_path: Path) -> Path:
    config_path = tmp_path / "config.yml"
    config_path.write_text(
        "base_url: http://bazarr\n"
        "api_key: secret\n"
        "sync: {}\n"
        "patch: {}\n"
        "delete: {}\n",
        encoding="utf-8",
    )
    return config_path


def _movies_fixture() -> list[dict]:
    return [
        {
            "radarrId": 5,
            "title": "Test Movie",
            "subtitles": [
                {"name": "English", "code2": "en", "code3": "eng", "path": "/m/test.en.srt", "forced": False, "hi": False},
            ],
            "episodeFileCount": 0,
        }
    ]


def _make_handler(delete_called: list[str]) -> httpx.MockTransport:
    def handler(request: httpx.Request) -> httpx.Response:
        if request.method == "DELETE":
            delete_called.append(request.url.path)
            return httpx.Response(204)
        if request.url.path == "/api/movies":
            return httpx.Response(200, json={"data": _movies_fixture(), "total": 1})
        if request.url.path == "/api/series":
            return httpx.Response(200, json={"data": [], "total": 0})
        return httpx.Response(200, json={"data": [], "total": 0})
    return httpx.MockTransport(handler)


def test_delete_all_dry_run_does_not_call_delete(tmp_path: Path, monkeypatch) -> None:
    config_path = _write_config(tmp_path)
    delete_called: list[str] = []
    transport = _make_handler(delete_called)

    original_init = BazarrClient.__init__

    def patched_init(self, config, transport=None, **kwargs):
        original_init(self, config, transport=transport, **kwargs)

    monkeypatch.setattr("bazarrbulksync.cli.BazarrClient.__init__", lambda self, config: original_init(self, config, transport=transport))

    runner = CliRunner()
    result = runner.invoke(
        app,
        [
            "delete", "all",
            "--config", str(config_path),
            "--dry-run",
            "--media-type", "movies",
        ],
    )

    assert result.exit_code == 0, result.output
    assert delete_called == []
    assert "Dry run" in result.output


def test_delete_all_help_shows_command() -> None:
    runner = CliRunner()
    result = runner.invoke(app, ["delete", "all", "--help"])
    assert result.exit_code == 0
    assert "delete" in result.output.lower()


def test_delete_before_help_shows_command() -> None:
    runner = CliRunner()
    result = runner.invoke(app, ["delete", "before", "--help"])
    assert result.exit_code == 0
    assert "delete" in result.output.lower()
