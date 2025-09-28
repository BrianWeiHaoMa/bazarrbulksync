import os

import yaml

CONFIG_PATH = "./bazarrbulksync_cli.yaml"

config = {}
if os.path.exists(CONFIG_PATH):
    with open(CONFIG_PATH, "r") as f:
        config = yaml.safe_load(f)


def _get_nested_value(
    *args,
    default=None,
    data: dict,
):
    try:
        if len(args) == 1:
            return data.get(args[0], default)

        idx = 1
        holder = data[args[0]]
        while idx < len(args) - 1:
            holder = holder[args[idx]]

        return holder.get(args[-1], default)
    except:
        return default


BAZARR_BASE_URL = _get_nested_value("bazarr", "base_url", default=None, data=config)
BAZARR_API_KEY = _get_nested_value("bazarr", "api_key", default=None, data=config)

OUTPUT_MESSAGES_TO_SCREEN = _get_nested_value(
    "output_messages_to_screen", default=True, data=config
)
LOG_MESSAGES_TO_FILE = _get_nested_value(
    "log_messages_to_file", default=True, data=config
)
LOG_MESSAGES_FILE_PATH = _get_nested_value(
    "log_messages_file_path", default="./bazarr_bulk_sync.log", data=config
)

MAX_MOVIES_PER_REQUEST = _get_nested_value(
    "max_movies_per_request", default=25, data=config
)
MAX_SERIES_PER_REQUEST = _get_nested_value(
    "max_series_per_request", default=25, data=config
)

MAX_REQUEST_RETRIES = _get_nested_value("max_request_retries", default=3, data=config)

REQUEST_TIMEOUT = _get_nested_value("request_timeout", default=30, data=config)

STOP_ON_REQUEST_FAILURE = _get_nested_value(
    "stop_on_request_failure", default=False, data=config
)

ORIGINAL_FORMAT = _get_nested_value("original_format", default=None, data=config)
REFERENCE = _get_nested_value("reference", default=None, data=config)
MAX_OFFSET_SECONDS = _get_nested_value("max_offset_seconds", default=None, data=config)
NO_FIX_FRAMERATE = _get_nested_value("no_fix_framerate", default=None, data=config)
GSS = _get_nested_value("gss", default=None, data=config)
