import yaml

with open("config.yaml", "r") as f:
    config = yaml.safe_load(f)
    
bazarr_base_url = config["sync"]["bazarr"]["base_url"]
bazarr_api_key = config["sync"]["bazarr"]["api_key"]

sync_store_previous_sync_times = config["sync"]["store_previous_sync_times"]
sync_store_previous_sync_times_file_path = config["sync"]["store_previous_sync_times_file_path"]

base_headers = {
    "X-API-KEY": bazarr_api_key,
}

LARGE_NUMBER = 2 ** 31 - 1