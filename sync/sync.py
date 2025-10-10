import logging
from datetime import datetime

import requests

from .bazarr import (
    BazarrAPI,
)


class LoggingData:
    subtitles_synced: int = 0
    request_failures: int = 0
    chunk: int = 0


class MoviesLoggingData(LoggingData):
    movies_processed: int = 0

    def __init__(self, total_movies: int):
        super().__init__()
        self.total_movies = total_movies


class SeriesLoggingData(LoggingData):
    series_processed: int = 0
    episodes_processed: int = 0

    def __init__(self, total_series: int):
        super().__init__()
        self.total_series = total_series


class Syncer:
    """A class to sync subtitles in Bazarr."""

    DATETIME_STR_FMT = "%Y-%m-%d %H:%M:%S"

    def __init__(
        self,
        bazarr_api: BazarrAPI,
        stop_on_request_failure: bool = False,
        latest_to_sync: datetime = datetime.max,
        logger: logging.Logger | None = None,
    ):
        """Initialize the Syncer class.

        :param BazarrAPI bazarr_api: The Bazarr API client
        :param bool stop_on_request_failure: Whether to stop on request
            failure, defaults to False
        :param datetime latest_to_sync: The latest timestamp to sync (if a
            subtitle previously synced after this timestamp, it will be skipped),
            defaults to datetime.max
        :param logging.Logger | None logger: The logger to use, defaults to None
        """
        self.bazarr_api = bazarr_api
        self.stop_on_request_failure = stop_on_request_failure
        self.lastest_to_sync = latest_to_sync

        if logger is None:
            logger = logging.getLogger("__syncer_dummy_logger")
            logger.disabled = True

        self.logger = logger

    @staticmethod
    def _helper_get_most_recent_sync_time(
        history: list[dict],
        subtitle_path: str,
    ) -> datetime:
        most_recent = datetime.min
        for history_data in history:
            if (
                history_data["action"] == BazarrAPI.SYNC_ACTION
                and history_data["subtitles_path"] == subtitle_path
            ):
                most_recent = datetime.strptime(
                    history_data["parsed_timestamp"],
                    BazarrAPI.HISTORY_DATETIME_FMT,
                )
                break
        return most_recent

    def get_most_recent_movie_sync_time(
        self,
        radarr_id: int,
        subtitle_path: str,
    ) -> datetime:
        movie_history = self.bazarr_api.get_movie_history(
            radarr_id=radarr_id,
        ).json()["data"]

        return Syncer._helper_get_most_recent_sync_time(
            history=movie_history,
            subtitle_path=subtitle_path,
        )

    def get_most_recent_episode_sync_time(
        self,
        episode_id: int,
        subtitle_path: str,
    ) -> datetime:
        episode_history = self.bazarr_api.get_episode_history(
            episode_id=episode_id,
        ).json()["data"]

        return Syncer._helper_get_most_recent_sync_time(
            history=episode_history,
            subtitle_path=subtitle_path,
        )

    def sync_movies(
        self,
        lastest_to_sync: datetime | None,
        original_format: str | None = None,
        max_offset_seconds: str | None = None,
        no_fix_framerate: str | None = None,
        gss: str | None = None,
        max_payload_size: int | None = None,
    ):
        """Sync movies' subtitles in Bazarr.

        :param datetime | None lastest_to_sync: The latest timestamp to sync
            (if a subtitle was already previously synced after this timestamp,
            it will be skipped), defaults to None
        :param int | None max_payload_size: The maximum payload size for the initial
            request when searching for existing movies, defaults to None
        """
        if lastest_to_sync is None:
            lastest_to_sync = self.lastest_to_sync

        for res_movies in self.bazarr_api.get_movies(
            start=BazarrAPI.LARGE_NUMBER,
            length=1,
            stop_on_request_failure=self.stop_on_request_failure,
        ):
            if res_movies is None:
                # a request failure here means
                # something is probably wrong
                # so we will not proceed
                raise ValueError("Failed to get total movies.")

            total_movies = res_movies.json()["total"]
            break

        movies_logging_data = MoviesLoggingData(total_movies=total_movies)

        self.logger.info(
            f"Syncing movies. {movies_logging_data.total_movies} movies to process..."
        )
        for res_movies in self.bazarr_api.get_movies(
            max_payload_size=max_payload_size,
            stop_on_request_failure=self.stop_on_request_failure,
        ):
            movies_logging_data.chunk += 1

            if res_movies is None:
                movies_logging_data.request_failures += 1
                self.logger.warning(
                    f"Failed to get movies chunk {movies_logging_data.chunk}, skipping. Request failures so far: {movies_logging_data.request_failures}."
                )
                continue

            for movie_data in res_movies.json()["data"]:
                for subtitle_data in movie_data["subtitles"]:
                    path = subtitle_data["path"]
                    if path is not None:
                        try:
                            most_recent = self.get_most_recent_movie_sync_time(
                                radarr_id=movie_data["radarrId"],
                                subtitle_path=path,
                            )

                            most_recent_str = most_recent.strftime(
                                Syncer.DATETIME_STR_FMT
                            )
                            if most_recent > lastest_to_sync:
                                # synced recently
                                # so we can skip it
                                self.logger.info(
                                    f"Skipping {path} (last synced at {most_recent_str})"
                                )
                                continue

                            self.logger.info(
                                f"Movies processed: {movies_logging_data.movies_processed}/{movies_logging_data.total_movies}, Subtitles synced: {movies_logging_data.subtitles_synced}, Request failures: {movies_logging_data.request_failures}."
                            )

                            self.logger.info(
                                f"Syncing {path} (previous sync {most_recent_str})"
                            )

                            self.bazarr_api.patch_subtitle(
                                action="sync",
                                language=subtitle_data["code2"],
                                path=path,
                                ttype="movie",
                                iid=movie_data["radarrId"],
                                forced=str(subtitle_data["forced"]),
                                hi=str(subtitle_data["hi"]),
                                original_format=original_format,
                                max_offset_seconds=max_offset_seconds,
                                no_fix_framerate=no_fix_framerate,
                                gss=gss,
                            )

                            new_most_recent = self.get_most_recent_movie_sync_time(
                                radarr_id=movie_data["radarrId"],
                                subtitle_path=path,
                            )
                            new_most_recent_str = new_most_recent.strftime(
                                Syncer.DATETIME_STR_FMT
                            )

                            self.logger.info(
                                f"Finished syncing {path} (newest sync {new_most_recent_str})"
                            )

                            movies_logging_data.subtitles_synced += 1
                        except requests.exceptions.RequestException as e:
                            if self.stop_on_request_failure:
                                raise

                            self.logger.warning(f"Failed to sync, skipping: {e}")
                            movies_logging_data.request_failures += 1
                movies_logging_data.movies_processed += 1

        self.logger.info(
            f"Finished syncing movies. Movies processed: {movies_logging_data.movies_processed}/{movies_logging_data.total_movies}, Subtitles synced: {movies_logging_data.subtitles_synced}, Request failures: {movies_logging_data.request_failures}."
        )

    def sync_episodes(
        self,
        lastest_to_sync: datetime | None,
        original_format: str | None = None,
        max_offset_seconds: str | None = None,
        no_fix_framerate: str | None = None,
        gss: str | None = None,
        max_payload_size: int | None = None,
    ):
        """Sync episodes' subtitles in Bazarr.

        :param datetime | None lastest_to_sync: The latest timestamp to sync
            (if a subtitle was already previously synced after this timestamp,
            it will be skipped), defaults to None
        :param int | None max_payload_size: The maximum payload size for the initial
            request when searching for existing movies, defaults to None
        """
        if lastest_to_sync is None:
            lastest_to_sync = self.lastest_to_sync

        for res_series in self.bazarr_api.get_series(
            start=BazarrAPI.LARGE_NUMBER,
            length=1,
            stop_on_request_failure=self.stop_on_request_failure,
        ):
            if res_series is None:
                # a request failure here means
                # something is probably wrong
                # so we will not proceed
                raise ValueError("Failed to get total series.")

            total_series = res_series.json()["total"]
            break

        series_logging_data = SeriesLoggingData(total_series=total_series)

        self.logger.info(
            f"Syncing episodes. {series_logging_data.total_series} series to process..."
        )
        for res_series in self.bazarr_api.get_series(
            max_payload_size=max_payload_size,
            stop_on_request_failure=self.stop_on_request_failure,
        ):
            series_logging_data.chunk += 1

            if res_series is None:
                series_logging_data.request_failures += 1
                self.logger.warning(
                    f"Failed to get series chunk {series_logging_data.chunk}, skipping. Request failures so far: {series_logging_data.request_failures}."
                )
                continue

            for series_data in res_series.json()["data"]:
                try:
                    for episode_data in self.bazarr_api.get_episodes(
                        series_id_list=[series_data["sonarrSeriesId"]],
                    ).json()["data"]:
                        for subtitle_data in episode_data["subtitles"]:
                            path = subtitle_data["path"]
                            if path is not None:
                                try:
                                    most_recent = (
                                        self.get_most_recent_episode_sync_time(
                                            episode_id=episode_data["sonarrEpisodeId"],
                                            subtitle_path=path,
                                        )
                                    )

                                    most_recent_str = most_recent.strftime(
                                        Syncer.DATETIME_STR_FMT
                                    )
                                    if most_recent > lastest_to_sync:
                                        # synced recently
                                        # so we can skip it
                                        self.logger.info(
                                            f"Skipping {path} (last synced at {most_recent_str})"
                                        )
                                        continue

                                    self.logger.info(
                                        f"Series processed: {series_logging_data.series_processed}/{series_logging_data.total_series}, Episodes processed: {series_logging_data.episodes_processed}, Subtitles synced: {series_logging_data.subtitles_synced}, Request failures: {series_logging_data.request_failures}."
                                    )

                                    self.logger.info(
                                        f"Syncing {path} (previous sync {most_recent_str})"
                                    )

                                    self.bazarr_api.patch_subtitle(
                                        action="sync",
                                        language=subtitle_data["code2"],
                                        path=path,
                                        ttype="episode",
                                        iid=episode_data["sonarrEpisodeId"],
                                        forced=str(subtitle_data["forced"]),
                                        hi=str(subtitle_data["hi"]),
                                        original_format=original_format,
                                        max_offset_seconds=max_offset_seconds,
                                        no_fix_framerate=no_fix_framerate,
                                        gss=gss,
                                    )

                                    new_most_recent = (
                                        self.get_most_recent_episode_sync_time(
                                            episode_id=episode_data["sonarrEpisodeId"],
                                            subtitle_path=path,
                                        )
                                    )
                                    new_most_recent_str = new_most_recent.strftime(
                                        Syncer.DATETIME_STR_FMT
                                    )

                                    self.logger.info(
                                        f"Finished syncing {path} (newest sync {new_most_recent_str})"
                                    )

                                    series_logging_data.subtitles_synced += 1
                                except requests.exceptions.RequestException as e:
                                    if self.stop_on_request_failure:
                                        raise

                                    self.logger.warning(
                                        f"Failed to sync, skipping: {e}"
                                    )
                                    series_logging_data.request_failures += 1
                        series_logging_data.episodes_processed += 1
                except requests.exceptions.RequestException as e:
                    if self.stop_on_request_failure:
                        raise

                    self.logger.warning(
                        f"Failed to get episodes for series, skipping: {e}"
                    )
                    series_logging_data.request_failures += 1
                series_logging_data.series_processed += 1

        self.logger.info(
            f"Finished syncing episodes. Series processed: {series_logging_data.series_processed}/{series_logging_data.total_series}, Episodes processed: {series_logging_data.episodes_processed}, Subtitles synced: {series_logging_data.subtitles_synced}, Request failures: {series_logging_data.request_failures}."
        )
