from __future__ import annotations

import logging
import time
from collections.abc import Iterable, Iterator
from typing import Any

import httpx

from .config import AppConfig
from .models import Episode, HistoryEvent, ItemType, Movie, Series, Subtitle, SyncOptions
from .util import unique_in_order

logger = logging.getLogger(__name__)


class BazarrApiError(RuntimeError):
    def __init__(self, message: str, *, status_code: int | None = None) -> None:
        super().__init__(message)
        self.status_code = status_code


class BazarrClient:
    def __init__(self, config: AppConfig, transport: httpx.BaseTransport | None = None) -> None:
        self.config = config
        headers = {"X-API-KEY": config.api_key}
        self._client = httpx.Client(
            base_url=config.api_url,
            headers=headers,
            timeout=config.timeout_seconds,
            transport=transport,
        )

    def close(self) -> None:
        self._client.close()

    def __enter__(self) -> BazarrClient:
        return self

    def __exit__(self, *args: object) -> None:
        self.close()

    def iter_series(self, chunk_size: int | None = None) -> Iterator[list[Series]]:
        for chunk in self._iter_paginated("series", chunk_size or self.config.series_chunk_size):
            yield [Series.from_api(item) for item in chunk]

    def iter_movies(self, chunk_size: int | None = None) -> Iterator[list[Movie]]:
        for chunk in self._iter_paginated("movies", chunk_size or self.config.movies_chunk_size):
            yield [Movie.from_api(item) for item in chunk]

    def get_series(self, ids: Iterable[int]) -> list[Series]:
        unique = unique_in_order(ids)
        if not unique:
            return []
        params = [("seriesid[]", str(item_id)) for item_id in unique]
        return [Series.from_api(item) for item in self._get_data("series", params=params)]

    def get_movies(self, ids: Iterable[int]) -> list[Movie]:
        unique = unique_in_order(ids)
        if not unique:
            return []
        params = [("radarrid[]", str(item_id)) for item_id in unique]
        return [Movie.from_api(item) for item in self._get_data("movies", params=params)]

    def get_episodes_by_ids(self, ids: Iterable[int]) -> list[Episode]:
        unique = unique_in_order(ids)
        if not unique:
            return []
        params = [("episodeid[]", str(item_id)) for item_id in unique]
        return [Episode.from_api(item) for item in self._get_data("episodes", params=params)]

    def get_episodes_for_series_ids(self, series_ids: Iterable[int]) -> list[Episode]:
        unique = unique_in_order(series_ids)
        if not unique:
            return []
        params = [("seriesid[]", str(item_id)) for item_id in unique]
        return [Episode.from_api(item) for item in self._get_data("episodes", params=params)]

    def get_episode_history(self, episode_id: int | None = None, length: int = -1) -> list[HistoryEvent]:
        params: list[tuple[str, str]] = [("length", str(length))]
        if episode_id is not None:
            params.append(("episodeid", str(episode_id)))
        return [HistoryEvent.episode_from_api(item) for item in self._get_data("episodes/history", params=params)]

    def get_movie_history(self, radarr_id: int | None = None, length: int = -1) -> list[HistoryEvent]:
        params: list[tuple[str, str]] = [("length", str(length))]
        if radarr_id is not None:
            params.append(("radarrid", str(radarr_id)))
        return [HistoryEvent.movie_from_api(item) for item in self._get_data("movies/history", params=params)]

    def sync_subtitle(
        self,
        *,
        item_type: ItemType,
        item_id: int,
        subtitle: Subtitle,
        options: SyncOptions,
    ) -> None:
        if not subtitle.path:
            raise ValueError("Cannot sync a subtitle without a path")

        payload: dict[str, str | int] = {
            "action": "sync",
            "language": options.language or subtitle.code2,
            "path": subtitle.path,
            "type": item_type.value,
            "id": item_id,
            "forced": _bazarr_bool(options.forced_for(subtitle)),
            "hi": _bazarr_bool(options.hi_for(subtitle)),
            "max_offset_seconds": str(options.max_offset_seconds),
            "no_fix_framerate": _bazarr_bool(options.no_fix_framerate),
            "gss": _bazarr_bool(options.gss),
        }
        if options.reference:
            payload["reference"] = options.reference

        logger.debug(
            "sync_subtitle type=%s id=%s language=%s path=%s",
            item_type.value,
            item_id,
            payload.get("language"),
            subtitle.path,
        )
        self._request("PATCH", "subtitles", data=payload, expected_status={204})
        logger.info("PATCH subtitles ok type=%s id=%s path=%s", item_type.value, item_id, subtitle.path)

    def _iter_paginated(self, path: str, chunk_size: int) -> Iterator[list[dict[str, Any]]]:
        start = 0
        total: int | None = None
        while total is None or start < total:
            body = self._request_json(path, params={"start": start, "length": chunk_size})
            data = body.get("data") or []
            if total is None:
                total = int(body.get("total", len(data)))
            if not data:
                break
            yield data
            start += len(data)

    def _get_data(self, path: str, params: Any = None) -> list[dict[str, Any]]:
        body = self._request_json(path, params=params)
        data = body.get("data") or []
        if not isinstance(data, list):
            raise BazarrApiError(f"Unexpected response from {path}: data is not a list")
        return data

    def _request_json(self, path: str, **kwargs: Any) -> dict[str, Any]:
        response = self._request("GET", path, expected_status={200}, **kwargs)
        try:
            body = response.json()
        except ValueError as exc:
            raise BazarrApiError(f"Invalid JSON response from {path}") from exc
        if not isinstance(body, dict):
            raise BazarrApiError(f"Unexpected response from {path}: body is not an object")
        return body

    def _request(
        self,
        method: str,
        path: str,
        *,
        expected_status: set[int],
        **kwargs: Any,
    ) -> httpx.Response:
        last_error: Exception | None = None
        for attempt in range(self.config.retries + 1):
            try:
                logger.debug("HTTP %s %s attempt=%s", method, path, attempt + 1)
                response = self._client.request(method, path, **kwargs)
                if response.status_code in expected_status:
                    logger.debug("HTTP %s %s -> %s", method, path, response.status_code)
                    return response
                err = BazarrApiError(
                    f"Bazarr returned HTTP {response.status_code} for {method} {path}: {response.text}",
                    status_code=response.status_code,
                )
                logger.warning(
                    "HTTP %s %s -> %s preview=%s",
                    method,
                    path,
                    response.status_code,
                    (response.text or "")[:400],
                )
                raise err
            except (httpx.HTTPError, BazarrApiError) as exc:
                last_error = exc
                if attempt >= self.config.retries:
                    break
                logger.warning("HTTP %s %s retry after error: %s", method, path, exc)
                time.sleep(min(0.25 * (attempt + 1), 1.0))

        logger.error("HTTP %s %s failed after retries: %s", method, path, last_error)
        if isinstance(last_error, BazarrApiError):
            raise last_error
        raise BazarrApiError(f"Request failed for {method} {path}: {last_error}") from last_error


def _bazarr_bool(value: bool) -> str:
    return "True" if value else "False"

