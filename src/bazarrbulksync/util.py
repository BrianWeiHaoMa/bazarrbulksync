from __future__ import annotations

from collections.abc import Iterable
from typing import TypeVar

T = TypeVar("T")


def unique_in_order(items: Iterable[T]) -> list[T]:
    return list(dict.fromkeys(items))
