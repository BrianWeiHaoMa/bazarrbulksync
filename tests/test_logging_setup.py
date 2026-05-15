from __future__ import annotations

import logging

from bazarrbulksync.logging_setup import SYNC_LOGGER_NAME, setup_sync_logging, teardown_sync_logging


def test_setup_teardown_writes_log(tmp_path) -> None:
    path = tmp_path / "sync.log"
    setup_sync_logging(path, debug=False)
    try:
        logging.getLogger(SYNC_LOGGER_NAME).info("test_message")
    finally:
        teardown_sync_logging()
    text = path.read_text(encoding="utf-8")
    assert "test_message" in text
    assert "INFO" in text
