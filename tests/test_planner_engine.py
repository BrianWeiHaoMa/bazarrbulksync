from __future__ import annotations

from datetime import date, datetime

from bazarrbulksync.config import AppConfig
from bazarrbulksync.engine import SyncEngine
from bazarrbulksync.models import (
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
from bazarrbulksync.planner import (
    SyncPlanner,
    chunk_series_for_episode_requests,
    eligible_subtitles,
    jobs_for_episodes,
    jobs_for_movie,
    latest_syncs,
)


def test_eligible_subtitles_skips_missing_paths_and_filters_language() -> None:
    subtitles = [
        Subtitle("English", "en", "eng", None),
        Subtitle("Japanese", "ja", "jpn", "/media/show.ja.srt"),
        Subtitle("Japanese duplicate", "ja", "jpn", "/media/show.ja.srt"),
        Subtitle("Portuguese", "pt", "por", "/media/show.pt.srt"),
    ]

    result = list(eligible_subtitles(subtitles, SyncOptions(language="ja")))

    assert [item.path for item in result] == ["/media/show.ja.srt"]


def test_jobs_for_series_and_movies_use_media_ids() -> None:
    episode = Episode(
        sonarr_episode_id=10,
        sonarr_series_id=20,
        title="Pilot",
        season=1,
        episode=1,
        subtitles=(Subtitle("English", "en", "eng", "/media/pilot.en.srt"),),
    )
    movie = Movie(
        radarr_id=30,
        title="Movie",
        subtitles=(Subtitle("English", "en", "eng", "/media/movie.en.srt"),),
    )

    episode_jobs = list(jobs_for_episodes([episode], SyncOptions(), series_title="Series"))
    movie_jobs = list(jobs_for_movie(movie, SyncOptions()))

    assert episode_jobs[0].item_type == ItemType.EPISODE
    assert episode_jobs[0].item_id == 10
    assert episode_jobs[0].display_name == "Series S01E01"
    assert movie_jobs[0].item_type == ItemType.MOVIE
    assert movie_jobs[0].item_id == 30


def test_latest_syncs_only_tracks_action_five() -> None:
    events = [
        HistoryEvent(ItemType.MOVIE, 3, 2, datetime(2025, 9, 1, 12, 0, 0)),
        HistoryEvent(ItemType.MOVIE, 3, 5, datetime(2025, 9, 2, 12, 0, 0)),
        HistoryEvent(ItemType.MOVIE, 3, 5, datetime(2025, 9, 3, 12, 0, 0)),
    ]

    assert latest_syncs(events)[3] == datetime(2025, 9, 3, 12, 0, 0)


def test_engine_dry_run_reports_without_calling_client() -> None:
    class NoopClient:
        def sync_subtitle(self, **kwargs: object) -> None:
            raise AssertionError("dry-run should not call Bazarr")

    movie = Movie(
        radarr_id=30,
        title="Movie",
        subtitles=(Subtitle("English", "en", "eng", "/media/movie.en.srt"),),
    )
    jobs = list(jobs_for_movie(movie, SyncOptions()))

    summary = SyncEngine(NoopClient()).run(jobs, options=SyncOptions(), dry_run=True)

    assert summary.dry_run == 1
    assert summary.failed == 0


def test_chunk_series_for_episode_requests_respects_episode_limit() -> None:
    series = [
        Series(sonarr_series_id=1, title="One", episode_file_count=12),
        Series(sonarr_series_id=2, title="Two", episode_file_count=7),
        Series(sonarr_series_id=3, title="Three", episode_file_count=20),
        Series(sonarr_series_id=4, title="Empty", episode_file_count=0),
    ]

    batches = list(chunk_series_for_episode_requests(series, episode_limit=18))

    assert [[item.sonarr_series_id for item in batch] for batch in batches] == [[1], [2], [3]]


def test_planner_fetches_episodes_for_multiple_series_per_batch() -> None:
    class FakeClient:
        def __init__(self) -> None:
            self.episode_calls: list[list[int]] = []
            self.config = type("Config", (), {"episodes_chunk_size": 20})()

        def iter_series(self, chunk_size: int | None = None):
            assert chunk_size == 10
            yield [
                Series(sonarr_series_id=1, title="One", episode_file_count=12),
                Series(sonarr_series_id=2, title="Two", episode_file_count=8),
                Series(sonarr_series_id=3, title="Three", episode_file_count=5),
            ]

        def get_episodes_for_series_ids(self, series_ids):
            ids = list(series_ids)
            self.episode_calls.append(ids)
            return [
                Episode(
                    sonarr_episode_id=series_id * 100,
                    sonarr_series_id=series_id,
                    title=f"Episode {series_id}",
                    season=1,
                    episode=1,
                    subtitles=(Subtitle("English", "en", "eng", f"/media/{series_id}.en.srt"),),
                )
                for series_id in ids
            ]

    client = FakeClient()
    planner = SyncPlanner(client)  # type: ignore[arg-type]

    jobs = list(
        planner.iter_all_jobs(
            media_type=MediaType.SERIES,
            series_chunk_size=10,
            episodes_chunk_size=20,
            options=SyncOptions(),
        )
    )

    assert client.episode_calls == [[1, 2], [3]]
    assert [job.title for job in jobs] == ["One", "Two", "Three"]


def test_collect_job_item_ids_matches_iter_all_jobs() -> None:
    class FakeClient:
        def __init__(self) -> None:
            self.episode_calls: list[list[int]] = []
            self.config = type("Config", (), {"episodes_chunk_size": 20})()

        def iter_series(self, chunk_size: int | None = None):
            yield [
                Series(sonarr_series_id=1, title="One", episode_file_count=12),
                Series(sonarr_series_id=2, title="Two", episode_file_count=8),
            ]

        def get_episodes_for_series_ids(self, series_ids):
            ids = list(series_ids)
            self.episode_calls.append(ids)
            return [
                Episode(
                    sonarr_episode_id=series_id * 100,
                    sonarr_series_id=series_id,
                    title=f"Episode {series_id}",
                    season=1,
                    episode=1,
                    subtitles=(Subtitle("English", "en", "eng", f"/media/{series_id}.en.srt"),),
                )
                for series_id in ids
            ]

    client = FakeClient()
    planner = SyncPlanner(client)  # type: ignore[arg-type]

    episode_ids, movie_ids = planner.collect_job_item_ids(
        media_type=MediaType.SERIES,
        series_chunk_size=10,
        episodes_chunk_size=20,
        options=SyncOptions(),
    )

    assert episode_ids == [100, 200]
    assert movie_ids == []


def test_iter_before_jobs_queries_movie_history_per_distinct_id() -> None:
    class FakeClient:
        def __init__(self) -> None:
            self.movie_history_calls: list[int] = []

        config = AppConfig(base_url="http://bazarr", api_key="k", before_history_batch_size=50)

        def iter_series(self, chunk_size: int | None = None):
            yield from ()

        def iter_movies(self, chunk_size: int | None = None):
            yield [
                Movie(
                    radarr_id=7,
                    title="Alpha",
                    subtitles=(Subtitle("English", "en", "eng", "/m/a.en.srt"),),
                ),
                Movie(
                    radarr_id=8,
                    title="Beta",
                    subtitles=(Subtitle("English", "en", "eng", "/m/b.en.srt"),),
                ),
            ]

        def get_movie_history(self, radarr_id: int | None = None, length: int = -1):
            assert radarr_id is not None
            self.movie_history_calls.append(radarr_id)
            return []

    client = FakeClient()
    planner = SyncPlanner(client)  # type: ignore[arg-type]
    threshold = date(2030, 1, 1)
    episode_ids, movie_ids = planner.collect_job_item_ids(
        media_type=MediaType.MOVIES,
        movies_chunk_size=100,
        options=SyncOptions(),
    )
    jobs = list(
        planner.iter_before_jobs(
            threshold,
            episode_ids,
            movie_ids,
            media_type=MediaType.MOVIES,
            movies_chunk_size=100,
            options=SyncOptions(),
        )
    )

    assert sorted(client.movie_history_calls) == [7, 8]
    assert len(jobs) == 2


def test_iter_before_jobs_dedupes_episode_history_when_multiple_subtitle_jobs() -> None:
    class FakeClient:
        def __init__(self) -> None:
            self.episode_history_calls: list[int] = []

        config = AppConfig(base_url="http://bazarr", api_key="k", before_history_batch_size=50)

        def iter_series(self, chunk_size: int | None = None):
            yield [Series(sonarr_series_id=1, title="One", episode_file_count=5)]

        def iter_movies(self, chunk_size: int | None = None):
            yield from ()

        def get_episodes_for_series_ids(self, series_ids):
            return [
                Episode(
                    sonarr_episode_id=99,
                    sonarr_series_id=1,
                    title="Pilot",
                    season=1,
                    episode=1,
                    subtitles=(
                        Subtitle("English", "en", "eng", "/media/p.en.srt"),
                        Subtitle("Japanese", "ja", "jpn", "/media/p.ja.srt"),
                    ),
                )
            ]

        def get_episode_history(self, episode_id: int | None = None, length: int = -1):
            assert episode_id is not None
            self.episode_history_calls.append(episode_id)
            return []

        def get_movie_history(self, radarr_id: int | None = None, length: int = -1):
            raise AssertionError("not used for series-only before")

    client = FakeClient()
    planner = SyncPlanner(client)  # type: ignore[arg-type]
    threshold = date(2030, 1, 1)
    episode_ids, movie_ids = planner.collect_job_item_ids(
        media_type=MediaType.SERIES,
        series_chunk_size=100,
        episodes_chunk_size=100,
        options=SyncOptions(),
    )
    assert episode_ids == [99, 99]
    jobs = list(
        planner.iter_before_jobs(
            threshold,
            episode_ids,
            movie_ids,
            media_type=MediaType.SERIES,
            series_chunk_size=100,
            episodes_chunk_size=100,
            options=SyncOptions(),
        )
    )

    assert client.episode_history_calls == [99]
    assert len(jobs) == 2


def test_iter_before_jobs_respects_history_lookup_batch_size_override() -> None:
    class FakeClient:
        def __init__(self) -> None:
            self.movie_history_calls: list[int] = []

        config = AppConfig(base_url="http://bazarr", api_key="k", before_history_batch_size=99)

        def iter_series(self, chunk_size: int | None = None):
            yield from ()

        def iter_movies(self, chunk_size: int | None = None):
            yield [
                Movie(
                    radarr_id=i,
                    title=f"M{i}",
                    subtitles=(Subtitle("English", "en", "eng", f"/m/{i}.en.srt"),),
                )
                for i in (1, 2, 3)
            ]

        def get_episode_history(self, episode_id: int | None = None, length: int = -1):
            raise AssertionError("not used")

        def get_movie_history(self, radarr_id: int | None = None, length: int = -1):
            assert radarr_id is not None
            self.movie_history_calls.append(radarr_id)
            return []

    client = FakeClient()
    planner = SyncPlanner(client)  # type: ignore[arg-type]
    threshold = date(2030, 1, 1)
    episode_ids, movie_ids = planner.collect_job_item_ids(
        media_type=MediaType.MOVIES,
        movies_chunk_size=100,
        options=SyncOptions(),
    )
    list(
        planner.iter_before_jobs(
            threshold,
            episode_ids,
            movie_ids,
            media_type=MediaType.MOVIES,
            movies_chunk_size=100,
            options=SyncOptions(),
            history_lookup_batch_size=2,
        )
    )

    assert client.movie_history_calls == [1, 2, 3]


def test_iter_jobs_for_ids_series_movies_episodes() -> None:
    class FakeClient:
        def __init__(self) -> None:
            self.get_series_calls: list[list[int]] = []
            self.get_movies_calls: list[list[int]] = []
            self.episode_id_calls: list[list[int]] = []

        config = type("Config", (), {"episodes_chunk_size": 50})()

        def get_series(self, ids):
            ids_list = list(ids)
            self.get_series_calls.append(ids_list)
            return [Series(sonarr_series_id=i, title=f"S{i}", episode_file_count=1) for i in ids_list]

        def get_movies(self, ids):
            ids_list = list(ids)
            self.get_movies_calls.append(ids_list)
            return [
                Movie(
                    radarr_id=i,
                    title=f"M{i}",
                    subtitles=(Subtitle("English", "en", "eng", f"/m/{i}.en.srt"),),
                )
                for i in ids_list
            ]

        def get_episodes_by_ids(self, episode_ids):
            ids = list(episode_ids)
            self.episode_id_calls.append(ids)
            return [
                Episode(
                    sonarr_episode_id=900,
                    sonarr_series_id=1,
                    title="Pilot",
                    season=1,
                    episode=1,
                    subtitles=(Subtitle("English", "en", "eng", "/e.en.srt"),),
                )
            ]

        def get_episodes_for_series_ids(self, series_ids):
            ids = list(series_ids)
            return [
                Episode(
                    sonarr_episode_id=ids[0] * 10,
                    sonarr_series_id=ids[0],
                    title="E",
                    season=1,
                    episode=1,
                    subtitles=(Subtitle("English", "en", "eng", f"/s/{ids[0]}.en.srt"),),
                )
            ]

    client = FakeClient()
    planner = SyncPlanner(client)  # type: ignore[arg-type]

    jobs = list(
        planner.iter_jobs_for_ids(
            series_ids=[1],
            movie_ids=[7],
            episode_ids=[900],
            options=SyncOptions(),
            episodes_chunk_size=50,
        )
    )

    assert client.get_series_calls == [[1], [1]]
    assert client.get_movies_calls == [[7]]
    assert client.episode_id_calls == [[900]]
    assert len(jobs) == 3
    assert {j.item_type for j in jobs} == {ItemType.EPISODE, ItemType.MOVIE}
    movie_job = next(j for j in jobs if j.item_type == ItemType.MOVIE)
    assert movie_job.item_id == 7


def test_iter_before_jobs_from_jobs_filters_by_episode_history() -> None:
    sub = Subtitle("English", "en", "eng", "/x.en.srt")
    job_stale = SyncJob(ItemType.EPISODE, 1, "Show", sub, season=1, episode=1)
    job_fresh = SyncJob(ItemType.EPISODE, 2, "Show", sub, season=1, episode=2)

    class FakeClient:
        def __init__(self) -> None:
            self.episode_history_calls: list[int] = []

        config = AppConfig(base_url="http://bazarr", api_key="k", before_history_batch_size=50)

        def get_episode_history(self, episode_id: int | None = None, length: int = -1):
            assert episode_id is not None
            self.episode_history_calls.append(episode_id)
            if episode_id == 1:
                return [HistoryEvent(ItemType.EPISODE, 1, 5, datetime(2020, 1, 1, 0, 0, 0))]
            return [HistoryEvent(ItemType.EPISODE, 2, 5, datetime(2035, 1, 1, 0, 0, 0))]

        def get_movie_history(self, radarr_id: int | None = None, length: int = -1):
            raise AssertionError("not used")

    client = FakeClient()
    planner = SyncPlanner(client)  # type: ignore[arg-type]
    threshold = date(2025, 6, 1)
    jobs = list(planner.iter_before_jobs_from_jobs(threshold, [job_stale, job_fresh]))

    assert [j.item_id for j in jobs] == [1]
    assert sorted(client.episode_history_calls) == [1, 2]

