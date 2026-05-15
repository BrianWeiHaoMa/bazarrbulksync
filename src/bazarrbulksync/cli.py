from __future__ import annotations

import sys
from collections.abc import Iterable
from pathlib import Path
from typing import Annotated

import typer
from rich.console import Console
from rich.progress import (
    BarColumn,
    MofNCompleteColumn,
    Progress,
    SpinnerColumn,
    TaskProgressColumn,
    TextColumn,
    TimeElapsedColumn,
)
from rich.table import Table

from .client import BazarrClient
from .config import AppConfig, DEFAULT_LOG_FILE_PATH, load_config, merge_sync_options
from .engine import SyncEngine
from .logging_setup import setup_sync_logging, teardown_sync_logging
from .models import MediaType, SyncJob, SyncOptions, SyncProgress, SyncSummary, parse_before_argument
from .planner import SyncPlanner

app = typer.Typer(help="Bulk sync subtitles through Bazarr.")
sync_app = typer.Typer(help="Run bulk subtitle synchronization.")
app.add_typer(sync_app, name="sync")
console = Console()


ConfigOption = Annotated[Path | None, typer.Option("--config", "-c", help="Path to config file.")]
SeriesChunkOption = Annotated[int | None, typer.Option("--series-chunk-size", min=1, help="Series fetched per Bazarr list API call.")]
MoviesChunkOption = Annotated[int | None, typer.Option("--movies-chunk-size", min=1, help="Movies fetched per Bazarr list API call.")]
EpisodesChunkOption = Annotated[int | None, typer.Option("--episodes-chunk-size", min=1, help="Approximate episodes fetched per API call (approximate because we fetch all the episodes in an entire series at a time).")]
BeforeHistoryBatchOption = Annotated[
    int | None,
    typer.Option(
        "--before-history-batch-size",
        min=1,
        help="Episode/movie IDs per batch when fetching Bazarr history for `sync before`.",
    ),
]
MediaTypeOption = Annotated[MediaType, typer.Option("--media-type", help="Which type of media to sync.")]
LanguageOption = Annotated[str | None, typer.Option("--language", help="Only sync subtitles with this code2 language.")]
ForcedOption = Annotated[bool | None, typer.Option("--forced/--no-forced", help="Override forced flag.")]
HiOption = Annotated[bool | None, typer.Option("--hi/--no-hi", help="Override hearing-impaired flag.")]
MaxOffsetOption = Annotated[int | None, typer.Option("--max-offset-seconds", min=1, help="Maximum sync offset seconds.")]
NoFixOption = Annotated[bool | None, typer.Option("--no-fix-framerate/--fix-framerate", help="Toggle framerate fixing.")]
GssOption = Annotated[bool | None, typer.Option("--gss/--no-gss", help="Toggle usage of Golden-Section Search algorithm during syncing.")]
ReferenceOption = Annotated[str | None, typer.Option("--reference", help="Sync reference track or subtitle path.")]
DryRunOption = Annotated[bool, typer.Option("--dry-run", help="Simulate work without calling sync.")]
YesOption = Annotated[bool, typer.Option("--yes", "-y", help="Skip confirmation prompts.")]
LogOption = Annotated[
    bool | None,
    typer.Option(
        "--log/--no-log",
        help="Turn file logging on or off for this run.",
    ),
]
LogFileOption = Annotated[
    Path | None,
    typer.Option(
        "--log-file",
        help="Log file path for this run (implies logging on).",
    ),
]
LogDebugOption = Annotated[
    bool | None,
    typer.Option(
        "--log-debug/--no-log-debug",
        help="Verbose file log when a log file is used.",
    ),
]

SeriesIdsOption = Annotated[
    str | None,
    typer.Option(
        "--series-ids",
        help="Comma-separated Sonarr series IDs to sync (all episodes from each series given will be synced).",
    ),
]
MovieIdsOption = Annotated[
    str | None,
    typer.Option(
        "--movie-ids",
        help="Comma-separated Radarr movie IDs to sync.",
    ),
]
EpisodeIdsOption = Annotated[
    str | None,
    typer.Option(
        "--episode-ids",
        help="Comma-separated Sonarr episode IDs to sync.",
    ),
]


