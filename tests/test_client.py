from __future__ import annotations

import json
from pathlib import Path
from urllib.parse import parse_qs

import httpx
import pytest

from bazarrbulksync.client import BazarrClient
from bazarrbulksync.config import AppConfig
from bazarrbulksync.models import DeleteOptions, ItemType, PatchOptions, Subtitle, SyncOptions


ROOT = Path(__file__).resolve().parents[1]


def fixture(path: str) -> dict:
    return json.loads((ROOT / "responses" / path).read_text(encoding="utf-8"))


def test_client_paginates_series_and_sends_api_key() -> None:
    seen_keys: list[str | None] = []

    def handler(request: httpx.Request) -> httpx.Response:
        seen_keys.append(request.headers.get("X-API-KEY"))
        params = parse_qs(request.url.query.decode())
        start = int(params.get("start", ["0"])[0])
        body = fixture("series/multiple.txt") if start == 0 else {"data": [], "total": 3}
        return httpx.Response(200, json=body)

    client = BazarrClient(AppConfig(base_url="http://bazarr", api_key="secret"), transport=httpx.MockTransport(handler))

    chunks = list(client.iter_series(chunk_size=3))

    assert len(chunks) == 1
    assert chunks[0][0].title.startswith("The 100 Girlfriends")
    assert seen_keys == ["secret", "secret"]


def test_sync_subtitle_uses_bazarr_patch_payload() -> None:
    captured: dict[str, list[str]] = {}

    def handler(request: httpx.Request) -> httpx.Response:
        nonlocal captured
        captured = dict(parse_qs(request.content.decode(), keep_blank_values=True))
        return httpx.Response(204)

    client = BazarrClient(AppConfig(base_url="http://bazarr", api_key="secret"), transport=httpx.MockTransport(handler))
    subtitle = Subtitle(name="Japanese", code2="ja", code3="jpn", path="/media/file.ja.srt", forced=False, hi=True)

    client.sync_subtitle(
        item_type=ItemType.MOVIE,
        item_id=3,
        subtitle=subtitle,
        options=SyncOptions(max_offset_seconds=90, no_fix_framerate=True, gss=True, reference="a:0"),
    )

    assert captured["action"] == ["sync"]
    assert captured["language"] == ["ja"]
    assert captured["type"] == ["movie"]
    assert captured["id"] == ["3"]
    assert captured["forced"] == ["False"]
    assert captured["hi"] == ["True"]
    assert captured["max_offset_seconds"] == ["90"]
    assert captured["no_fix_framerate"] == ["True"]
    assert captured["gss"] == ["True"]
    assert captured["reference"] == ["a:0"]


def test_patch_subtitle_translate_payload() -> None:
    captured: dict[str, list[str]] = {}

    def handler(request: httpx.Request) -> httpx.Response:
        nonlocal captured
        captured = dict(parse_qs(request.content.decode(), keep_blank_values=True))
        return httpx.Response(204)

    client = BazarrClient(AppConfig(base_url="http://bazarr", api_key="secret"), transport=httpx.MockTransport(handler))
    subtitle = Subtitle(name="English", code2="en", code3="eng", path="/media/file.en.srt", forced=False, hi=True)

    client.patch_subtitle(
        action="translate",
        item_type=ItemType.MOVIE,
        item_id=3,
        subtitle=subtitle,
        options=PatchOptions(language="ja", original_format=True),
    )

    assert captured["action"] == ["translate"]
    assert captured["language"] == ["ja"]
    assert captured["type"] == ["movie"]
    assert captured["id"] == ["3"]
    assert captured["forced"] == ["False"]
    assert captured["hi"] == ["True"]
    assert captured["original_format"] == ["True"]
    assert "max_offset_seconds" not in captured
    assert "no_fix_framerate" not in captured
    assert "gss" not in captured
    assert "reference" not in captured


def test_patch_subtitle_mod_action_payload() -> None:
    captured: dict[str, list[str]] = {}

    def handler(request: httpx.Request) -> httpx.Response:
        nonlocal captured
        captured = dict(parse_qs(request.content.decode(), keep_blank_values=True))
        return httpx.Response(204)

    client = BazarrClient(AppConfig(base_url="http://bazarr", api_key="secret"), transport=httpx.MockTransport(handler))
    subtitle = Subtitle(name="English", code2="en", code3="eng", path="/media/file.en.srt", forced=True, hi=False)

    client.patch_subtitle(
        action="remove_HI",
        item_type=ItemType.EPISODE,
        item_id=10,
        subtitle=subtitle,
        options=PatchOptions(),
    )

    assert captured["action"] == ["remove_HI"]
    assert captured["language"] == ["en"]
    assert captured["type"] == ["episode"]
    assert captured["id"] == ["10"]
    assert captured["forced"] == ["True"]
    assert captured["hi"] == ["False"]
    assert "original_format" not in captured
    assert "max_offset_seconds" not in captured
    assert "no_fix_framerate" not in captured
    assert "gss" not in captured
    assert "reference" not in captured


