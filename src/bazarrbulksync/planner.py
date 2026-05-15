from __future__ import annotations

import logging
from collections.abc import Iterable, Iterator
from datetime import date, datetime

from .client import BazarrClient
from .models import (
    SYNC_ACTION,
    Episode,
    HistoryEvent,
    ItemType,
    MediaType,
    Movie,
    Series,
    Subtitle,
    SyncJob,
    SyncOptions,
)
from .util import unique_in_order

logger = logging.getLogger(__name__)


class SyncPlanner:
    def __init__(self, client: BazarrClient) -> None:
        self.client = client

    def collect_job_item_ids(
        self,
        *,
        media_type: MediaType = MediaType.ALL,
        series_chunk_size: int | None = None,
        movies_chunk_size: int | None = None,
        episodes_chunk_size: int | None = None,
        options: SyncOptions = SyncOptions(),
    ) -> tuple[list[int], list[int]]:
        episode_ids: list[int] = []
        movie_ids: list[int] = []
        for job in self.iter_all_jobs(
            media_type=media_type,
            series_chunk_size=series_chunk_size,
            movies_chunk_size=movies_chunk_size,
            episodes_chunk_size=episodes_chunk_size,
            options=options,
        ):
            if job.item_type == ItemType.EPISODE:
                episode_ids.append(job.item_id)
            else:
                movie_ids.append(job.item_id)
        return episode_ids, movie_ids

    def iter_all_jobs(
        self,
        *,
        media_type: MediaType = MediaType.ALL,
        series_chunk_size: int | None = None,
        movies_chunk_size: int | None = None,
        episodes_chunk_size: int | None = None,
        options: SyncOptions = SyncOptions(),
    ) -> Iterator[SyncJob]:
        logger.debug("Planning all jobs media_type=%s", media_type.value)
        if media_type in {MediaType.SERIES, MediaType.ALL}:
            yield from self._iter_series_jobs(
                series_chunk_size=series_chunk_size,
                episodes_chunk_size=episodes_chunk_size,
                options=options,
            )

        if media_type in {MediaType.MOVIES, MediaType.ALL}:
            for movie_chunk in self.client.iter_movies(movies_chunk_size):
                logger.debug("Movies list chunk size=%s", len(movie_chunk))
                for movie in movie_chunk:
                    yield from jobs_for_movie(movie, options)

    def iter_before_jobs(
        self,
        before: date | datetime,
        episode_ids: Iterable[int],
        movie_ids: Iterable[int],
        *,
        media_type: MediaType = MediaType.ALL,
        series_chunk_size: int | None = None,
        movies_chunk_size: int | None = None,
        episodes_chunk_size: int | None = None,
        options: SyncOptions = SyncOptions(),
        history_lookup_batch_size: int | None = None,
    ) -> Iterator[SyncJob]:
        logger.info("Planning before jobs threshold=%s media_type=%s", before, media_type.value)
        threshold = _as_datetime(before)
        batch = history_lookup_batch_size or self.client.config.before_history_batch_size
        if batch <= 0:
            raise ValueError("history_lookup_batch_size must be positive")

        episode_syncs: dict[int, datetime] = {}
        movie_syncs: dict[int, datetime] = {}

        if media_type in {MediaType.SERIES, MediaType.ALL}:
            episode_syncs = _latest_syncs_for_episode_ids(self.client, episode_ids, batch)
            logger.debug("Episode history latest sync entries=%s", len(episode_syncs))

        if media_type in {MediaType.MOVIES, MediaType.ALL}:
            movie_syncs = _latest_syncs_for_movie_ids(self.client, movie_ids, batch)
            logger.debug("Movie history latest sync entries=%s", len(movie_syncs))

        for job in self.iter_all_jobs(
            media_type=media_type,
            series_chunk_size=series_chunk_size,
            movies_chunk_size=movies_chunk_size,
            episodes_chunk_size=episodes_chunk_size,
            options=options,
        ):
            latest = episode_syncs.get(job.item_id) if job.item_type == ItemType.EPISODE else movie_syncs.get(job.item_id)
            if latest is None or latest < threshold:
                yield job

    def iter_before_jobs_from_jobs(
        self,
        before: date | datetime,
        jobs: Iterable[SyncJob],
        *,
        history_lookup_batch_size: int | None = None,
    ) -> Iterator[SyncJob]:
        """Like :meth:`iter_before_jobs`, but only considers the given jobs (e.g. from explicit IDs)."""

        job_list = list(jobs)
        threshold = _as_datetime(before)
        batch = history_lookup_batch_size or self.client.config.before_history_batch_size
        if batch <= 0:
            raise ValueError("history_lookup_batch_size must be positive")

        episode_item_ids = unique_in_order(job.item_id for job in job_list if job.item_type == ItemType.EPISODE)
        movie_item_ids = unique_in_order(job.item_id for job in job_list if job.item_type == ItemType.MOVIE)

        episode_syncs: dict[int, datetime] = {}
        movie_syncs: dict[int, datetime] = {}

        if episode_item_ids:
            episode_syncs = _latest_syncs_for_episode_ids(self.client, episode_item_ids, batch)
            logger.debug("Episode history latest sync entries=%s", len(episode_syncs))
        if movie_item_ids:
            movie_syncs = _latest_syncs_for_movie_ids(self.client, movie_item_ids, batch)
            logger.debug("Movie history latest sync entries=%s", len(movie_syncs))

        for job in job_list:
            latest = episode_syncs.get(job.item_id) if job.item_type == ItemType.EPISODE else movie_syncs.get(job.item_id)
            if latest is None or latest < threshold:
                yield job

    def iter_jobs_for_ids(
        self,
        *,
        series_ids: Iterable[int] | None = None,
        movie_ids: Iterable[int] | None = None,
        episode_ids: Iterable[int] | None = None,
        options: SyncOptions = SyncOptions(),
        episodes_chunk_size: int | None = None,
    ) -> Iterator[SyncJob]:
        s = unique_in_order(series_ids or ())
        m = unique_in_order(movie_ids or ())
        e = unique_in_order(episode_ids or ())
        if not s and not m and not e:
            return
        episode_limit = episodes_chunk_size or self.client.config.episodes_chunk_size

        if s:
            series_list = self.client.get_series(s)
            for series_batch in chunk_series_for_episode_requests(series_list, episode_limit):
                yield from self._iter_jobs_for_series_batch(series_batch, options)

        if m:
            for movie in self.client.get_movies(m):
                yield from jobs_for_movie(movie, options)

        if e:
            episodes = self.client.get_episodes_by_ids(e)
            unique_series_ids = unique_in_order(ep.sonarr_series_id for ep in episodes)
            titles: dict[int, str] = {}
            if unique_series_ids:
                titles = {ser.sonarr_series_id: ser.title for ser in self.client.get_series(unique_series_ids)}
            yield from jobs_for_episodes(episodes, options, series_titles=titles)

    def _iter_series_jobs(
        self,
        *,
        series_chunk_size: int | None,
        episodes_chunk_size: int | None,
        options: SyncOptions,
    ) -> Iterator[SyncJob]:
        episode_limit = episodes_chunk_size or self.client.config.episodes_chunk_size
        for series_chunk in self.client.iter_series(series_chunk_size):
            logger.debug("Series list chunk size=%s", len(series_chunk))
            for series_batch in chunk_series_for_episode_requests(series_chunk, episode_limit):
                yield from self._iter_jobs_for_series_batch(series_batch, options)

    def _iter_jobs_for_series_batch(self, series_batch: Iterable[Series], options: SyncOptions) -> Iterator[SyncJob]:
        series_list = list(series_batch)
        series_titles = {series.sonarr_series_id: series.title for series in series_list}
        series_ids = [series.sonarr_series_id for series in series_list]
        logger.info(
            "Episodes API batch series_count=%s series_ids=%s",
            len(series_ids),
            series_ids,
        )
        episodes = self.client.get_episodes_for_series_ids(series_ids)
        logger.debug("Episodes API returned episode_count=%s", len(episodes))
        yield from jobs_for_episodes(episodes, options, series_titles=series_titles)