def _parse_csv_ints(value: str | None) -> list[int]:
    if value is None or not value.strip():
        return []
    out: list[int] = []
    for part in value.split(","):
        piece = part.strip()
        if not piece:
            continue
        try:
            out.append(int(piece))
        except ValueError as exc:
            raise typer.BadParameter(f"Not an integer: {piece!r}") from exc
    return out


def _cli_id_lists_for_sync(
    media_type: MediaType,
    series_ids: str | None,
    movie_ids: str | None,
    episode_ids: str | None,
) -> tuple[list[int], list[int], list[int], bool]:
    s_raw = _parse_csv_ints(series_ids)
    m_raw = _parse_csv_ints(movie_ids)
    e_raw = _parse_csv_ints(episode_ids)
    any_nonempty_option = bool(len(s_raw) or len(m_raw) or len(e_raw))

    s = s_raw if media_type in (MediaType.SERIES, MediaType.ALL) else []
    m = m_raw if media_type in (MediaType.MOVIES, MediaType.ALL) else []
    e = e_raw if media_type in (MediaType.SERIES, MediaType.ALL) else []
    use = bool(s or m or e)
    if any_nonempty_option and not use:
        raise typer.BadParameter(
            "ID options were given but none apply for the current --media-type. "
            "Use series or all with --series-ids / --episode-ids, and movies or all with --movie-ids."
        )
    return s, m, e, use


def _effective_log_settings(
    config: AppConfig,
    *,
    log: bool | None,
    log_file: Path | None,
    log_debug: bool | None,
) -> tuple[Path | None, bool]:
    enabled = config.log_enabled if log is None else log
    if log_file is not None:
        enabled = True
    if not enabled:
        return None, config.log_debug if log_debug is None else log_debug

    eff_path = log_file if log_file is not None else (config.log_file or DEFAULT_LOG_FILE_PATH)
    eff_debug = config.log_debug if log_debug is None else log_debug
    return eff_path, eff_debug


@sync_app.command("all", help="Sync without date/datetime restriction.")
def sync_all(
    config_path: ConfigOption = None,
    series_chunk_size: SeriesChunkOption = None,
    movies_chunk_size: MoviesChunkOption = None,
    episodes_chunk_size: EpisodesChunkOption = None,
    series_ids: SeriesIdsOption = None,
    movie_ids: MovieIdsOption = None,
    episode_ids: EpisodeIdsOption = None,
    media_type: MediaTypeOption = MediaType.ALL,
    language: LanguageOption = None,
    forced: ForcedOption = None,
    hi: HiOption = None,
    max_offset_seconds: MaxOffsetOption = None,
    no_fix_framerate: NoFixOption = None,
    gss: GssOption = None,
    reference: ReferenceOption = None,
    dry_run: DryRunOption = False,
    yes: YesOption = False,
    log: LogOption = None,
    log_file: LogFileOption = None,
    log_debug: LogDebugOption = None,
) -> None:
    config, options = _load_runtime(
        config_path,
        series_chunk_size=series_chunk_size,
        movies_chunk_size=movies_chunk_size,
        episodes_chunk_size=episodes_chunk_size,
        media_type=media_type,
        language=language,
        forced=forced,
        hi=hi,
        max_offset_seconds=max_offset_seconds,
        no_fix_framerate=no_fix_framerate,
        gss=gss,
        reference=reference,
    )
    s, m, e, use_ids = _cli_id_lists_for_sync(config.media_type, series_ids, movie_ids, episode_ids)
    eff_log_file, eff_log_debug = _effective_log_settings(config, log=log, log_file=log_file, log_debug=log_debug)
    with BazarrClient(config) as client:
        planner = SyncPlanner(client)
        if use_ids:
            console.print("[cyan]Planning sync for the given Sonarr/Radarr IDs...[/cyan]")
            jobs = planner.iter_jobs_for_ids(
                series_ids=s,
                movie_ids=m,
                episode_ids=e,
                options=options,
                episodes_chunk_size=config.episodes_chunk_size,
            )
        else:
            console.print(
                "[cyan]Counting total number of subtitle files to sync. This should take only a few seconds...[/cyan]"
            )
            jobs = planner.iter_all_jobs(
                media_type=config.media_type,
                series_chunk_size=config.series_chunk_size,
                movies_chunk_size=config.movies_chunk_size,
                episodes_chunk_size=config.episodes_chunk_size,
                options=options,
            )
        _run_jobs(
            client,
            jobs,
            options=options,
            dry_run=dry_run,
            yes=yes,
            log_file=eff_log_file,
            log_debug=eff_log_debug,
        )


