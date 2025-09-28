# BazarrBulkSync
An optimized command-line tool for bulk syncing media subtitles in Bazarr.

## Use Cases
- You want to sync all of your Bazarr media subtitles in one go. BazarrBulkSync allows you to do this with a single command.
- Your Bazarr collection is MASSIVE and you want to save RAM while syncing. BazarrBulkSync supports chunking to limit the amount of resources used during the sync.
- You want to bulk sync more than once. BazarrBulkSync supports ignoring recently synced subtitles to avoid redundant syncing, saving a significant amount of time and computational resources.
- You want to record the syncing process and know which subtitles were synced at what time. BazarrBulkSync supports logging to a file and/or outputting to the screen.

## Installation and Usage
### Local Python
Make sure you have Python installed on your machine. You can install Python from [python.org](https://www.python.org/downloads/).
BazarrBulkSync has been tested for Python versions 3.10, 3.11, 3.12, and 3.13.

In the working directory of your choice, do
```
python -m venv .venv
source .venv/bin/activate # This is for Linux. On Windows use .venv\Scripts\activate instead
pip install bazarrbulksync
```

Each time you run the command-line tool, make sure that the virtual environment is activated (by using `source .venv/bin/activate` or `.venv\Scripts\activate` in the same working directory).

You can now run BazarrBulkSync:
```
bazarrbulksync --help
```
You should create a [config file](#config-file-template) in the same working directory as the one where you want to run BazarrBulkSync.

### Docker
Make sure you have Docker installed on your machine. You can install Docker from [docker.com](https://www.docker.com/). 

Pull the official BazarrBulkSync image from DockerHub:
```
docker pull wayhowma/bazarrbulksync:latest
```

After replacing `/my_absolute_path` below with the directory path that you want to mount (this is the place you would like to store BazarrBulkSync's config file and log file), you can run BazarrBulkSync:
```
docker run --rm -v /my_absolute_path:/app wayhowma/bazarrbulksync --help
```
You should create a [config file](#config-file-template) in the mounted directory `/my_absolute_path`.

### Config File Template
It is recommended to create the config file `bazarrbulksync_cli.yaml` in the same working directory as the one where you want to run BazarrBulkSync. This allows you to easily run BazarrBulkSync each time without need to respecify the parameters. The content of `bazarrbulksync_cli.yaml` should follow the template below. 
```yaml
# bazarrbulksync_cli.yaml

bazarr:
  base_url: http://192.168.1.251:6767/ # replace this with your bazarr service url
  api_key: asdai21g3isufykasgfs7iodftas9d8f # replace this with your bazarr API key

output_messages_to_screen: true # false if you don't want to see messages on the screen
log_messages_to_file: true # false if you don't want to log messages to a file
log_messages_file_path: ./bazarrbulksync.log # the file path to store the log messages

# These are values for controlling maximum API request payload sizes.
# If you are running out of ram, reducing these values may help
# especially if the number of movies/series in your bazarr is large.
max_movies_per_request: 25 
max_series_per_request: 25

# The maximum number of retries for a failed API request.
max_request_retries: 3

# The maximum amount of time to wait (in seconds) for the bazarr server 
# to respond before automatically failing a request.
request_timeout: 1600

# A request failure is when the same request fails max_request_retries times.
stop_on_request_failure: false # true if you want the program to stop on the first request failure

# These are additional optional parameters for the API when syncing subtitles.
original_format: null # Use original subtitles format from ["True", "False"]
max_offset_seconds: null # Maximum offset seconds to allow as a string ex. "300"
no_fix_framerate: null # Don't try to fix framerate from ["True", "False"]
gss: null # Use Golden-Section Search from ["True", "False"]
```

## Examples
### Local Python
Assuming we use [this](#config-file-template) config file, we can run the below command to sync all movies. Note: you need to activate the virtual environment that you set up [earlier](#local-python) before running this command.
```
bazarrbulksync --sync movies
```

```
2025-09-28 00:15:24,289 | Bazarr Bulk Sync CLI Tool Arguments: Namespace(sync='movies', bazarr_base_url='http://192.168.1.251:6767/', bazarr_api_key='asdai21g3isufykasgfs7iodftas9d8f', output_messages_to_screen=True, log_messages_to_file=True, log_messages_file_path='./bazarrbulksync.log', max_movies_per_request=25, max_series_per_request=25, max_request_retries=1, request_timeout=1600, latest_to_sync='9999-12-31', original_format=None, max_offset_seconds=None, no_fix_framerate=None, gss=None, stop_on_request_failure=False)
2025-09-28 00:15:24,291 | Syncing movies...
2025-09-28 00:15:24,364 | Syncing /data/media/media/movies/5 Centimeters per Second (2007)/[Arid] 5 Centimeters per Second (BDRip 1920x1080 Hi10 FLAC) [FD8B6FF2].ja.srt (previous sync 2025-09-28 00:08:13)
2025-09-28 00:15:28,332 | Finished syncing /data/media/media/movies/5 Centimeters per Second (2007)/[Arid] 5 Centimeters per Second (BDRip 1920x1080 Hi10 FLAC) [FD8B6FF2].ja.srt (newest sync 2025-09-28 00:15:28)
2025-09-28 00:15:28,332 | Movies synced so far: 1
2025-09-28 00:15:28,354 | Syncing /data/media/media/movies/Django Unchained (2012)/Django.Unchained.2012.1080p.BluRay.x264.YIFY.en.srt (previous sync 2025-09-28 00:08:32)
2025-09-28 00:15:47,170 | Finished syncing /data/media/media/movies/Django Unchained (2012)/Django.Unchained.2012.1080p.BluRay.x264.YIFY.en.srt (newest sync 2025-09-28 00:15:47)
2025-09-28 00:15:47,171 | Movies synced so far: 2
2025-09-28 00:15:47,213 | Syncing /data/media/media/movies/KPop Demon Hunters (2025)/KPop.Demon.Hunters.2025.1080p.WEBRip.x264.AAC5.1-[YTS.MX].en.srt (previous sync 2025-09-28 00:01:43)
2025-09-28 00:16:04,363 | Finished syncing /data/media/media/movies/KPop Demon Hunters (2025)/KPop.Demon.Hunters.2025.1080p.WEBRip.x264.AAC5.1-[YTS.MX].en.srt (newest sync 2025-09-28 00:16:04)
2025-09-28 00:16:04,364 | Movies synced so far: 3
2025-09-28 00:16:04,394 | Syncing /data/media/media/movies/Kung Fu Panda 3 (2016)/Kung.Fu.Panda.3.2016.1080p.BluRay.x264-[YTS.AG].en.srt (previous sync 2025-09-28 00:01:55)
2025-09-28 00:16:15,712 | Finished syncing /data/media/media/movies/Kung Fu Panda 3 (2016)/Kung.Fu.Panda.3.2016.1080p.BluRay.x264-[YTS.AG].en.srt (newest sync 2025-09-28 00:16:15)
2025-09-28 00:16:15,712 | Movies synced so far: 4
2025-09-28 00:16:15,764 | Syncing /data/media/media/movies/Penguins of Madagascar (2014)/Penguins.of.Madagascar.2014.1080p.BluRay.x264.YIFY.en.srt (previous sync 2025-09-28 00:02:06)
2025-09-28 00:16:26,659 | Finished syncing /data/media/media/movies/Penguins of Madagascar (2014)/Penguins.of.Madagascar.2014.1080p.BluRay.x264.YIFY.en.srt (newest sync 2025-09-28 00:16:26)
2025-09-28 00:16:26,659 | Movies synced so far: 5
2025-09-28 00:16:26,688 | Finished syncing movies. Total movies synced: 5
```

### Docker
Assuming we use [this](#config-file-template) config file, we can run the following command to sync all movies:
```
docker run --rm -v /bazarrbulksync:/app wayhowma/bazarrbulksync:latest --sync movies
```

```
2025-09-28 04:01:04,012 | Bazarr Bulk Sync CLI Tool Arguments: Namespace(sync='movies', bazarr_base_url='http://192.168.1.251:6767/', bazarr_api_key='asdai21g3isufykasgfs7iodftas9d8f', output_messages_to_screen=True, log_messages_to_file=True, log_messages_file_path='./bazarrbulksync.log', max_movies_per_request=25, max_series_per_request=25, max_request_retries=1, request_timeout=1600, latest_to_sync='9999-12-31', original_format=None, max_offset_seconds=None, no_fix_framerate=None, gss=None, stop_on_request_failure=False)
2025-09-28 04:01:04,018 | Syncing movies...
2025-09-28 04:01:04,060 | Syncing /data/media/media/movies/5 Centimeters per Second (2007)/[Arid] 5 Centimeters per Second (BDRip 1920x1080 Hi10 FLAC) [FD8B6FF2].ja.srt (previous sync 2025-09-27 23:57:21)
2025-09-28 04:01:08,200 | Finished syncing /data/media/media/movies/5 Centimeters per Second (2007)/[Arid] 5 Centimeters per Second (BDRip 1920x1080 Hi10 FLAC) [FD8B6FF2].ja.srt (newest sync 2025-09-28 00:01:08)
2025-09-28 04:01:08,201 | Movies synced so far: 1
2025-09-28 04:01:08,212 | Syncing /data/media/media/movies/Django Unchained (2012)/Django.Unchained.2012.1080p.BluRay.x264.YIFY.en.srt (previous sync 2025-09-27 23:57:39)
2025-09-28 04:01:27,074 | Finished syncing /data/media/media/movies/Django Unchained (2012)/Django.Unchained.2012.1080p.BluRay.x264.YIFY.en.srt (newest sync 2025-09-28 00:01:27)
2025-09-28 04:01:27,075 | Movies synced so far: 2
2025-09-28 04:01:27,105 | Syncing /data/media/media/movies/KPop Demon Hunters (2025)/KPop.Demon.Hunters.2025.1080p.WEBRip.x264.AAC5.1-[YTS.MX].en.srt (previous sync 2025-09-27 21:11:54)
2025-09-28 04:01:43,846 | Finished syncing /data/media/media/movies/KPop Demon Hunters (2025)/KPop.Demon.Hunters.2025.1080p.WEBRip.x264.AAC5.1-[YTS.MX].en.srt (newest sync 2025-09-28 00:01:43)
2025-09-28 04:01:43,847 | Movies synced so far: 3
2025-09-28 04:01:43,858 | Syncing /data/media/media/movies/Kung Fu Panda 3 (2016)/Kung.Fu.Panda.3.2016.1080p.BluRay.x264-[YTS.AG].en.srt (previous sync 2025-09-27 21:12:06)
2025-09-28 04:01:55,179 | Finished syncing /data/media/media/movies/Kung Fu Panda 3 (2016)/Kung.Fu.Panda.3.2016.1080p.BluRay.x264-[YTS.AG].en.srt (newest sync 2025-09-28 00:01:55)
2025-09-28 04:01:55,180 | Movies synced so far: 4
2025-09-28 04:01:55,209 | Syncing /data/media/media/movies/Penguins of Madagascar (2014)/Penguins.of.Madagascar.2014.1080p.BluRay.x264.YIFY.en.srt (previous sync 2025-09-27 21:12:16)
2025-09-28 04:02:06,189 | Finished syncing /data/media/media/movies/Penguins of Madagascar (2014)/Penguins.of.Madagascar.2014.1080p.BluRay.x264.YIFY.en.srt (newest sync 2025-09-28 00:02:06)
2025-09-28 04:02:06,189 | Movies synced so far: 5
2025-09-28 04:02:06,205 | Finished syncing movies. Total movies synced: 5
```

Using the same config file as above, we run the sync again but only for movies that were never synced after 2025-09-28 00:01:30 using
```
docker run --rm -v /bazarrbulksync:/app wayhowma/bazarrbulksync:latest --sync movies --latest-to-sync "2025-09-28 00:01:30"
```

```
2025-09-28 04:08:08,920 | Bazarr Bulk Sync CLI Tool Arguments: Namespace(sync='movies', bazarr_base_url='http://192.168.1.251:6767/', bazarr_api_key='asdai21g3isufykasgfs7iodftas9d8f', output_messages_to_screen=True, log_messages_to_file=True, log_messages_file_path='./bazarrbulksync.log', max_movies_per_request=25, max_series_per_request=25, max_request_retries=1, request_timeout=1600, latest_to_sync='2025-09-28 00:01:30', original_format=None, max_offset_seconds=None, no_fix_framerate=None, gss=None, stop_on_request_failure=False)
2025-09-28 04:08:08,928 | Syncing movies...
2025-09-28 04:08:08,971 | Syncing /data/media/media/movies/5 Centimeters per Second (2007)/[Arid] 5 Centimeters per Second (BDRip 1920x1080 Hi10 FLAC) [FD8B6FF2].ja.srt (previous sync 2025-09-28 00:01:08)
2025-09-28 04:08:13,115 | Finished syncing /data/media/media/movies/5 Centimeters per Second (2007)/[Arid] 5 Centimeters per Second (BDRip 1920x1080 Hi10 FLAC) [FD8B6FF2].ja.srt (newest sync 2025-09-28 00:08:13)
2025-09-28 04:08:13,115 | Movies synced so far: 1
2025-09-28 04:08:13,125 | Syncing /data/media/media/movies/Django Unchained (2012)/Django.Unchained.2012.1080p.BluRay.x264.YIFY.en.srt (previous sync 2025-09-28 00:01:27)
2025-09-28 04:08:32,166 | Finished syncing /data/media/media/movies/Django Unchained (2012)/Django.Unchained.2012.1080p.BluRay.x264.YIFY.en.srt (newest sync 2025-09-28 00:08:32)
2025-09-28 04:08:32,167 | Movies synced so far: 2
2025-09-28 04:08:32,197 | Skipping /data/media/media/movies/KPop Demon Hunters (2025)/KPop.Demon.Hunters.2025.1080p.WEBRip.x264.AAC5.1-[YTS.MX].en.srt (last synced at 2025-09-28 00:01:43)
2025-09-28 04:08:32,206 | Skipping /data/media/media/movies/Kung Fu Panda 3 (2016)/Kung.Fu.Panda.3.2016.1080p.BluRay.x264-[YTS.AG].en.srt (last synced at 2025-09-28 00:01:55)
2025-09-28 04:08:32,234 | Skipping /data/media/media/movies/Penguins of Madagascar (2014)/Penguins.of.Madagascar.2014.1080p.BluRay.x264.YIFY.en.srt (last synced at 2025-09-28 00:02:06)
2025-09-28 04:08:32,250 | Finished syncing movies. Total movies synced: 2
```

## Contributing
Contributions are welcome. Please open up an issue if you have ideas for improvements or submit a pull request on GitHub.

## Licensing
BazarrBulkSync is distributed under the MIT License.