def jobs_for_episodes(
    episodes: Iterable[Episode],
    options: SyncOptions,
    *,
    series_title: str | None = None,
    series_titles: dict[int, str] | None = None,
) -> Iterator[SyncJob]:
    for episode in episodes:
        if series_titles is not None:
            display_title = series_titles.get(episode.sonarr_series_id, episode.title)
        else:
            display_title = series_title or episode.title
        for subtitle in eligible_subtitles(episode.subtitles, options):
            yield SyncJob(
                item_type=ItemType.EPISODE,
                item_id=episode.sonarr_episode_id,
                title=display_title,
                season=episode.season,
                episode=episode.episode,
                subtitle=subtitle,
            )


def jobs_for_movie(movie: Movie, options: SyncOptions) -> Iterator[SyncJob]:
    for subtitle in eligible_subtitles(movie.subtitles, options):
        yield SyncJob(
            item_type=ItemType.MOVIE,
            item_id=movie.radarr_id,
            title=movie.title,
            subtitle=subtitle,
        )


def eligible_subtitles(subtitles: Iterable[Subtitle], options: SyncOptions) -> Iterator[Subtitle]:
    seen_paths: set[str] = set()
    for subtitle in subtitles:
        if not subtitle.path:
            continue
        if subtitle.path in seen_paths:
            continue
        if options.language and subtitle.code2 != options.language:
            continue
        seen_paths.add(subtitle.path)
        yield subtitle


