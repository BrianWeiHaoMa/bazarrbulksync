import os
import sys

import yaml

CONFIG_PATH = "./bazarrbulksync_configs/tests_config.yaml"

if not os.path.exists(CONFIG_PATH):
    print(f"please create the config file at {CONFIG_PATH}", file=sys.stderr)
    sys.exit(1)

with open(CONFIG_PATH, "r") as f:
    config = yaml.safe_load(f)

BAZARR_BASE_URL = config["bazarr"]["base_url"]
BAZARR_API_KEY = config["bazarr"]["api_key"]
