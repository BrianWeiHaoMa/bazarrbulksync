from urllib.parse import urljoin
import json
from typing import Any

import requests

from . import (
    bazarr_base_url,
    base_headers,
    LARGE_NUMBER,
)

def helper_get_data_total_style_data(
        url: str,
        start: int | None = None,
        length: int | None = None,
        max_payload_size: int | None = None,
):
    """A helper function to help iterate
    through the API data in chunks to save
    memory. This is a helper function for the 
    {"data": ..., "total": ...} style API calls.

    :param str url: the API call url
    :param int | None start: the item to start on, 
        defaults to None which is the same as the very first 
        item
    :param int | None length: the number of items 
        to return data for starting from start, defaults to None 
        which returns all items data starting from start 
    :param int | None max_payload_size: the maximum number of
        items data to return per API call
    :yield dict: a dictionary containing items data
    """
    if max_payload_size is None or max_payload_size <= 0:
        res = requests.get(
            url=url,
            params={
                "start": start,
                "length": length,
            },
            headers=base_headers,
        )
        
        yield json.loads(res.content)
    else:
        # one API call to get the total number
        # of series available
        res = requests.get(
            url=url,
            params={
                "start": LARGE_NUMBER,
                "length": 1,
            },
            headers=base_headers,
        )

        total = json.loads(res.content)["total"]
        
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
            # edge case where we need to
            # yield the empty content
            yield json.loads(res.content)
            return
        
        left = length
        curr_start = start
        while left:
            payload_size = min(left, max_payload_size)
            
            res = requests.get(
                url=url,
                params={
                    "start": curr_start,
                    "length": payload_size,
                },
                headers=base_headers,
            )
        
            yield json.loads(res.content)
            
            curr_start += payload_size
            left -= payload_size


def get_series(
        start: int | None = None, 
        length: int | None = None,
        max_payload_size: int | None = None,
        content: Any = None,
):  
    """Get series data.

    :param int | None start: the series to start on, 
        defaults to None which is the same as the very first 
        series
    :param int | None length: the number of series 
        to return data for starting from start, defaults to None 
        which returns all series data starting from start 
    :param int | None max_payload_size: the maximum number of
        series data to return per API call
    :param Any content: content to override the http request
        content, defaults to None
    :yield dict: a dictionary containing series
        data
    """
    if content is not None:
        yield json.loads(content)
        return
    
    url = urljoin(bazarr_base_url, "api/series")

    for yield_data in helper_get_data_total_style_data(
            url=url,
            start=start,
            length=length,
            max_payload_size=max_payload_size,
    ):
        yield yield_data


def get_movies(
        start: int | None = None, 
        length: int | None = None,
        max_payload_size: int | None = None,
        content=None,
):
    """Get movies data.

    :param int | None start: the movies to start on, 
        defaults to None which the same as the very first 
        movie
    :param int | None length: the number of movies 
        to return data for starting from start, defaults to None 
        which returns all movies data starting from start
    :param int | None max_payload_size: the maximum number of
        series data to return per API call
    :param Any content: content to override the http request
        content, defaults to None
    :yield dict: a dictionary containing movies
        data
    """
    if content is not None:
        yield json.loads(content)
        return
    
    url = urljoin(bazarr_base_url, "api/movies")

    for yield_data in helper_get_data_total_style_data(
            url=url,
            start=start,
            length=length,
            max_payload_size=max_payload_size,
    ):
        yield yield_data
        

def get_series_episodes(
        series_id_list: list[int] | None = None, 
        episode_id_list: list[int] | None = None,
        content=None,
) -> dict:
    """Gets episodes data.

    :param list[int] | None series_id_list: a list of the
        series ids, defaults to None
    :param list[int] | None episode_id_list: a list of the
        episode ids, defaults to None
    :param Any content: content to override the http request
        content, defaults to None
    :return dict: a dictionary containing episodes
        data
    """
    if content is None:
        url = urljoin(bazarr_base_url, "api/episodes")
        
        res = requests.get(
            url=url,
            params={
                "seriesid[]": series_id_list,
                "episodeid[]": episode_id_list,
            },
            headers=base_headers,
        )
        
        content = res.content
    
    return json.loads(content)


def patch_subtitles(
        action: str,
        language: str,
        path: str,
        ttype: str,
        iid: int,
) -> int:
    """Calls patch subtitles from the API.

    :param str action: from ["sync", "translate" or mods name]
    :param str language: language code2
    :param str path: subtitles file path
    :param str ttype: from ["episode", "movie"]
    :param int iid: episodeId or radarrId
    :return int: status code of the request
    """
    url = urljoin(bazarr_base_url, "api/subtitles")
    
    res = requests.patch(
        url=url,
        params={
            "action": action,
            "language": language,
            "path": path,
            "type": ttype,
            "id": iid,
        },
        headers=base_headers,
    )
    
    return res.status_code