def chunk_series_for_episode_requests(series: Iterable[Series], episode_limit: int) -> Iterator[list[Series]]:
    if episode_limit <= 0:
        raise ValueError("episode_limit must be positive")

    batch: list[Series] = []
    batch_episode_count = 0

    for item in series:
        episode_count = item.episode_file_count
        if episode_count <= 0:
            continue

        if batch and batch_episode_count + episode_count > episode_limit:
            yield batch
            batch = []
            batch_episode_count = 0

        batch.append(item)
        batch_episode_count += episode_count

        if batch_episode_count >= episode_limit:
            yield batch
            batch = []
            batch_episode_count = 0

    if batch:
        yield batch


def latest_syncs(events: Iterable[HistoryEvent]) -> dict[int, datetime]:
    latest: dict[int, datetime] = {}
    for event in events:
        if event.action != SYNC_ACTION or event.parsed_timestamp is None:
            continue
        current = latest.get(event.item_id)
        if current is None or event.parsed_timestamp > current:
            latest[event.item_id] = event.parsed_timestamp
    return latest


def _merge_latest_sync_maps(into: dict[int, datetime], new: dict[int, datetime]) -> None:
    for item_id, ts in new.items():
        current = into.get(item_id)
        into[item_id] = ts if current is None else max(current, ts)


def _latest_syncs_for_episode_ids(client: BazarrClient, episode_ids: Iterable[int], batch_size: int) -> dict[int, datetime]:
    unique = unique_in_order(episode_ids)
    result: dict[int, datetime] = {}
    for i in range(0, len(unique), batch_size):
        for episode_id in unique[i : i + batch_size]:
            events = client.get_episode_history(episode_id=episode_id)
            _merge_latest_sync_maps(result, latest_syncs(events))
    return result


def _latest_syncs_for_movie_ids(client: BazarrClient, movie_ids: Iterable[int], batch_size: int) -> dict[int, datetime]:
    unique = unique_in_order(movie_ids)
    result: dict[int, datetime] = {}
    for i in range(0, len(unique), batch_size):
        for radarr_id in unique[i : i + batch_size]:
            events = client.get_movie_history(radarr_id=radarr_id)
            _merge_latest_sync_maps(result, latest_syncs(events))
    return result


def _as_datetime(value: date | datetime) -> datetime:
    if isinstance(value, datetime):
        return value
    return datetime.combine(value, datetime.min.time())
