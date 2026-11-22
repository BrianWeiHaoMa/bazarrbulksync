# bazarrbulksync

Have Bazarr sync, fix, or delete the subtitles of your whole library in one command.

Bazarr works on one movie or one series at a time. bazarrbulksync goes through your whole library using Bazarr's own API. Its main job is syncing every subtitle that isn't synced yet. That can take hours or days and runs unattended, and if it's interrupted, running the same command again continues where it stopped. It can also apply Bazarr's subtitle fixes, delete subtitles, and list them. Bazarr does the actual work. This tool picks what to send and waits for each result.

## Install

Download the file for your system from the [latest release](https://github.com/BrianWeiHaoMa/bazarrbulksync/releases/latest). It is a single executable and needs nothing else installed.

| System | File |
|---|---|
| Linux | `bazarrbulksync-linux-amd64`, `bazarrbulksync-linux-arm64` |
| macOS | `bazarrbulksync-darwin-arm64` (Apple silicon), `bazarrbulksync-darwin-amd64` (Intel) |
| Windows | `bazarrbulksync-windows-amd64.exe`, `bazarrbulksync-windows-arm64.exe` |

On Linux and macOS, rename the file to `bazarrbulksync`, run `chmod +x bazarrbulksync`, and put it on your `PATH`. On macOS, if a file downloaded with a browser is blocked, run `xattr -d com.apple.quarantine bazarrbulksync`.

Or use the container image: `docker run --rm wayhowma/bazarrbulksync --help`.

You need Bazarr 1.6.0 or newer.

## Quick start

```sh
export BAZARR_URL=http://192.168.1.10:6767   # include any base path, e.g. https://example.com/bazarr
export BAZARR_API_KEY=0123456789abcdef       # Bazarr: Settings > General > Security > API Key

bazarrbulksync list            # every external subtitle Bazarr knows about, then totals
bazarrbulksync sync --dry-run  # what sync would do, without changing anything
bazarrbulksync sync            # asks first, then syncs one subtitle at a time
```

```
$ bazarrbulksync sync --title sintel --yes
synced  Sintel [movie 2]  de  /media/movies/Sintel (2010)/Sintel (2010).de.srt
synced  Sintel [movie 2]  en HI  /media/movies/Sintel (2010)/Sintel (2010).en.hi.srt
synced  Sintel [movie 2]  en  /media/movies/Sintel (2010)/Sintel (2010).en.srt
synced  Sintel [movie 2]  es  /media/movies/Sintel (2010)/Sintel (2010).es.srt

Total: 5 of 5 selected subtitles
synced 4, skipped (forced) 1
```

You can press Ctrl-C at any point (a sync already handed to Bazarr still finishes there). Running the command again skips everything already synced.

## Commands

| Command | What it does |
|---|---|
| `list` | Prints one line per subtitle, then the totals per language. `--json` prints one JSON object per line instead. |
| `sync` | Has Bazarr sync every selected subtitle that isn't already synced. Each result is printed when Bazarr finishes it. |
| `fix FIX` | Has Bazarr apply one fix to each subtitle: `remove-hi`, `remove-tags`, `ocr-fixes`, `common-fixes`, `fix-uppercase`, or `remove-emoji`. |
| `delete` | Has Bazarr delete the selected subtitles. You must give at least one selection option. |

`sync`, `fix`, and `delete` ask before changing anything. Add `--dry-run` to see what they would do, or `--yes` to skip the question (required when the command doesn't run in a terminal, e.g. under cron). Each command takes `--help`.

### Choosing subtitles

Every command takes the same options. Without any, it acts on every external subtitle (`delete` refuses to run without one). Subtitles embedded in video files are never touched.

| Option | Selects |
|---|---|
| `--movies`, `--series` | only movie subtitles, or only episode subtitles |
| `--title TEXT` | movies and series whose title contains TEXT (ignoring case) |
| `--movie-id N`, `--series-id N` | the movie or series with that Radarr or Sonarr ID, as Bazarr shows it |
| `--season N` | episodes of that season (specials are season 0) |
| `--language CODE` | that language, by the code `list` prints (e.g. `en`) |
| `--hi`, `--no-hi` | only, or no, hearing-impaired subtitles |
| `--forced`, `--no-forced` | only, or no, forced subtitles |

A subtitle has to match every kind of option you give. If you repeat an option, or combine `--title`, `--movie-id`, and `--series-id`, matching any one of the values is enough. A title or ID that matches nothing in your library is an error, and nothing is changed.

```sh
bazarrbulksync sync --title "breaking bad" --season 2
bazarrbulksync sync --language ar --reference lang:en   # sync Arabic subtitles against the English ones
bazarrbulksync fix remove-tags --movies
bazarrbulksync delete --language es --series-id 12 --dry-run
bazarrbulksync list --no-forced --json
```

### Sync options

| Option | Effect |
|---|---|
| `--max-offset-seconds N`, `--no-fix-framerate`, `--gss` | the same settings as Bazarr's own sync dialog |
| `--reference REF` | what to sync against: `a:N` (audio track N, 0 to 9), `s:N` (embedded subtitle track N, 0 to 9), or `lang:CODE` (the same video's non-forced subtitle in that language; subtitles in that language, and videos without one, are skipped) |
| `--concurrency N` | keep up to N of the run's syncs in Bazarr at once (default 1). Raise it if Bazarr's machine has CPU to spare. |
| `--resync` | also sync subtitles that are already synced |
| `--retry-failed` | also retry subtitles whose sync failed in an earlier run |
| `--state-file PATH` | where failed syncs are remembered (`sync --help` prints the default) |

## How `sync` decides what to do

A subtitle counts as synced when Bazarr's history for its file has a sync newer than its last download, upgrade, upload, or translation. That's why a second run skips what's done, and why a scheduled run picks up only subtitles that are new or replaced since. Forced subtitles are always skipped because Bazarr doesn't sync them.

If Bazarr runs a sync but doesn't complete it, the subtitle is reported `failed` and noted in a small state file. Later runs skip it until Bazarr's history shows something new for that file (a download, upgrade, upload, translation, or sync), or until you pass `--retry-failed`. A sync Bazarr refuses is reported `failed` with Bazarr's reason but not remembered. If Bazarr restarts before recording the sync, the outcome can't be known: the subtitle is reported `unconfirmed`, nothing is remembered, and the next run tries it again.

## Running on a schedule

```sh
docker run --rm -e BAZARR_URL -e BAZARR_API_KEY -v bazarrbulksync:/data wayhowma/bazarrbulksync sync --yes
```

`-e BAZARR_URL -e BAZARR_API_KEY` passes the variables exported as in the quick start. The volume keeps the state file (`/data/state.json`) between runs. Inside a container, `localhost` means the container itself. If Bazarr runs in Docker too, add `--network <Bazarr's network>` and use its container name, e.g. `BAZARR_URL=http://bazarr:6767`.

Exit codes: `0` when everything succeeded or nothing was selected, `1` when something failed or the run was stopped (including any subtitle reported `failed` or `unconfirmed`), and `2` for a usage error. Result lines go to standard output. Progress, prompts, and errors go to standard error.

## Good to know

- Bazarr changes the subtitle files themselves, and nothing can be undone. Use `--dry-run` first.
- Run only one bazarrbulksync at a time against the same Bazarr, or with the same state file.
- With Bazarr 1.6.0 to 1.6.2 set to keep subtitles in a custom folder, Bazarr's `remove-hi` deletes a hearing-impaired subtitle that has no plain counterpart and then fails. `fix remove-hi` reports it `failed`, but the file is gone.
- Upgrading from 0.3.x? 0.4.0 is a rewrite. There's no config file anymore and the commands have changed: see the [changelog](CHANGELOG.md).

## Development

`go test ./...` runs the offline tests against a fake Bazarr. What the tool promises is in [PRODUCT_SPECIFICATION.md](PRODUCT_SPECIFICATION.md), and how it is built is in [TECHNICAL_SPECIFICATION.md](TECHNICAL_SPECIFICATION.md).

## License

[MIT](LICENSE)
