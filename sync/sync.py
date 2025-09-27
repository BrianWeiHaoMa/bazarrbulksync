import logging
from datetime import datetime

import requests

from .bazarr import (
    BazarrAPI,
)


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

    def sync_movies(
        self,
        lastest_to_sync: datetime | None,
        original_format: str | None = None,
        reference: str | None = None,
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

        self.logger.info("Syncing movies...")
        total_synced = 0
        for res_movies in self.bazarr_api.get_movies(
            max_payload_size=max_payload_size,
            stop_on_request_failure=self.stop_on_request_failure,
        ):
            for movie_data in res_movies.json()["data"]:
                for subtitle_data in movie_data["subtitles"]:
                    path = subtitle_data["path"]
                    if path is not None:
                        try:
                            movie_history = self.bazarr_api.get_movie_history(
                                radarr_id=movie_data["radarrId"],
                            ).json()["data"]

                            most_recent = Syncer._helper_get_most_recent_sync_time(
                                history=movie_history,
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
                                reference=reference,
                                max_offset_seconds=max_offset_seconds,
                                no_fix_framerate=no_fix_framerate,
                                gss=gss,
                            )

                            self.logger.info(f"Finished syncing {path}")

                            total_synced += 1
                            self.logger.info(f"Movies synced so far: {total_synced}")
                        except requests.exceptions.RequestException as e:
                            if self.stop_on_request_failure:
                                raise

                            self.logger.warning(f"Failed to sync, skipping: {e}")

        self.logger.info(
            f"Finished syncing movies. Total movies synced: {total_synced}"
        )

    def sync_episodes(
        self,
        lastest_to_sync: datetime | None,
        original_format: str | None = None,
        reference: str | None = None,
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

        self.logger.info("Syncing episodes...")
        total_synced = 0
        for res_series in self.bazarr_api.get_series(
            max_payload_size=max_payload_size,
            stop_on_request_failure=self.stop_on_request_failure,
        ):
            for series_data in res_series.json()["data"]:
                try:
                    for episode_data in self.bazarr_api.get_episodes(
                        series_id_list=[series_data["sonarrSeriesId"]],
                    ).json()["data"]:
                        for subtitle_data in episode_data["subtitles"]:
                            path = subtitle_data["path"]
                            if path is not None:
                                try:
                                    episode_history = (
                                        self.bazarr_api.get_episode_history(
                                            episode_id=episode_data["sonarrEpisodeId"],
                                        ).json()["data"]
                                    )

                                    most_recent = (
                                        Syncer._helper_get_most_recent_sync_time(
                                            history=episode_history,
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
                                        reference=reference,
                                        max_offset_seconds=max_offset_seconds,
                                        no_fix_framerate=no_fix_framerate,
                                        gss=gss,
                                    )

                                    self.logger.info(f"Finished syncing {path}")

                                    total_synced += 1
                                    self.logger.info(
                                        f"Episodes synced so far: {total_synced}"
                                    )
                                except requests.exceptions.RequestException as e:
                                    if self.stop_on_request_failure:
                                        raise

                                    self.logger.warning(
                                        f"Failed to sync, skipping: {e}"
                                    )
                except requests.exceptions.RequestException as e:
                    if self.stop_on_request_failure:
                        raise

                    self.logger.warning(
                        f"Failed to get episodes for series, skipping: {e}"
                    )

        self.logger.info(
            f"Finished syncing episodes. Total episodes synced: {total_synced}"
        )
