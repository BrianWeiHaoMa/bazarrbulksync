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
2025-10-10 17:43:57,772 | Bazarr Bulk Sync CLI Tool Arguments: Namespace(sync='movies', bazarr_base_url='http://192.168.1.251:6767/', bazarr_api_key='0f8b21b007f3f36da4cc94859060facd', output_messages_to_screen=True, log_messages_to_file=True, log_messages_file_path='./bazarrbulksync.log', max_movies_per_request=25, max_series_per_request=25, max_request_retries=3, request_timeout=1600, latest_to_sync='9999-12-31', original_format=None, max_offset_seconds=None, no_fix_framerate=None, gss=None, stop_on_request_failure=False)
2025-10-10 17:43:57,781 | Syncing movies. 92 movies to process...
2025-10-10 17:43:57,814 | Movies processed: 0/92, Subtitles synced: 0, Request failures: 0.
2025-10-10 17:43:57,814 | Syncing /data/media/media/movies/5 Centimeters per Second (2007)/[Arid] 5 Centimeters per Second (BDRip 1920x1080 Hi10 FLAC) [FD8B6FF2].ja.srt (previous sync 2025-10-10 00:23:31)
2025-10-10 17:44:07,993 | Finished syncing /data/media/media/movies/5 Centimeters per Second (2007)/[Arid] 5 Centimeters per Second (BDRip 1920x1080 Hi10 FLAC) [FD8B6FF2].ja.srt (newest sync 2025-10-10 17:44:07)       
2025-10-10 17:44:08,002 | Movies processed: 16/92, Subtitles synced: 1, Request failures: 0.
2025-10-10 17:44:08,003 | Syncing /data/media/media/movies/Django Unchained (2012)/Django.Unchained.2012.1080p.BluRay.x264.YIFY.en.srt (previous sync 2025-10-10 00:23:50)
2025-10-10 17:44:25,826 | Finished syncing /data/media/media/movies/Django Unchained (2012)/Django.Unchained.2012.1080p.BluRay.x264.YIFY.en.srt (newest sync 2025-10-10 17:44:24)
2025-10-10 17:44:25,898 | Movies processed: 40/92, Subtitles synced: 2, Request failures: 0.
2025-10-10 17:44:25,898 | Syncing /data/media/media/movies/KPop Demon Hunters (2025)/KPop.Demon.Hunters.2025.1080p.WEBRip.x264.AAC5.1-[YTS.MX].en.srt (previous sync 2025-10-10 00:24:09)
2025-10-10 17:44:42,138 | Finished syncing /data/media/media/movies/KPop Demon Hunters (2025)/KPop.Demon.Hunters.2025.1080p.WEBRip.x264.AAC5.1-[YTS.MX].en.srt (newest sync 2025-10-10 17:44:41)
2025-10-10 17:44:42,146 | Movies processed: 43/92, Subtitles synced: 3, Request failures: 0.
2025-10-10 17:44:42,146 | Syncing /data/media/media/movies/Kung Fu Panda 3 (2016)/Kung.Fu.Panda.3.2016.1080p.BluRay.x264-[YTS.AG].en.srt (previous sync 2025-10-10 00:24:22)
2025-10-10 17:44:53,026 | Finished syncing /data/media/media/movies/Kung Fu Panda 3 (2016)/Kung.Fu.Panda.3.2016.1080p.BluRay.x264-[YTS.AG].en.srt (newest sync 2025-10-10 17:44:52)
2025-10-10 17:44:53,072 | Movies processed: 58/92, Subtitles synced: 4, Request failures: 0.
2025-10-10 17:44:53,073 | Syncing /data/media/media/movies/Penguins of Madagascar (2014)/Penguins.of.Madagascar.2014.1080p.BluRay.x264.YIFY.en.srt (previous sync 2025-10-10 00:24:35)
2025-10-10 17:45:04,243 | Finished syncing /data/media/media/movies/Penguins of Madagascar (2014)/Penguins.of.Madagascar.2014.1080p.BluRay.x264.YIFY.en.srt (newest sync 2025-10-10 17:45:03)
2025-10-10 17:45:04,258 | Finished syncing movies. Movies processed: 92/92, Subtitles synced: 5, Request failures: 0.
```

### Docker
Assuming we use [this](#config-file-template) config file, we can run the following command to sync all movies:
```
docker run --rm -v /bazarrbulksync:/app wayhowma/bazarrbulksync:latest --sync movies
```

```
2025-10-10 17:43:57,772 | Bazarr Bulk Sync CLI Tool Arguments: Namespace(sync='movies', bazarr_base_url='http://192.168.1.251:6767/', bazarr_api_key='0f8b21b007f3f36da4cc94859060facd', output_messages_to_screen=True, log_messages_to_file=True, log_messages_file_path='./bazarrbulksync.log', max_movies_per_request=25, max_series_per_request=25, max_request_retries=3, request_timeout=1600, latest_to_sync='9999-12-31', original_format=None, max_offset_seconds=None, no_fix_framerate=None, gss=None, stop_on_request_failure=False)
2025-10-10 17:43:57,781 | Syncing movies. 92 movies to process...
2025-10-10 17:43:57,814 | Movies processed: 0/92, Subtitles synced: 0, Request failures: 0.
2025-10-10 17:43:57,814 | Syncing /data/media/media/movies/5 Centimeters per Second (2007)/[Arid] 5 Centimeters per Second (BDRip 1920x1080 Hi10 FLAC) [FD8B6FF2].ja.srt (previous sync 2025-10-10 00:23:31)
2025-10-10 17:44:07,993 | Finished syncing /data/media/media/movies/5 Centimeters per Second (2007)/[Arid] 5 Centimeters per Second (BDRip 1920x1080 Hi10 FLAC) [FD8B6FF2].ja.srt (newest sync 2025-10-10 17:44:07)       
2025-10-10 17:44:08,002 | Movies processed: 16/92, Subtitles synced: 1, Request failures: 0.
2025-10-10 17:44:08,003 | Syncing /data/media/media/movies/Django Unchained (2012)/Django.Unchained.2012.1080p.BluRay.x264.YIFY.en.srt (previous sync 2025-10-10 00:23:50)
2025-10-10 17:44:25,826 | Finished syncing /data/media/media/movies/Django Unchained (2012)/Django.Unchained.2012.1080p.BluRay.x264.YIFY.en.srt (newest sync 2025-10-10 17:44:24)
2025-10-10 17:44:25,898 | Movies processed: 40/92, Subtitles synced: 2, Request failures: 0.
2025-10-10 17:44:25,898 | Syncing /data/media/media/movies/KPop Demon Hunters (2025)/KPop.Demon.Hunters.2025.1080p.WEBRip.x264.AAC5.1-[YTS.MX].en.srt (previous sync 2025-10-10 00:24:09)
2025-10-10 17:44:42,138 | Finished syncing /data/media/media/movies/KPop Demon Hunters (2025)/KPop.Demon.Hunters.2025.1080p.WEBRip.x264.AAC5.1-[YTS.MX].en.srt (newest sync 2025-10-10 17:44:41)
2025-10-10 17:44:42,146 | Movies processed: 43/92, Subtitles synced: 3, Request failures: 0.
2025-10-10 17:44:42,146 | Syncing /data/media/media/movies/Kung Fu Panda 3 (2016)/Kung.Fu.Panda.3.2016.1080p.BluRay.x264-[YTS.AG].en.srt (previous sync 2025-10-10 00:24:22)
2025-10-10 17:44:53,026 | Finished syncing /data/media/media/movies/Kung Fu Panda 3 (2016)/Kung.Fu.Panda.3.2016.1080p.BluRay.x264-[YTS.AG].en.srt (newest sync 2025-10-10 17:44:52)
2025-10-10 17:44:53,072 | Movies processed: 58/92, Subtitles synced: 4, Request failures: 0.
2025-10-10 17:44:53,073 | Syncing /data/media/media/movies/Penguins of Madagascar (2014)/Penguins.of.Madagascar.2014.1080p.BluRay.x264.YIFY.en.srt (previous sync 2025-10-10 00:24:35)
2025-10-10 17:45:04,243 | Finished syncing /data/media/media/movies/Penguins of Madagascar (2014)/Penguins.of.Madagascar.2014.1080p.BluRay.x264.YIFY.en.srt (newest sync 2025-10-10 17:45:03)
2025-10-10 17:45:04,258 | Finished syncing movies. Movies processed: 92/92, Subtitles synced: 5, Request failures: 0.
```

Using the same config file as above, we run the sync again but only for movies that were never synced after 2025-10-10 17:44:26 using
```
docker run --rm -v /bazarrbulksync:/app wayhowma/bazarrbulksync:latest --sync movies --latest-to-sync "2025-10-10 17:44:26"
```

```
2025-10-10 17:47:17,964 | Bazarr Bulk Sync CLI Tool Arguments: Namespace(sync='movies', bazarr_base_url='http://192.168.1.251:6767/', bazarr_api_key='0f8b21b007f3f36da4cc94859060facd', output_messages_to_screen=True, log_messages_to_file=True, log_messages_file_path='./bazarrbulksync.log', max_movies_per_request=25, max_series_per_request=25, max_request_retries=3, request_timeout=1600, latest_to_sync='2025-10-10 17:44:26', original_format=None, max_offset_seconds=None, no_fix_framerate=None, gss=None, stop_on_request_failure=False)
2025-10-10 17:47:17,993 | Syncing movies. 92 movies to process...
2025-10-10 17:47:18,039 | Movies processed: 0/92, Subtitles synced: 0, Request failures: 0.
2025-10-10 17:47:18,039 | Syncing /data/media/media/movies/5 Centimeters per Second (2007)/[Arid] 5 Centimeters per Second (BDRip 1920x1080 Hi10 FLAC) [FD8B6FF2].ja.srt (previous sync 2025-10-10 17:44:07)
2025-10-10 17:47:28,116 | Finished syncing /data/media/media/movies/5 Centimeters per Second (2007)/[Arid] 5 Centimeters per Second (BDRip 1920x1080 Hi10 FLAC) [FD8B6FF2].ja.srt (newest sync 2025-10-10 17:47:27)
2025-10-10 17:47:28,146 | Movies processed: 16/92, Subtitles synced: 1, Request failures: 0.
2025-10-10 17:47:28,146 | Syncing /data/media/media/movies/Django Unchained (2012)/Django.Unchained.2012.1080p.BluRay.x264.YIFY.en.srt (previous sync 2025-10-10 17:44:24)
2025-10-10 17:47:46,712 | Finished syncing /data/media/media/movies/Django Unchained (2012)/Django.Unchained.2012.1080p.BluRay.x264.YIFY.en.srt (newest sync 2025-10-10 17:47:45)
2025-10-10 17:47:46,738 | Skipping /data/media/media/movies/KPop Demon Hunters (2025)/KPop.Demon.Hunters.2025.1080p.WEBRip.x264.AAC5.1-[YTS.MX].en.srt (last synced at 2025-10-10 17:44:41)
2025-10-10 17:47:46,746 | Skipping /data/media/media/movies/Kung Fu Panda 3 (2016)/Kung.Fu.Panda.3.2016.1080p.BluRay.x264-[YTS.AG].en.srt (last synced at 2025-10-10 17:44:52)
2025-10-10 17:47:46,770 | Skipping /data/media/media/movies/Penguins of Madagascar (2014)/Penguins.of.Madagascar.2014.1080p.BluRay.x264.YIFY.en.srt (last synced at 2025-10-10 17:45:03)
2025-10-10 17:47:46,784 | Finished syncing movies. Movies processed: 92/92, Subtitles synced: 2, Request failures: 0.
```

## Contributing
Contributions are welcome. Please open up an issue if you have ideas for improvements or submit a pull request on GitHub.

## Licensing
BazarrBulkSync is distributed under the MIT License.