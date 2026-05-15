from __future__ import annotations

from dataclasses import dataclass, replace
from pathlib import Path
from typing import Any

import yaml

from .models import MediaType, SyncOptions


DEFAULT_CONFIG_PATH = Path("./bazarrbulksync/bazarrbulksync_config.yml")

DEFAULT_LOG_FILE_PATH = Path("./bazarrbulksync/bazarrbulksync.log")


@dataclass(frozen=True)
class AppConfig:
    base_url: str
    api_key: str
    series_chunk_size: int = 3000
    movies_chunk_size: int = 3000
    episodes_chunk_size: int = 6000
    before_history_batch_size: int = 3000
    timeout_seconds: float = 30.0
    retries: int = 2
    media_type: MediaType = MediaType.ALL
    log_enabled: bool = False
    log_file: Path | None = None
    log_debug: bool = False
    sync_options: SyncOptions = SyncOptions()

    @property
    def api_url(self) -> str:
        return f"{self.base_url.rstrip('/')}/api"

    def with_overrides(
        self,
        *,
        series_chunk_size: int | None = None,
        movies_chunk_size: int | None = None,
        episodes_chunk_size: int | None = None,
        before_history_batch_size: int | None = None,
        media_type: MediaType | None = None,
        sync_options: SyncOptions | None = None,
    ) -> AppConfig:
        return replace(
            self,
            series_chunk_size=series_chunk_size if series_chunk_size is not None else self.series_chunk_size,
            movies_chunk_size=movies_chunk_size if movies_chunk_size is not None else self.movies_chunk_size,
            episodes_chunk_size=episodes_chunk_size if episodes_chunk_size is not None else self.episodes_chunk_size,
            before_history_batch_size=(
                before_history_batch_size
                if before_history_batch_size is not None
                else self.before_history_batch_size
            ),
            media_type=media_type if media_type is not None else self.media_type,
            sync_options=sync_options or self.sync_options,
        )


def load_config(path: str | Path | None = None) -> AppConfig:
    config_path = resolve_config_path(path)
    if not config_path.exists():
        raise FileNotFoundError(f"Config file not found: {config_path}")

    with config_path.open("r", encoding="utf-8") as handle:
        raw = yaml.safe_load(handle) or {}

    if not isinstance(raw, dict):
        raise ValueError("Config file must contain a YAML mapping")

    return parse_config(raw)


def resolve_config_path(path: str | Path | None = None) -> Path:
    if path:
        return Path(path).expanduser()

    if DEFAULT_CONFIG_PATH.exists():
        return DEFAULT_CONFIG_PATH

    return Path("bazarrbulksync_config.yml")


def parse_config(raw: dict[str, Any]) -> AppConfig:
    base_url = raw.get("base_url")
    api_key = raw.get("api_key")
    if not base_url or not isinstance(base_url, str):
        raise ValueError("Config value 'base_url' is required")
    if not api_key or not isinstance(api_key, str):
        raise ValueError("Config value 'api_key' is required")

    sync_raw = raw.get("sync") or {}
    if not isinstance(sync_raw, dict):
        raise ValueError("Config value 'sync' must be a mapping")

    media_type = MediaType(str(raw.get("media_type", MediaType.ALL.value)))
    return AppConfig(
        base_url=base_url.rstrip("/"),
        api_key=api_key,
        series_chunk_size=_positive_int(raw.get("series_chunk_size", 3000), "series_chunk_size"),
        movies_chunk_size=_positive_int(raw.get("movies_chunk_size", 3000), "movies_chunk_size"),
        episodes_chunk_size=_positive_int(raw.get("episodes_chunk_size", 6000), "episodes_chunk_size"),
        before_history_batch_size=_positive_int(
            raw.get("before_history_batch_size", raw.get("since_history_batch_size", 3000)),
            "before_history_batch_size",
        ),
        timeout_seconds=float(raw.get("timeout_seconds", 30.0)),
        retries=max(0, int(raw.get("retries", 2))),
        media_type=media_type,
        log_enabled=bool(raw.get("log_enabled", False)),
        log_file=_optional_path(raw.get("log_file")),
        log_debug=bool(raw.get("log_debug", False)),
        sync_options=SyncOptions(
            language=_optional_str(sync_raw.get("language")),
            forced=_optional_bool(sync_raw.get("forced")),
            hi=_optional_bool(sync_raw.get("hi")),
            max_offset_seconds=_positive_int(sync_raw.get("max_offset_seconds", 60), "sync.max_offset_seconds"),
            no_fix_framerate=bool(sync_raw.get("no_fix_framerate", False)),
            gss=bool(sync_raw.get("gss", False)),
            reference=_optional_str(sync_raw.get("reference")),
        ),
        )


def effective_log_file_path(config: AppConfig) -> Path | None:
    if not config.log_enabled:
        return None
    return config.log_file or DEFAULT_LOG_FILE_PATH


def merge_sync_options(base: SyncOptions, **overrides: Any) -> SyncOptions:
    kwargs = {key: value for key, value in overrides.items() if value is not None}
    return replace(base, **kwargs)


def _optional_path(value: Any) -> Path | None:
    if value is None or value == "":
        return None
    return Path(str(value)).expanduser()


def _optional_str(value: Any) -> str | None:
    if value is None or value == "":
        return None
    return str(value)


def _optional_bool(value: Any) -> bool | None:
    if value is None:
        return None
    if isinstance(value, bool):
        return value
    if isinstance(value, str):
        normalized = value.strip().lower()
        if normalized in {"true", "1", "yes", "on"}:
            return True
        if normalized in {"false", "0", "no", "off"}:
            return False
    raise ValueError(f"Expected a boolean value, got {value!r}")


def _positive_int(value: Any, name: str) -> int:
    parsed = int(value)
    if parsed <= 0:
        raise ValueError(f"Config value '{name}' must be positive")
    return parsed

