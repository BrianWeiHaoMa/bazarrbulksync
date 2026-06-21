from __future__ import annotations

import json
from datetime import datetime
from pathlib import Path

import pytest

from bazarrbulksync.config import DEFAULT_LOG_FILE_PATH, effective_log_file_path, parse_config
from bazarrbulksync.models import (
    ACTION_HISTORY_CODES,
    MOD_ACTIONS,
    DeleteOptions,
    PatchAction,
    PatchOptions,
    Subtitle,
    action_has_history,
    action_history_code,
    Episode,
    Movie,
    Series,
    parse_bazarr_datetime,
    parse_before_argument,
)


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


def test_config_parses_patch_options() -> None:
    config = parse_config(
        {
            "base_url": "http://bazarr.local/",
            "api_key": "secret",
            "patch": {
                "language": "ja",
                "forced": True,
                "hi": False,
                "original_format": True,
            },
        }
    )

    assert config.patch_options.language == "ja"
    assert config.patch_options.forced is True
    assert config.patch_options.hi is False
    assert config.patch_options.original_format is True


def test_config_patch_options_default_when_absent() -> None:
    config = parse_config({"base_url": "http://bazarr.local/", "api_key": "secret"})

    assert config.patch_options.language is None
    assert config.patch_options.forced is None
    assert config.patch_options.hi is None
    assert config.patch_options.original_format is None


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


def test_patch_action_enum_values() -> None:
    assert PatchAction.TRANSLATE == "translate"
    assert PatchAction.REMOVE_HI == "remove_HI"
    assert PatchAction.OCR_FIXES == "OCR_fixes"
    assert PatchAction.FIX_UPPERCASE == "fix_uppercase"
    assert PatchAction.REVERSE_RTL == "reverse_rtl"


def test_action_history_codes_mapping() -> None:
    assert ACTION_HISTORY_CODES["sync"] == 5
    assert ACTION_HISTORY_CODES["translate"] == 6
    assert ACTION_HISTORY_CODES["delete"] == 0
    for mod in MOD_ACTIONS:
        assert ACTION_HISTORY_CODES[mod] is None


def test_action_has_history_and_code() -> None:
    assert action_has_history("sync") is True
    assert action_has_history("translate") is True
    assert action_has_history("delete") is True
    assert action_has_history("remove_HI") is False
    assert action_has_history("OCR_fixes") is False
    assert action_history_code("translate") == 6
    assert action_history_code("delete") == 0
    assert action_history_code("remove_HI") is None
    assert action_history_code("nonexistent") is None


def test_patch_options_defaults_and_helpers() -> None:
    opts = PatchOptions()
    assert opts.language is None
    assert opts.forced is None
    assert opts.hi is None
    assert opts.original_format is None

    sub = Subtitle("English", "en", "eng", "/m.en.srt", forced=True, hi=False)
    assert opts.forced_for(sub) is True
    assert opts.hi_for(sub) is False

    opts_override = PatchOptions(forced=False, hi=True)
    assert opts_override.forced_for(sub) is False
    assert opts_override.hi_for(sub) is True


def test_config_parses_delete_options() -> None:
    config = parse_config(
        {
            "base_url": "http://bazarr.local/",
            "api_key": "secret",
            "delete": {
                "language": "ja",
                "forced": True,
                "hi": False,
            },
        }
    )

    assert config.delete_options.language == "ja"
    assert config.delete_options.forced is True
    assert config.delete_options.hi is False


def test_config_delete_options_default_when_absent() -> None:
    config = parse_config({"base_url": "http://bazarr.local/", "api_key": "secret"})

    assert config.delete_options.language is None
    assert config.delete_options.forced is None
    assert config.delete_options.hi is None


def test_delete_options_defaults_and_helpers() -> None:
    opts = DeleteOptions()
    assert opts.language is None
    assert opts.forced is None
    assert opts.hi is None

    sub = Subtitle("English", "en", "eng", "/m.en.srt", forced=True, hi=False)
    assert opts.forced_for(sub) is True
    assert opts.hi_for(sub) is False

    opts_override = DeleteOptions(forced=False, hi=True)
    assert opts_override.forced_for(sub) is False
    assert opts_override.hi_for(sub) is True
