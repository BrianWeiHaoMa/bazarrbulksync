import json
import itertools
from urllib.parse import urljoin

import pytest
import requests

from . import (
    bazarr_base_url,
    LARGE_NUMBER,
    base_headers,
)
from sync.utils import (
    get_series,
    get_movies,
)

def convert_list_into_count_of_element_strings(
        arr: list
) -> dict[str, int]:
    res = dict()
    for v in arr:
        s = str(v)
        if s in res:
            res[s] += 1
        else:
            res[s] = 1
            
    return res


def helper_check_data_total_style_functions(
        url,
        func,
        start,
        length,
        max_payload_size,
):
    res = requests.get(
        url=url,
        params={
            "start": start,
            "length": length,
        },
        headers=base_headers,
    )
    
    expected = json.loads(res.content)
    
    holder = dict()
    for yield_data in func(
            start=start, 
            length=length, 
            max_payload_size=max_payload_size,
    ):
        for k, v in yield_data.items():
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
    LARGE_NUMBER,
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
    url = urljoin(bazarr_base_url, "api/series")
    
    helper_check_data_total_style_functions(
        url=url,
        func=get_series,
        start=start,
        length=length,
        max_payload_size=max_payload_size
    )
    
    
@pytest.mark.parametrize(
    "start, length, max_payload_size",
    start_length_max_payload_size_style_inputs,
)
def test_get_movies(start, length, max_payload_size):
    url = urljoin(bazarr_base_url, "api/movies")
    
    helper_check_data_total_style_functions(
        url=url,
        func=get_movies,
        start=start,
        length=length,
        max_payload_size=max_payload_size
    )