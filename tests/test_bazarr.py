import itertools
from urllib.parse import urljoin

import pytest
import requests

from sync.bazarr import (
    BazarrAPI,
)
from .config import (
    BAZARR_API_KEY,
    BAZARR_BASE_URL,
)

bazarr_api = BazarrAPI(
    base_url=BAZARR_BASE_URL,
    api_key=BAZARR_API_KEY,
)


def convert_list_into_count_of_element_strings(arr: list) -> dict[str, int]:
    res: dict[str, int] = {}
    for v in arr:
        s = str(v)
        if s in res:
            res[s] += 1
        else:
            res[s] = 1
    return res


def helper_check__data_total__payload_chunking(
    url,
    method,
    request_base_kwargs: dict,
    start,
    length,
    max_payload_size,
):
    kwargs = request_base_kwargs.copy()
    kwargs["url"] = url
    kwargs["params"] = {
        "start": start,
        "length": length,
    }

    expected = requests.get(**kwargs).json()

    holder = {}
    for res in method(
        start=start,
        length=length,
        max_payload_size=max_payload_size,
    ):
        data = res.json()
        for k, v in data.items():
            if k not in holder:
                holder[k] = v
            else:
                if k == "data":
                    holder[k].extend(v)

    holder_cnt = convert_list_into_count_of_element_strings(holder["data"])
    expected_cnt = convert_list_into_count_of_element_strings(expected["data"])

    assert holder_cnt == expected_cnt
    assert holder["total"] == expected["total"]


START_LENGTH_MAX_PAYLOAD_SIZE_STYLE_POSSIBILITIES = (
    None,
    -3,
    0,
    3,
    BazarrAPI.LARGE_NUMBER,
)

start_length_max_payload_size_style_inputs = list(
    itertools.product(
        START_LENGTH_MAX_PAYLOAD_SIZE_STYLE_POSSIBILITIES,
        START_LENGTH_MAX_PAYLOAD_SIZE_STYLE_POSSIBILITIES,
        START_LENGTH_MAX_PAYLOAD_SIZE_STYLE_POSSIBILITIES,
    )
)


@pytest.mark.parametrize(
    "start, length, max_payload_size",
    start_length_max_payload_size_style_inputs,
)
def test_get_series(start, length, max_payload_size):
    url = urljoin(bazarr_api.base_url, "api/series")

    helper_check__data_total__payload_chunking(
        url=url,
        method=bazarr_api.get_series,
        request_base_kwargs=bazarr_api.request_base_kwargs,
        start=start,
        length=length,
        max_payload_size=max_payload_size,
    )


@pytest.mark.parametrize(
    "start, length, max_payload_size",
    start_length_max_payload_size_style_inputs,
)
def test_get_movies(start, length, max_payload_size):
    url = urljoin(bazarr_api.base_url, "api/movies")

    helper_check__data_total__payload_chunking(
        url=url,
        method=bazarr_api.get_movies,
        request_base_kwargs=bazarr_api.request_base_kwargs,
        start=start,
        length=length,
        max_payload_size=max_payload_size,
    )
