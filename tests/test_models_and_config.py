from __future__ import annotations

import json
from datetime import datetime
from pathlib import Path

import pytest

from bazarrbulksync.config import DEFAULT_LOG_FILE_PATH, effective_log_file_path, parse_config
from bazarrbulksync.models import Episode, Movie, Series, parse_bazarr_datetime, parse_before_argument


ROOT = Path(__file__).resolve().parents[1]


def load_fixture(path: str) -> dict:
    return json.loads((ROOT / "responses" / path).read_text(encoding="utf-8"))


def test_models_parse_response_fixtures() -> None:
    series = Series.from_api(load_fixture("series/single.txt")["data"][0])
    episode = Episode.from_api(load_fixture("series_episodes/single.txt")["data"][0])
    movie = Movie.from_api(load_fixture("movies/single.txt")["data"][0])

    assert series.sonarr_series_id == 32
    assert series.episode_file_count == 12
    assert episode.sonarr_episode_id == 3225
    last_path = episode.subtitles[-1].path
    assert last_path is not None
    assert last_path.endswith(".ja.srt")
    assert movie.radarr_id == 3
    assert movie.subtitles[0].path is None


def test_config_parses_defaults_and_sync_options() -> None:
    config = parse_config(
        {
            "base_url": "http://bazarr.local/",
            "api_key": "secret",
            "series_chunk_size": 25,
            "movies_chunk_size": 50,
            "episodes_chunk_size": 75,
            "sync": {
                "language": "ja",
                "forced": None,
                "hi": False,
                "max_offset_seconds": 90,
                "no_fix_framerate": True,
                "gss": True,
            },
        }
    )

    assert config.api_url == "http://bazarr.local/api"
    assert config.series_chunk_size == 25
    assert config.movies_chunk_size == 50
    assert config.episodes_chunk_size == 75
    assert config.before_history_batch_size == 3000
    assert config.log_enabled is False
    assert config.log_file is None
    assert config.log_debug is False
    assert config.sync_options.language == "ja"
    assert config.sync_options.forced is None
    assert config.sync_options.hi is False
    assert config.sync_options.no_fix_framerate is True


def test_missing_specific_chunk_sizes_use_defaults() -> None:
    config = parse_config({"base_url": "http://bazarr.local/", "api_key": "secret"})

    assert config.series_chunk_size == 3000
    assert config.movies_chunk_size == 3000
    assert config.episodes_chunk_size == 6000
    assert config.before_history_batch_size == 3000
    assert config.log_enabled is False
    assert config.log_file is None
    assert config.log_debug is False


def test_effective_log_file_path_respects_log_enabled() -> None:
    off = parse_config({"base_url": "http://bazarr.local/", "api_key": "secret", "sync": {}})
    assert effective_log_file_path(off) is None

    off_with_path = parse_config(
        {
            "base_url": "http://bazarr.local/",
            "api_key": "secret",
            "log_enabled": False,
            "log_file": "/tmp/ignored.log",
            "sync": {},
        }
    )
    assert effective_log_file_path(off_with_path) is None

    default_path = parse_config(
        {
            "base_url": "http://bazarr.local/",
            "api_key": "secret",
            "log_enabled": True,
            "sync": {},
        }
    )
    assert effective_log_file_path(default_path) == DEFAULT_LOG_FILE_PATH


def test_log_file_and_log_debug_from_config() -> None:
    config = parse_config(
        {
            "base_url": "http://bazarr.local/",
            "api_key": "secret",
            "log_enabled": True,
            "log_file": "/var/log/bazarrbulksync/sync.log",
            "log_debug": True,
            "sync": {},
        }
    )

    assert config.log_enabled is True
    assert config.log_file == Path("/var/log/bazarrbulksync/sync.log")
    assert config.log_debug is True
    assert effective_log_file_path(config) == Path("/var/log/bazarrbulksync/sync.log")


def test_before_history_batch_size_can_be_set() -> None:
    config = parse_config(
        {
            "base_url": "http://bazarr.local/",
            "api_key": "secret",
            "before_history_batch_size": 25,
        }
    )

    assert config.before_history_batch_size == 25


def test_legacy_since_history_batch_size_yaml_key_still_works() -> None:
    config = parse_config(
        {
            "base_url": "http://bazarr.local/",
            "api_key": "secret",
            "since_history_batch_size": 40,
        }
    )

    assert config.before_history_batch_size == 40


def test_bazarr_datetime_parser_handles_fixture_format() -> None:
    parsed = parse_bazarr_datetime("09/24/25 16:59:06")

    assert parsed is not None
    assert parsed.year == 2025
    assert parsed.month == 9
    assert parsed.day == 24


def test_parse_before_argument_accepts_iso_date_and_datetime() -> None:
    assert parse_before_argument("2025-09-01") == datetime(2025, 9, 1, 0, 0, 0)
    assert parse_before_argument(" 2025-09-01 14:30:59 ") == datetime(2025, 9, 1, 14, 30, 59)


def test_parse_before_argument_rejects_empty_and_garbage() -> None:
    with pytest.raises(ValueError, match="empty"):
        parse_before_argument("")
    with pytest.raises(ValueError, match="Expected date or datetime"):
        parse_before_argument("not-a-date")