@sync_app.command(
    "before",
    help="Only sync subtitles whose media was last synced before a given date/datetime.",
)
def sync_before(
    before: Annotated[
        str,
        typer.Argument(
            help=(
                "Only include media whose last Bazarr sync is strictly before this instant (date or datetime), "
                "e.g. 2025-09-01 or '2025-09-01 14:30:00'."
            ),
        ),
    ],
    config_path: ConfigOption = None,
    series_chunk_size: SeriesChunkOption = None,
    movies_chunk_size: MoviesChunkOption = None,
    episodes_chunk_size: EpisodesChunkOption = None,
    series_ids: SeriesIdsOption = None,
    movie_ids: MovieIdsOption = None,
    episode_ids: EpisodeIdsOption = None,
    before_history_batch_size: BeforeHistoryBatchOption = None,
    media_type: MediaTypeOption = MediaType.ALL,
    language: LanguageOption = None,
    forced: ForcedOption = None,
    hi: HiOption = None,
    max_offset_seconds: MaxOffsetOption = None,
    no_fix_framerate: NoFixOption = None,
    gss: GssOption = None,
    reference: ReferenceOption = None,
    dry_run: DryRunOption = False,
    yes: YesOption = False,
    log: LogOption = None,
    log_file: LogFileOption = None,
    log_debug: LogDebugOption = None,
) -> None:
    try:
        threshold = parse_before_argument(before)
    except ValueError as exc:
        raise typer.BadParameter(str(exc)) from exc
    config, options = _load_runtime(
        config_path,
        series_chunk_size=series_chunk_size,
        movies_chunk_size=movies_chunk_size,
        episodes_chunk_size=episodes_chunk_size,
        before_history_batch_size=before_history_batch_size,
        media_type=media_type,
        language=language,
        forced=forced,
        hi=hi,
        max_offset_seconds=max_offset_seconds,
        no_fix_framerate=no_fix_framerate,
        gss=gss,
        reference=reference,
    )
    s, m, e, use_ids = _cli_id_lists_for_sync(config.media_type, series_ids, movie_ids, episode_ids)
    eff_log_file, eff_log_debug = _effective_log_settings(config, log=log, log_file=log_file, log_debug=log_debug)
    with BazarrClient(config) as client:
        planner = SyncPlanner(client)
        if use_ids:
            console.print(
                f"[cyan]Finding subtitle files for the given IDs whose last sync was before "
                f"{threshold.strftime('%Y-%m-%d %H:%M:%S')}...[/cyan]"
            )
            id_jobs = list(
                planner.iter_jobs_for_ids(
                    series_ids=s,
                    movie_ids=m,
                    episode_ids=e,
                    options=options,
                    episodes_chunk_size=config.episodes_chunk_size,
                )
            )
            jobs = planner.iter_before_jobs_from_jobs(
                threshold,
                id_jobs,
                history_lookup_batch_size=before_history_batch_size,
            )
        else:
            console.print(
                f"[cyan]Finding all subtitle files whose last sync was before "
                f"{threshold.strftime('%Y-%m-%d %H:%M:%S')}. On large libraries, this could take a few minutes...[/cyan]"
            )
            episode_item_ids, movie_item_ids = planner.collect_job_item_ids(
                media_type=config.media_type,
                series_chunk_size=config.series_chunk_size,
                movies_chunk_size=config.movies_chunk_size,
                episodes_chunk_size=config.episodes_chunk_size,
                options=options,
            )
            jobs = planner.iter_before_jobs(
                threshold,
                episode_item_ids,
                movie_item_ids,
                media_type=config.media_type,
                series_chunk_size=config.series_chunk_size,
                movies_chunk_size=config.movies_chunk_size,
                episodes_chunk_size=config.episodes_chunk_size,
                options=options,
                history_lookup_batch_size=before_history_batch_size,
            )
        _run_jobs(
            client,
            jobs,
            options=options,
            dry_run=dry_run,
            yes=yes,
            log_file=eff_log_file,
            log_debug=eff_log_debug,
        )


