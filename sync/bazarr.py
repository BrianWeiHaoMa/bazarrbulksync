from urllib.parse import urljoin
from typing import Callable

import requests


class BazarrAPI:
    """A wrapper around the Bazarr API."""

    LARGE_NUMBER = 2**31 - 1
    SYNC_ACTION = 5
    HISTORY_DATETIME_FMT = "%m/%d/%y %H:%M:%S"

    def __init__(
        self,
        base_url: str,
        api_key: str,
        request_timeout: int = 1600,
        max_request_retries: int = 1,
    ):
        """Initialize the Bazarr API client.

        :param str base_url: The base URL for the Bazarr API
        :param str api_key: The API key for authenticating with the
            Bazarr API
        :param int request_timeout: The request timeout in seconds
            for each API request, defaults to 1600
        :param int max_request_retries: The maximum number of retries
            for a failed API request, defaults to 1
        """
        self.base_url = base_url
        self.api_key = api_key
        self.request_timeout = request_timeout
        self.max_request_retries = max_request_retries

        self.request_base_kwargs = {
            "headers": {
                "X-API-KEY": self.api_key,
            },
            "timeout": request_timeout,
        }

    def _make_request(
        self,
        request_func: Callable,
        kwargs: dict,
    ) -> requests.Response:
        """Make an API request. This will retry the request up
        to `max_request_retries` times if it fails.

        :param Callable request_func: The request function to use
            (e.g. requests.get, requests.post, etc.)
        :param dict kwargs: The kwargs to pass to the request function
        :raises exception: Raises requests.exceptions.RequestException
            if the request fails all retries
        :return requests.Response: The response of the API request
        """
        cnt = 0
        while cnt < self.max_request_retries:
            try:
                res = request_func(**kwargs)
                res.raise_for_status()
                return res
            except requests.exceptions.RequestException as e:
                exception = e
                cnt += 1
        raise exception

    def _helper_get__start_length_id__request(
        self,
        url: str,
        start: int | None = None,
        length: int | None = None,
        ids: list[int] | None = None,
        ids_key: str | None = None,
        max_payload_size: int | None = None,
        stop_on_request_failure: bool = False,
    ):
        """Helper function to make a API GET requests in the
        style of GET /series and GET /movies because their logic
        is basically the same. This function will chunk the request
        if `max_payload_size` is provided and greater than 0 and
        yield the responses one by one.

        :param str url: The URL to send the request to
        :param int | None start: The start parameter for the request,
            defaults to None
        :param int | None length: The length parameter for the request,
            defaults to None
        :param list[int] | None ids: The ids parameter for the request,
            defaults to None
        :param str | None ids_key: The key to use for the ids list
            in the request, defaults to None
        :param int | None max_payload_size: The maximum payload size for the
            request, defaults to None
        :param bool stop_on_request_failure: Whether to skip or stop on
            request failure when there is chunking involved, defaults to False
        :return requests.Response: The response of the API request
        """
        kwargs = self.request_base_kwargs.copy()
        kwargs["url"] = url

        if ids is not None and ids != []:
            if ids_key is None:
                raise ValueError("ids_key must be provided if ids is provided")

            kwargs["params"] = {
                ids_key: ids,
            }

            res = self._make_request(
                request_func=requests.get,
                kwargs=kwargs,
            )

            yield res
            return

        if max_payload_size is None or max_payload_size <= 0:
            kwargs["params"] = {
                "start": start,
                "length": length,
            }

            res = self._make_request(
                request_func=requests.get,
                kwargs=kwargs,
            )

            yield res
            return

        kwargs["params"] = {
            "start": BazarrAPI.LARGE_NUMBER,
            "length": 1,
        }

        # first API call to get the total number
        # of series available
        res = self._make_request(
            request_func=requests.get,
            kwargs=kwargs,
        )

        total = res.json()["total"]

        if start is None or start < 0:
            # the API starts on the first
            # series in this case
            start = 0

        if length is None or length <= 0:
            # the API returns all results
            # in this case
            start = 0
            length = total
        else:
            length = min(length, max(0, total - start))

        if not length:
            # edge case where we need to yield
            # the response with empty content
            yield res
            return

        left = length
        curr_start = start
        while left:
            payload_size = min(left, max_payload_size)

            kwargs["params"] = {
                "start": curr_start,
                "length": payload_size,
            }

            try:
                res = self._make_request(
                    request_func=requests.get,
                    kwargs=kwargs,
                )

                yield res
            except requests.exceptions.RequestException:
                # we can just skip this chunk
                # if for some reason the API calls
                # fail more than max_request_retries
                if stop_on_request_failure:
                    # in this case we don't continue
                    # yielding chunks anymore
                    raise

            curr_start += payload_size
            left -= payload_size

    def get_series(
        self,
        start: int | None = None,
        length: int | None = None,
        series_ids: list[int] | None = None,
        max_payload_size: int | None = None,
        stop_on_request_failure: bool = False,
    ):
        """GET /series from the Bazarr API in a chunked manner."""
        for res in self._helper_get__start_length_id__request(
            url=urljoin(self.base_url, "api/series"),
            start=start,
            length=length,
            ids=series_ids,
            ids_key="seriesid[]",
            max_payload_size=max_payload_size,
            stop_on_request_failure=stop_on_request_failure,
        ):
            yield res

    def get_movies(
        self,
        start: int | None = None,
        length: int | None = None,
        radarr_ids: list[int] | None = None,
        max_payload_size: int | None = None,
        stop_on_request_failure: bool = False,
    ):
        """GET /movies from the Bazarr API in a chunked manner."""
        for res in self._helper_get__start_length_id__request(
            url=urljoin(self.base_url, "api/movies"),
            start=start,
            length=length,
            ids=radarr_ids,
            ids_key="radarrid[]",
            max_payload_size=max_payload_size,
            stop_on_request_failure=stop_on_request_failure,
        ):
            yield res

    def get_episodes(
        self,
        series_id_list: list[int] | None = None,
        episode_id_list: list[int] | None = None,
    ):
        """GET /episodes from the Bazarr API."""
        url = urljoin(self.base_url, "api/episodes")

        kwargs = self.request_base_kwargs.copy()
        kwargs["url"] = url
        kwargs["params"] = {
            "seriesid[]": series_id_list,
            "episodeid[]": episode_id_list,
        }

        res = self._make_request(
            request_func=requests.get,
            kwargs=kwargs,
        )

        return res

    def get_episode_history(
        self,
        episode_id: int,
    ):
        """GET /episodes/history from the Bazarr API."""
        url = urljoin(self.base_url, "api/episodes/history")

        kwargs = self.request_base_kwargs.copy()
        kwargs["url"] = url
        kwargs["params"] = {
            "episodeid": episode_id,
        }

        res = self._make_request(
            request_func=requests.get,
            kwargs=kwargs,
        )

        return res

    def get_movie_history(
        self,
        radarr_id: int,
    ):
        """GET /movies/history from the Bazarr API."""
        url = urljoin(self.base_url, "api/movies/history")

        kwargs = self.request_base_kwargs.copy()
        kwargs["url"] = url
        kwargs["params"] = {
            "radarrid": radarr_id,
        }

        res = self._make_request(
            request_func=requests.get,
            kwargs=kwargs,
        )

        return res

    def patch_subtitle(
        self,
        action: str,
        language: str,
        path: str,
        ttype: str,
        iid: int,
        forced: str | None,
        hi: str | None,
        original_format: str | None = None,
        reference: str | None = None,
        max_offset_seconds: str | None = None,
        no_fix_framerate: str | None = None,
        gss: str | None = None,
    ):
        """PATCH /subtitles from the Bazarr API."""
        url = urljoin(self.base_url, "api/subtitles")

        kwargs = self.request_base_kwargs.copy()
        kwargs["url"] = url
        kwargs["params"] = {
            "action": action,
            "language": language,
            "path": path,
            "type": ttype,
            "id": iid,
            "forced": forced,
            "hi": hi,
            "original_format": original_format,
            "reference": reference,
            "max_offset_seconds": max_offset_seconds,
            "no_fix_framerate": no_fix_framerate,
            "gss": gss,
        }

        res = self._make_request(
            request_func=requests.patch,
            kwargs=kwargs,
        )

        return res
