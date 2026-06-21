from __future__ import annotations

import logging
from collections.abc import Callable, Iterable
from typing import Protocol

from .client import BazarrApiError
from .models import DeleteOptions, ItemType, PatchOptions, Subtitle, SyncJob, SyncOptions, SyncProgress, SyncResult, SyncSummary

logger = logging.getLogger(__name__)

ProgressCallback = Callable[[SyncProgress], None]


class SupportsSubtitleSync(Protocol):
    def sync_subtitle(
        self,
        *,
        item_type: ItemType,
        item_id: int,
        subtitle: Subtitle,
        options: SyncOptions,
    ) -> None: ...


class SupportsSubtitlePatch(Protocol):
    def patch_subtitle(
        self,
        *,
        action: str,
        item_type: ItemType,
        item_id: int,
        subtitle: Subtitle,
        options: PatchOptions,
    ) -> None: ...


class SupportsSubtitleDelete(Protocol):
    def delete_subtitle(
        self,
        *,
        item_type: ItemType,
        item_id: int,
        subtitle: Subtitle,
        options: DeleteOptions,
        series_id: int | None = None,
    ) -> None: ...


class SyncEngine:
    def __init__(self, client: SupportsSubtitleSync) -> None:
        self.client = client

    def run(
        self,
        jobs: Iterable[SyncJob],
        *,
        options: SyncOptions,
        dry_run: bool = False,
        progress: ProgressCallback | None = None,
        total: int | None = None,
    ) -> SyncSummary:
        job_list = jobs if total is not None else list(jobs)
        total_jobs = total if total is not None else len(job_list)  # type: ignore[arg-type]
        summary = SyncSummary()
        logger.info("Sync engine starting total_jobs=%s dry_run=%s", total_jobs, dry_run)

        for completed, job in enumerate(job_list, start=1):
            logger.debug("Sync job %s/%s: %s", completed, total_jobs, job.display_name)
            result = self._run_one(job, options=options, dry_run=dry_run)
            logger.info("Sync result status=%s job=%s message=%s", result.status, job.display_name, result.message or "")
            summary.results.append(result)
            if progress:
                progress(SyncProgress(completed=completed, total=total_jobs, job=job, result=result))

        logger.info(
            "Sync engine finished synced=%s skipped=%s failed=%s dry_run=%s total=%s",
            summary.synced,
            summary.skipped,
            summary.failed,
            summary.dry_run,
            summary.total,
        )
        return summary

    def _run_one(self, job: SyncJob, *, options: SyncOptions, dry_run: bool) -> SyncResult:
        if not job.subtitle.path:
            return SyncResult(job=job, status="skipped", message="Subtitle has no local path")

        if dry_run:
            return SyncResult(job=job, status="dry-run", message="Would sync subtitle")

        try:
            self.client.sync_subtitle(
                item_type=job.item_type,
                item_id=job.item_id,
                subtitle=job.subtitle,
                options=options,
            )
        except (BazarrApiError, ValueError) as exc:
            return SyncResult(job=job, status="failed", message=str(exc))

        return SyncResult(job=job, status="synced", message="Subtitle synced")


class PatchEngine:
    def __init__(self, client: SupportsSubtitlePatch) -> None:
        self.client = client

    def run(
        self,
        jobs: Iterable[SyncJob],
        *,
        action: str,
        options: PatchOptions,
        dry_run: bool = False,
        progress: ProgressCallback | None = None,
        total: int | None = None,
    ) -> SyncSummary:
        job_list = jobs if total is not None else list(jobs)
        total_jobs = total if total is not None else len(job_list)  # type: ignore[arg-type]
        summary = SyncSummary()
        logger.info("Patch engine starting action=%s total_jobs=%s dry_run=%s", action, total_jobs, dry_run)

        for completed, job in enumerate(job_list, start=1):
            logger.debug("Patch job %s/%s: %s", completed, total_jobs, job.display_name)
            result = self._run_one(job, action=action, options=options, dry_run=dry_run)
            logger.info("Patch result status=%s job=%s message=%s", result.status, job.display_name, result.message or "")
            summary.results.append(result)
            if progress:
                progress(SyncProgress(completed=completed, total=total_jobs, job=job, result=result))

        logger.info(
            "Patch engine finished action=%s patched=%s skipped=%s failed=%s dry_run=%s total=%s",
            action,
            summary.synced,
            summary.skipped,
            summary.failed,
            summary.dry_run,
            summary.total,
        )
        return summary

    def _run_one(self, job: SyncJob, *, action: str, options: PatchOptions, dry_run: bool) -> SyncResult:
        if not job.subtitle.path:
            return SyncResult(job=job, status="skipped", message="Subtitle has no local path")

        if dry_run:
            return SyncResult(job=job, status="dry-run", message=f"Would apply `{action}` patch on subtitle")

        try:
            self.client.patch_subtitle(
                action=action,
                item_type=job.item_type,
                item_id=job.item_id,
                subtitle=job.subtitle,
                options=options,
            )
        except (BazarrApiError, ValueError) as exc:
            return SyncResult(job=job, status="failed", message=str(exc))

        return SyncResult(job=job, status="synced", message=f"Successfully applied `{action}` patch on subtitle")


class DeleteEngine:
    def __init__(self, client: SupportsSubtitleDelete) -> None:
        self.client = client

    def run(
        self,
        jobs: Iterable[SyncJob],
        *,
        options: DeleteOptions,
        dry_run: bool = False,
        progress: ProgressCallback | None = None,
        total: int | None = None,
    ) -> SyncSummary:
        job_list = jobs if total is not None else list(jobs)
        total_jobs = total if total is not None else len(job_list)  # type: ignore[arg-type]
        summary = SyncSummary()
        logger.info("Delete engine starting total_jobs=%s dry_run=%s", total_jobs, dry_run)

        for completed, job in enumerate(job_list, start=1):
            logger.debug("Delete job %s/%s: %s", completed, total_jobs, job.display_name)
            result = self._run_one(job, options=options, dry_run=dry_run)
            logger.info("Delete result status=%s job=%s message=%s", result.status, job.display_name, result.message or "")
            summary.results.append(result)
            if progress:
                progress(SyncProgress(completed=completed, total=total_jobs, job=job, result=result))

        logger.info(
            "Delete engine finished deleted=%s skipped=%s failed=%s dry_run=%s total=%s",
            summary.synced,
            summary.skipped,
            summary.failed,
            summary.dry_run,
            summary.total,
        )
        return summary

    def _run_one(self, job: SyncJob, *, options: DeleteOptions, dry_run: bool) -> SyncResult:
        if not job.subtitle.path:
            return SyncResult(job=job, status="skipped", message="Subtitle has no local path")

        if job.item_type == ItemType.EPISODE and job.series_id is None:
            return SyncResult(job=job, status="failed", message="Episode delete requires series_id")

        if dry_run:
            return SyncResult(job=job, status="dry-run", message="Would delete subtitle")

        try:
            self.client.delete_subtitle(
                item_type=job.item_type,
                item_id=job.item_id,
                subtitle=job.subtitle,
                options=options,
                series_id=job.series_id,
            )
        except (BazarrApiError, ValueError) as exc:
            return SyncResult(job=job, status="failed", message=str(exc))

        return SyncResult(job=job, status="synced", message="Subtitle deleted")

