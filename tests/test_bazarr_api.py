import itertools
from urllib.parse import urljoin
import json

import requests
import pytest

from . import (
    base_headers,
    bazarr_base_url,
    LARGE_NUMBER
)

def helper_get_response_total(
        url: str,
) -> int:
    res = requests.get(
        url=url,
        headers=base_headers,
    )
    
    holder = json.loads(res.content)
    
    return holder["total"]


def test_bazarr_has_at_least_10_series():
    url = urljoin(bazarr_base_url, "api/series")
    assert helper_get_response_total(url) >= 10, \
        "bazarr should have >= 10 series for these tests"
    
    
def test_bazarr_has_at_least_10_movies():
    url = urljoin(bazarr_base_url, "api/movies")
    assert helper_get_response_total(url) >= 10, \
        "bazarr should have >= 10 movies for these tests"
    
    
def helper_check_data_total_style_response_structure(
        url: str,
        start: int | None,
        length: int | None,
):
    res = requests.get(
        url=url,
        params={
            "start": start,
            "length": length,
        },
        headers=base_headers,
    )
    
    holder: dict = json.loads(res.content)
    
    keys = holder.keys()
    assert keys == {"data", "total"}
    
    assert type(holder["data"]) == list
    assert type(holder["total"]) == int
    
    if start is None or start < 0:
        sstart = 0
    else:
        sstart = start
        
    total = holder["total"]
    if length is None or length <= 0:
        assert len(holder["data"]) == total
    else:
        expected_size = min(length, max(0, total - sstart))
        assert len(holder["data"]) == expected_size


START_LENGTH_STYLE_POSSIBILITIES = (
    None,
    -3,
    0,
    5,
    LARGE_NUMBER,
)

start_length_style_inputs = list(
    itertools.product(
        START_LENGTH_STYLE_POSSIBILITIES, 
        START_LENGTH_STYLE_POSSIBILITIES,
    )
)


@pytest.mark.parametrize(
    "start, length",
    start_length_style_inputs,
)
def test_get_series_response_structure(start, length):
    url = urljoin(bazarr_base_url, "api/series")
    
    helper_check_data_total_style_response_structure(
        url=url,
        start=start,
        length=length,
    )
    
    
@pytest.mark.parametrize(
    "start, length",
    start_length_style_inputs,
)
def test_get_movies_response_structure(start, length):
    url = urljoin(bazarr_base_url, "api/movies")
    
    helper_check_data_total_style_response_structure(
        url=url,
        start=start,
        length=length,
    )