def test_get_episodes_for_multiple_series_uses_repeated_series_ids() -> None:
    captured: dict[str, list[str]] = {}

    def handler(request: httpx.Request) -> httpx.Response:
        nonlocal captured
        captured = parse_qs(request.url.query.decode())
        return httpx.Response(200, json=fixture("series_episodes/single.txt"))

    client = BazarrClient(AppConfig(base_url="http://bazarr", api_key="secret"), transport=httpx.MockTransport(handler))

    episodes = client.get_episodes_for_series_ids([32, 148])

    assert captured["seriesid[]"] == ["32", "148"]
    assert episodes[0].sonarr_series_id == 32


def test_get_episodes_by_ids_sends_episodeid_params() -> None:
    captured: dict[str, list[str]] = {}

    def handler(request: httpx.Request) -> httpx.Response:
        nonlocal captured
        captured = parse_qs(request.url.query.decode())
        return httpx.Response(200, json=fixture("series_episodes/single.txt"))

    client = BazarrClient(AppConfig(base_url="http://bazarr", api_key="secret"), transport=httpx.MockTransport(handler))

    client.get_episodes_by_ids([1, 2, 3])

    assert captured["episodeid[]"] == ["1", "2", "3"]


def test_get_series_and_movies_use_id_array_params() -> None:
    captured: dict[str, list[str]] = {}

    def handler(request: httpx.Request) -> httpx.Response:
        nonlocal captured
        captured = parse_qs(request.url.query.decode())
        return httpx.Response(200, json={"data": [], "total": 0})

    client = BazarrClient(AppConfig(base_url="http://bazarr", api_key="secret"), transport=httpx.MockTransport(handler))

    client.get_series([10, 11])
    assert captured["seriesid[]"] == ["10", "11"]

    client.get_movies([20])
    assert captured["radarrid[]"] == ["20"]


def test_delete_subtitle_movie_uses_movies_endpoint() -> None:
    captured: dict[str, list[str]] = {}
    captured_method: list[str] = []
    captured_path: list[str] = []

    def handler(request: httpx.Request) -> httpx.Response:
        captured_method.append(request.method)
        captured_path.append(request.url.path)
        captured.update(parse_qs(request.url.query.decode()))
        return httpx.Response(204)

    client = BazarrClient(AppConfig(base_url="http://bazarr", api_key="secret"), transport=httpx.MockTransport(handler))
    subtitle = Subtitle(name="English", code2="en", code3="eng", path="/media/movie.en.srt", forced=False, hi=True)

    client.delete_subtitle(
        item_type=ItemType.MOVIE,
        item_id=3,
        subtitle=subtitle,
        options=DeleteOptions(),
    )

    assert captured_method == ["DELETE"]
    assert captured_path == ["/api/movies/subtitles"]
    assert captured["radarrid"] == ["3"]
    assert captured["language"] == ["en"]
    assert captured["forced"] == ["False"]
    assert captured["hi"] == ["True"]
    assert captured["path"] == ["/media/movie.en.srt"]


def test_delete_subtitle_episode_uses_episodes_endpoint() -> None:
    captured: dict[str, list[str]] = {}
    captured_method: list[str] = []
    captured_path: list[str] = []

    def handler(request: httpx.Request) -> httpx.Response:
        captured_method.append(request.method)
        captured_path.append(request.url.path)
        captured.update(parse_qs(request.url.query.decode()))
        return httpx.Response(204)

    client = BazarrClient(AppConfig(base_url="http://bazarr", api_key="secret"), transport=httpx.MockTransport(handler))
    subtitle = Subtitle(name="English", code2="en", code3="eng", path="/media/ep.en.srt", forced=True, hi=False)

    client.delete_subtitle(
        item_type=ItemType.EPISODE,
        item_id=10,
        subtitle=subtitle,
        options=DeleteOptions(language="ja"),
        series_id=20,
    )

    assert captured_method == ["DELETE"]
    assert captured_path == ["/api/episodes/subtitles"]
    assert captured["seriesid"] == ["20"]
    assert captured["episodeid"] == ["10"]
    assert captured["language"] == ["ja"]
    assert captured["forced"] == ["True"]
    assert captured["hi"] == ["False"]
    assert captured["path"] == ["/media/ep.en.srt"]


def test_delete_subtitle_episode_without_series_id_raises() -> None:
    client = BazarrClient(AppConfig(base_url="http://bazarr", api_key="secret"), transport=httpx.MockTransport(lambda r: httpx.Response(204)))
    subtitle = Subtitle(name="English", code2="en", code3="eng", path="/media/ep.en.srt")

    with pytest.raises(ValueError, match="series_id"):
        client.delete_subtitle(
            item_type=ItemType.EPISODE,
            item_id=10,
            subtitle=subtitle,
            options=DeleteOptions(),
        )


def test_delete_subtitle_without_path_raises() -> None:
    client = BazarrClient(AppConfig(base_url="http://bazarr", api_key="secret"), transport=httpx.MockTransport(lambda r: httpx.Response(204)))
    subtitle = Subtitle(name="English", code2="en", code3="eng", path=None)

    with pytest.raises(ValueError, match="path"):
        client.delete_subtitle(
            item_type=ItemType.MOVIE,
            item_id=3,
            subtitle=subtitle,
            options=DeleteOptions(),
        )

