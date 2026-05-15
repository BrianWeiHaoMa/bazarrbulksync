from __future__ import annotations

import json
from pathlib import Path
from urllib.parse import parse_qs

import httpx

from bazarrbulksync.client import BazarrClient
from bazarrbulksync.config import AppConfig
from bazarrbulksync.models import ItemType, Subtitle, SyncOptions


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

