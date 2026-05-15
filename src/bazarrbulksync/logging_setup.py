"""Optional file logging for sync debugging."""

from __future__ import annotations

import logging
from pathlib import Path

SYNC_LOGGER_NAME = "bazarrbulksync"


def setup_sync_logging(log_file: Path | str | None, *, debug: bool = False) -> None:
    logger = logging.getLogger(SYNC_LOGGER_NAME)
    logger.handlers.clear()
    logger.setLevel(logging.DEBUG if debug else logging.INFO)
    logger.propagate = False

    if not log_file:
        return

    path = Path(log_file).expanduser()
    path.parent.mkdir(parents=True, exist_ok=True)
    handler = logging.FileHandler(path, encoding="utf-8")
    handler.setLevel(logging.DEBUG if debug else logging.INFO)
    if debug:
        formatter = logging.Formatter(
            "%(asctime)s %(levelname)s [%(name)s] %(filename)s:%(lineno)d %(message)s",
            datefmt="%Y-%m-%d %H:%M:%S",
        )
    else:
        formatter = logging.Formatter(
            "%(asctime)s %(levelname)s [%(name)s] %(message)s",
            datefmt="%Y-%m-%d %H:%M:%S",
        )
    handler.setFormatter(formatter)
    logger.addHandler(handler)
    logger.info("Logging to %s (debug=%s)", path.resolve(), debug)


def teardown_sync_logging() -> None:
    logger = logging.getLogger(SYNC_LOGGER_NAME)
    for handler in list(logger.handlers):
        logger.removeHandler(handler)
        try:
            handler.close()
        except Exception:
            pass
    logger.handlers.clear()