def _load_runtime(
    config_path: Path | None,
    *,
    series_chunk_size: int | None,
    movies_chunk_size: int | None,
    episodes_chunk_size: int | None,
    before_history_batch_size: int | None = None,
    media_type: MediaType,
    language: str | None,
    forced: bool | None,
    hi: bool | None,
    max_offset_seconds: int | None,
    no_fix_framerate: bool | None,
    gss: bool | None,
    reference: str | None,
) -> tuple[AppConfig, SyncOptions]:
    config = load_config(config_path)
    options = merge_sync_options(
        config.sync_options,
        language=language,
        forced=forced,
        hi=hi,
        max_offset_seconds=max_offset_seconds,
        no_fix_framerate=no_fix_framerate,
        gss=gss,
        reference=reference,
    )
    return (
        config.with_overrides(
            series_chunk_size=series_chunk_size,
            movies_chunk_size=movies_chunk_size,
            episodes_chunk_size=episodes_chunk_size,
            before_history_batch_size=before_history_batch_size,
            media_type=media_type,
            sync_options=options,
        ),
        options,
    )


def _run_jobs(
    client: BazarrClient,
    jobs: Iterable[SyncJob],
    *,
    options: SyncOptions,
    dry_run: bool,
    yes: bool,
    log_file: Path | None = None,
    log_debug: bool = False,
) -> None:
    setup_sync_logging(log_file, debug=log_debug)
    try:
        job_list = list(jobs)
        if not job_list:
            console.print("[yellow]No local subtitle files matched the request.[/yellow]")
            return

        action = "dry-run" if dry_run else "sync"
        if not dry_run and not yes and sys.stdin.isatty():
            typer.confirm(f"About to {action} {len(job_list)} subtitle files. Continue?", abort=True)

        console.print(f"[cyan]Syncing {len(job_list)} subtitles now...[/cyan]")
        engine = SyncEngine(client)
        if sys.stdout.isatty():
            with Progress(
                TimeElapsedColumn(),
                SpinnerColumn(),
                BarColumn(),
                MofNCompleteColumn(),
                TaskProgressColumn(),
                TextColumn("[progress.description]{task.description}"),
                console=console,
            ) as progress:
                task_id = progress.add_task("Syncing subtitles", total=len(job_list))

                def update(event: SyncProgress) -> None:
                    description = "Finished: " + event.job.display_name if event.job else "Syncing subtitles"
                    progress.update(task_id, completed=event.completed, description=description)

                summary = engine.run(job_list, options=options, dry_run=dry_run, progress=update, total=len(job_list))
        else:
            summary = engine.run(job_list, options=options, dry_run=dry_run, total=len(job_list))

        _print_summary(summary)
        if summary.failed:
            raise typer.Exit(1)
    finally:
        teardown_sync_logging()


def _print_summary(summary: SyncSummary) -> None:
    table = Table(title="Bazarr Bulk Sync Summary")
    table.add_column("Status")
    table.add_column("Count", justify="right")
    table.add_row("Synced", str(summary.synced))
    table.add_row("Dry run", str(summary.dry_run))
    table.add_row("Skipped", str(summary.skipped))
    table.add_row("Failed", str(summary.failed))
    table.add_row("Total", str(summary.total))
    console.print(table)

    failures = [result for result in summary.results if result.status == "failed"]
    for result in failures[:10]:
        console.print(f"[red]FAILED[/red] {result.job.display_name}: {result.message}")
    if len(failures) > 10:
        console.print(f"[red]...and {len(failures) - 10} more failures[/red]")


if __name__ == "__main__":
    app()

