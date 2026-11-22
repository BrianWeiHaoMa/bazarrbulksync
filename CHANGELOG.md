# Changelog

All notable changes to bazarrbulksync. Versions follow [Semantic Versioning](https://semver.org/).

## [0.4.0] - 2026-11-22

A rewrite in Go with the same purpose. Its commands and configuration are new, so scripts and Docker setups written for 0.3.0 need updating.

### Added

- Single executables for Linux, macOS, and Windows on x86-64 and arm64, attached to each GitHub release. They need no Python or any other runtime. The container image `wayhowma/bazarrbulksync` now holds just the executable and CA certificates, and is built for linux/arm64 as well as linux/amd64.
- `sync` judges each subtitle from Bazarr's history just before submitting it. A subtitle whose newest download, upgrade, upload, or translation already has a sync after it is skipped as `currently synced`, so a stopped run continues where it left off and a scheduled run only syncs what's new. `--resync` syncs them anyway.
- `sync` follows each job through Bazarr's queue and reports `synced` only once Bazarr's history records the sync. It reports `failed` when Bazarr ran the sync and recorded nothing, and `unconfirmed` when Bazarr restarted or never answered.
- Failed syncs are remembered in a state file and skipped by later runs until Bazarr's history shows something new for the file. Use `--retry-failed` to retry them and `--state-file` to choose the file.
- `sync --concurrency N` limits how many of the run's syncs are in Bazarr at once (default 1).
- `sync --reference lang:CODE` syncs each subtitle against the same video's subtitle in another language, skipping videos that have none.
- `list` prints the selected subtitles with totals, or one JSON object per subtitle with `--json`.
- The same selection options on every command: `--movies`, `--series`, `--title`, `--movie-id`, `--series-id`, `--season`, `--language`, `--hi`/`--no-hi`, `--forced`/`--no-forced`.
- `--dry-run` on `sync`, `fix`, and `delete` shows what would be done and why. A run asks for confirmation unless `--yes` is given.
- The connection is checked before anything else: a wrong address, a rejected API key, or a Bazarr older than 1.6.0 is reported and nothing is changed. Reads are retried for up to 5 minutes when Bazarr stops answering.
- Ctrl-C and `docker stop` stop a run at once, sending no further change, and print the totals so far. Syncs already handed to Bazarr are left for Bazarr to finish.

### Changed

- The connection comes from `BAZARR_URL` and `BAZARR_API_KEY`, or `--url` and `--api-key`. The YAML configuration file is gone.
- `patch` is now `fix`, with the fix names `remove-hi`, `remove-tags`, `ocr-fixes`, `common-fixes`, `fix-uppercase`, and the new `remove-emoji`. `fix remove-hi` skips a subtitle when Bazarr would write the result over another subtitle of the same video and language.
- `sync all` and `sync before DATE` are replaced by `sync`, which skips the subtitles Bazarr's history shows as currently synced (`--resync` includes them). `patch` and `delete` lose their `all` and `before` subcommands the same way.
- IDs are given one per option, repeated as needed: `--series-id 10 --series-id 20` instead of `--series-ids 10,20`.
- `delete` requires at least one selection option.
- Bazarr 1.6.0 or newer is required.
- Exit codes: 0 for success or an empty selection, 1 for a failure, and 2 for a usage error.

### Removed

- The Python package. bazarrbulksync is no longer published to PyPI.
- The `translate` and `reverse_rtl` patch actions.
- Log files and the `--log`, `--log-file`, and `--log-debug` options.
- `--episode-ids`: there is no selection by episode; narrow with `--series-id` and `--season`.
- `--media-type` (use `--movies` or `--series`) and the chunk-size options.

## [0.3.0] - 2026-06-21

### Added

- `patch` to apply Bazarr's subtitle modifications in bulk.
- `delete` to delete external subtitles in bulk.

### Changed

- The sample configuration file turns file logging off.

## [0.2.0] - 2026-05-14

### Changed

- Rewritten as the Python package `bazarrbulksync`, configured by `bazarrbulksync_config.yml`, with `sync all` and `sync before`.

## [0.1.0] - 2025-10-10

### Added

- Progress figures in the log.

[0.4.0]: https://github.com/BrianWeiHaoMa/bazarrbulksync/compare/v0.3.0...v0.4.0
[0.3.0]: https://github.com/BrianWeiHaoMa/bazarrbulksync/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/BrianWeiHaoMa/bazarrbulksync/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/BrianWeiHaoMa/bazarrbulksync/compare/v0.0.0...v0.1.0
