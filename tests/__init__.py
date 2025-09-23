import yaml

with open("config.yaml", "r") as f:
    config = yaml.safe_load(f)
    
bazarr_base_url = config["tests"]["bazarr"]["base_url"]
bazarr_api_key = config["tests"]["bazarr"]["api_key"]

base_headers = {
    "X-API-KEY": bazarr_api_key,
}

LARGE_NUMBER = 2 ** 31 - 1