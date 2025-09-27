import logging
from datetime import datetime
from argparse import ArgumentParser
import traceback

import requests

from sync.sync import (
    Syncer,
)
from sync.bazarr import (
    BazarrAPI,
)
from .config import (
    BAZARR_BASE_URL,
    BAZARR_API_KEY,
    OUTPUT_MESSAGES_TO_SCREEN,
    LOG_MESSAGES_TO_FILE,
    LOG_MESSAGES_FILE_PATH,
    MAX_MOVIES_PER_REQUEST,
    MAX_SERIES_PER_REQUEST,
    REQUEST_TIMEOUT,
    STOP_ON_REQUEST_FAILURE,
    MAX_REQUEST_RETRIES,
)

def main():
    parser = ArgumentParser(
        prog="bazarrbulksync",
        description="A CLI tool to bulk sync subtitles in Bazarr. "
        "You can find out more about the project at: https://github.com/BrianWeiHaoMa/BazarrBulkSync",
    )

    parser.add_argument(
        "--sync",
        type=str,
        choices=["movies", "episodes", "both"],
        required=True,
        help="What to run the sync for",
    )
    parser.add_argument(
        "--bazarr-base-url",
        type=str,
        default=BAZARR_BASE_URL,
        help="The bazarr base url (ex: http://192.168.1.251:6767/)",
    )
    parser.add_argument(
        "--bazarr-api-key",
        type=str,
        default=BAZARR_API_KEY,
        help="The bazarr API key (ex: asdai21g3isufykasgfs7iodftas9d8f)",
    )
    parser.add_argument(
        "--output-messages-to-screen",
        type=lambda x: (str(x).lower() == "true"),
        default=OUTPUT_MESSAGES_TO_SCREEN,
        help="True if you want to enable outputting log messages to screen",
    )
    parser.add_argument(
        "--log-messages-to-file",
        type=lambda x: (str(x).lower() == "true"),
        default=LOG_MESSAGES_TO_FILE,
        help="True if you want to save log messages to a file",
    )
    parser.add_argument(
        "--log-messages-file-path",
        type=str,
        default=LOG_MESSAGES_FILE_PATH,
        help="The file path to save the log messages to (ex: ./bazarr_bulk_sync.log)",
    )
    parser.add_argument(
        "--max-movies-per-request",
        type=int,
        default=MAX_MOVIES_PER_REQUEST,
        help="The maximum number of movies to query per request",
    )
    parser.add_argument(
        "--max-series-per-request",
        type=int,
        default=MAX_SERIES_PER_REQUEST,
        help="The maximum number of series to query per request",
    )
    parser.add_argument(
        "--max-request-retries",
        type=int,
        default=MAX_REQUEST_RETRIES,
        help="The maximum number of retries for a failed API request",
    )
    parser.add_argument(
        "--request-timeout",
        type=int,
        default=REQUEST_TIMEOUT,
        help="The request timeout in seconds for each API request",
    )
    parser.add_argument(
        "--latest-to-sync",
        type=str,
        default=datetime.max.strftime("%Y-%m-%d"),
        help="The most latest sync time to sync from (up until the present time) \
                '%%Y-%%m-%%d %%H:%%M:%%S' or '%%Y-%%m-%%d' format. Items previously already \
                    synced more recently than this time will be skipped.",
    )
    parser.add_argument(
        "--original-format",
        type=str,
        choices=["True", "False"],
        default=None,
        help="The original format option in the API when syncing subtitles",
    )
    parser.add_argument(
        "--reference",
        type=str,
        default=None,
        help="The reference option in the API when syncing subtitles",
    )
    parser.add_argument(
        "--max-offset-seconds",
        type=str,
        default=None,
        help="The max offset seconds option in the API when syncing subtitles",
    )
    parser.add_argument(
        "--no-fix-framerate",
        type=str,
        default=None,
        help="The no fix framerate option in the API when syncing subtitles",
    )
    parser.add_argument(
        "--gss",
        type=str,
        default=None,
        help="The gss option in the API when syncing subtitles",
    )
    parser.add_argument(
        "--stop-on-request-failure",
        type=lambda x: (str(x).lower() == "true"),
        default=STOP_ON_REQUEST_FAILURE,
        help="True if you want to stop the sync when a request fails",
    )

    parsed_args = parser.parse_args()

    if parsed_args.bazarr_base_url is None:
        raise ValueError("Bazarr URL must be set via config file or CLI argument")

    if parsed_args.bazarr_api_key is None:
        raise ValueError("Bazarr API key must be set via config file or CLI argument")

    sync = parsed_args.sync

    LOGGER_NAME = "Bazarr Bulk Sync CLI Tool"
    logging_fmt = "%(asctime)s | %(message)s"
    if parsed_args.output_messages_to_screen:
        logging.basicConfig(
            level=logging.INFO,
            format=logging_fmt,
        )
        logger = logging.getLogger(LOGGER_NAME)

        if parsed_args.log_messages_to_file:
            file_handler = logging.FileHandler(
                parsed_args.log_messages_file_path, mode="a"
            )
            file_handler.setLevel(logging.INFO)
            file_formatter = logging.Formatter(logging_fmt)
            file_handler.setFormatter(file_formatter)
            logger.addHandler(file_handler)
    else:
        disable_logging = False
        if parsed_args.log_messages_to_file:
            logging.basicConfig(
                level=logging.INFO,
                format=logging_fmt,
                filename=parsed_args.log_messages_file_path,
                filemode="a",
            )
        else:
            disable_logging = True

        logger = logging.getLogger(LOGGER_NAME)
        if disable_logging:
            logger.disabled = True

    logger.info(f"Bazarr Bulk Sync CLI Tool Arguments: {parsed_args}")

    try:
        latest_to_sync = datetime.strptime(
            parsed_args.latest_to_sync,
            "%Y-%m-%d",
        )
    except Exception:
        try:
            latest_to_sync = datetime.strptime(
                parsed_args.latest_to_sync,
                "%Y-%m-%d %H:%M:%S",
            )
        except Exception:
            raise ValueError(
                f"latest_to_sync ({parsed_args.latest_to_sync}) must be in"
                "'%%Y-%%m-%%d %%H:%%M:%%S' or '%%Y-%%m-%%d' format"
            )

    bazarr_api = BazarrAPI(
        base_url=parsed_args.bazarr_base_url,
        api_key=parsed_args.bazarr_api_key,
        request_timeout=parsed_args.request_timeout,
        max_request_retries=parsed_args.max_request_retries,
    )

    syncer = Syncer(
        bazarr_api=bazarr_api,
        latest_to_sync=latest_to_sync,
        logger=logger,
        stop_on_request_failure=parsed_args.stop_on_request_failure,
    )

    try:
        if sync == "movies":
            syncer.sync_movies(
                lastest_to_sync=latest_to_sync,
                original_format=parsed_args.original_format,
                reference=parsed_args.reference,
                max_offset_seconds=parsed_args.max_offset_seconds,
                no_fix_framerate=parsed_args.no_fix_framerate,
                gss=parsed_args.gss,
                max_payload_size=parsed_args.max_movies_per_request,
            )
        elif sync == "episodes":
            syncer.sync_episodes(
                lastest_to_sync=latest_to_sync,
                original_format=parsed_args.original_format,
                reference=parsed_args.reference,
                max_offset_seconds=parsed_args.max_offset_seconds,
                no_fix_framerate=parsed_args.no_fix_framerate,
                gss=parsed_args.gss,
                max_payload_size=parsed_args.max_movies_per_request,
            )
        elif sync == "both":
            syncer.sync_movies(
                lastest_to_sync=latest_to_sync,
                original_format=parsed_args.original_format,
                reference=parsed_args.reference,
                max_offset_seconds=parsed_args.max_offset_seconds,
                no_fix_framerate=parsed_args.no_fix_framerate,
                gss=parsed_args.gss,
                max_payload_size=parsed_args.max_movies_per_request,
            )
            syncer.sync_episodes(
                lastest_to_sync=latest_to_sync,
                original_format=parsed_args.original_format,
                reference=parsed_args.reference,
                max_offset_seconds=parsed_args.max_offset_seconds,
                no_fix_framerate=parsed_args.no_fix_framerate,
                gss=parsed_args.gss,
                max_payload_size=parsed_args.max_series_per_request,
            )
        else:
            assert False, "this should never be reached"
    except Exception as e:
        if isinstance(e, requests.RequestException):
            logger.warning(f"A request failure occurred during the sync: {e}")
        else:
            logger.critical(
                f"A critical error occurred during the sync:\n{traceback.format_exc()}"
            )
            raise e


if __name__ == "__main__":
    main()
