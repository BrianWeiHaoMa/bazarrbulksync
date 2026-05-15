from __future__ import annotations

from dataclasses import dataclass, field
from datetime import datetime
from enum import StrEnum
from typing import Any


SYNC_ACTION = 5


class MediaType(StrEnum):
    SERIES = "series"
    MOVIES = "movies"
    ALL = "all"


class ItemType(StrEnum):
    EPISODE = "episode"
    MOVIE = "movie"


@dataclass(frozen=True)
class Subtitle:
    name: str | None
    code2: str
    code3: str | None
    path: str | None
    forced: bool = False
    hi: bool = False
    file_size: int | None = None

    @classmethod
    def from_api(cls, data: dict[str, Any]) -> Subtitle:
        return cls(
            name=data.get("name"),
            code2=str(data.get("code2") or ""),
            code3=data.get("code3"),
            path=data.get("path"),
            forced=bool(data.get("forced", False)),
            hi=bool(data.get("hi", False)),
            file_size=data.get("file_size"),
        )


@dataclass(frozen=True)
class Series:
    sonarr_series_id: int
    title: str
    episode_file_count: int = 0
    
    @classmethod
    def from_api(cls, data: dict[str, Any]) -> Series:
        return cls(
            sonarr_series_id=int(data["sonarrSeriesId"]),
            title=str(data.get("title") or ""),
            episode_file_count=int(data.get("episodeFileCount") or 0),
        )


@dataclass(frozen=True)
class Episode:
    sonarr_episode_id: int
    sonarr_series_id: int
    title: str
    season: int | None
    episode: int | None
    subtitles: tuple[Subtitle, ...] = ()

    @classmethod
    def from_api(cls, data: dict[str, Any]) -> Episode:
        return cls(
            sonarr_episode_id=int(data["sonarrEpisodeId"]),
            sonarr_series_id=int(data["sonarrSeriesId"]),
            title=str(data.get("title") or ""),
            season=data.get("season"),
            episode=data.get("episode"),
            subtitles=tuple(Subtitle.from_api(item) for item in data.get("subtitles") or ()),
        )


@dataclass(frozen=True)
class Movie:
    radarr_id: int
    title: str
    subtitles: tuple[Subtitle, ...] = ()

    @classmethod
    def from_api(cls, data: dict[str, Any]) -> Movie:
        return cls(
            radarr_id=int(data["radarrId"]),
            title=str(data.get("title") or ""),
            subtitles=tuple(Subtitle.from_api(item) for item in data.get("subtitles") or ()),
        )


@dataclass(frozen=True)
class HistoryEvent:
    item_type: ItemType
    item_id: int
    action: int
    parsed_timestamp: datetime | None

    @classmethod
    def episode_from_api(cls, data: dict[str, Any]) -> HistoryEvent:
        return cls(
            item_type=ItemType.EPISODE,
            item_id=int(data["sonarrEpisodeId"]),
            action=int(data.get("action", 0)),
            parsed_timestamp=parse_bazarr_datetime(data.get("parsed_timestamp")),
        )

    @classmethod
    def movie_from_api(cls, data: dict[str, Any]) -> HistoryEvent:
        return cls(
            item_type=ItemType.MOVIE,
            item_id=int(data["radarrId"]),
            action=int(data.get("action", 0)),
            parsed_timestamp=parse_bazarr_datetime(data.get("parsed_timestamp")),
        )


@dataclass(frozen=True)
class SyncOptions:
    language: str | None = None
    forced: bool | None = None
    hi: bool | None = None
    max_offset_seconds: int = 60
    no_fix_framerate: bool = False
    gss: bool = False
    reference: str | None = None

    def forced_for(self, subtitle: Subtitle) -> bool:
        return subtitle.forced if self.forced is None else self.forced

    def hi_for(self, subtitle: Subtitle) -> bool:
        return subtitle.hi if self.hi is None else self.hi


@dataclass(frozen=True)
class SyncJob:
    item_type: ItemType
    item_id: int
    title: str
    subtitle: Subtitle
    season: int | None = None
    episode: int | None = None

    @property
    def display_name(self) -> str:
        if self.item_type == ItemType.EPISODE and self.season is not None and self.episode is not None:
            return f"{self.title} S{self.season:02d}E{self.episode:02d}"
        return self.title


@dataclass
class SyncProgress:
    completed: int
    total: int
    job: SyncJob | None
    result: SyncResult | None = None


@dataclass(frozen=True)
class SyncResult:
    job: SyncJob
    status: str
    message: str = ""


@dataclass
class SyncSummary:
    results: list[SyncResult] = field(default_factory=list)

    @property
    def total(self) -> int:
        return len(self.results)

    @property
    def synced(self) -> int:
        return self.count("synced")

    @property
    def skipped(self) -> int:
        return self.count("skipped")

    @property
    def failed(self) -> int:
        return self.count("failed")

    @property
    def dry_run(self) -> int:
        return self.count("dry-run")

    def count(self, status: str) -> int:
        return sum(1 for result in self.results if result.status == status)


def parse_bazarr_datetime(value: str | None) -> datetime | None:
    if not value:
        return None

    formats = (
        "%m/%d/%y %H:%M:%S",
        "%m/%d/%Y %H:%M:%S",
        "%Y-%m-%d %H:%M:%S",
        "%Y-%m-%dT%H:%M:%S",
    )
    for fmt in formats:
        try:
            return datetime.strptime(value, fmt)
        except ValueError:
            continue
    return None


def parse_before_argument(value: str) -> datetime:
    """Parse ``sync before`` threshold from the CLI (naive local).

    Accepts an ISO calendar date or a date with time in the formats 'YYYY-MM-DD' or 'YYYY-MM-DD HH:MM:SS'.
    Returns a naive local datetime object.
    Raises ValueError if the value cannot be parsed.
    """

    stripped = value.strip()
    if not stripped:
        raise ValueError("Before value is empty")

    for fmt in (
        "%Y-%m-%d %H:%M:%S",
        "%Y-%m-%d"
    ):
        try:
            return datetime.strptime(stripped, fmt)
        except ValueError:
            continue

    raise ValueError("Expected date or datetime, e.g. YYYY-MM-DD or YYYY-MM-DD HH:MM:SS")

