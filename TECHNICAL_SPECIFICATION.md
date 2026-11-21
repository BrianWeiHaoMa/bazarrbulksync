# bazarrbulksync — Technical Specification

## Authority

`PRODUCT_SPECIFICATION.md` outranks this document. This document ranks second and outranks every other document, decision, and line of code in this project. Product decisions are not restated here: this document is read together with `PRODUCT_SPECIFICATION.md`, never instead of it. `Approach`, `Foundations`, `Composition`, `State`, `Standing rules`, and `Product decisions settled here` hold unconditionally and are checked by reading the built system. `How the guarantees hold` is true while the system runs, for every run inside the product's operating envelope and inside `Substrate assumptions`, which narrow no product guarantee; where an entry names a limit, the limit is part of the decision. Unassigned implementation choices are deliberately open for downstream work at its own level. An explicitly pending decision remains with its named owner and is not a delegated choice. Work that finds a line here unworkable stops the dependent work and reports the conflict and a proposed amendment; independent work continues. It does not work around the line or add an assumption to make a failure disappear. A conflict that reaches a product line is resolved by amending `PRODUCT_SPECIFICATION.md` first. Labels (`S1`, `R1`, `P1`, `T1`) are permanent and are how plans cite this document.

## Approach

bazarrbulksync is a client of Bazarr's HTTP API that carries nothing from one run to the next except the recorded failures of G21: every decision about a subtitle is taken from what Bazarr answers in this run about its library, its history, its job queue, and its own start time. Bazarr runs a sync as a queued job and answers the request before the job runs, without a job identifier, so the tool follows a sync by the job's name in Bazarr's queue and judges its result from Bazarr's history.

One run is one sequential loop with at most one HTTP request in flight, whatever `--concurrency` is: `--concurrency` bounds the syncs the run leaves in Bazarr, not its requests. The only other activity is the wait for a stop signal. One copy runs per Bazarr server and state file (E2); the tool takes no lock.

## Foundations

- **Go**, the current stable release (1.27 when written), with cgo disabled: one Linux machine cross-compiles the six executables of F2 and both image platforms, and each executable needs nothing installed (S6).
- **The Go standard library** for HTTP, TLS against the operating system's trust store, JSON, signals, and files. A third-party module is admitted only inside one part, and only when it builds without cgo.
- **The container image** holds the Linux executable of its platform, built with `/data/state.json` as its default state path, and a CA certificate bundle (S7). Its entry point is the executable in exec form, so the tool is process 1 and receives the stop signal itself (S8). The release executables are built with the default path of P1.

## Composition

A **video** is a movie or an episode. An **event** is one entry of a video's history in Bazarr.

### Invocation

Turns the command line and environment into one validated request or a usage error (G1, G26), and owns the help and version text. It resolves the state file's path (P1), which `sync --help` prints and the failure store receives. It contacts nothing and opens no file.

### Bazarr client

The only part that opens a network connection. It owns the address, the API key, the time limit every request carries (Go's HTTP client has none unless given one), and the retrying of G3. Other parts receive subtitles, events, and jobs in the terms below, never an HTTP status or JSON.

- **The connection check** (G2) is one read of Bazarr's status, not retried. It yields Bazarr's version and start time, or the cause.
- **A read** succeeds only with HTTP 200 and a body that decodes. Anything else is a failed read: it is retried until it succeeds, or until 5 minutes have passed since the first failed or unanswered request after the last success, which also cuts short the attempt in flight; then the client reports Bazarr *unreachable*, with the last failure as the cause, and the command stops (G3). A read that needed a retry says so.
- **A change** (submit a sync, apply a fix, delete) is sent once and returns exactly one of: *accepted* (HTTP 204); *refused*, with the response body, reduced to one line, as Bazarr's reason (any other status except 502, 503, and 504); *unanswered* (no complete response within the time limit, or 502, 503, or 504, which a proxy sends when Bazarr did not answer it). After an unanswered change the client itself reads Bazarr's start time, retried as any read, before it returns. An operation cut short by a stop returns *stopped*.
- **A subtitle** is an entry of a video's `subtitles` whose `path` is not null. It carries its type, the Radarr movie ID or the Sonarr series and episode IDs, the movie or series title, season and episode numbers, `language` (Bazarr's `code2`), `hi`, `forced`, and `path`, the path byte for byte as Bazarr gives it. Path and video ID together identify it.
- **A sync job of a subtitle** is a job in Bazarr's queue whose `status` is `pending` or `running` and whose `job_name` is `Syncing <path>` or `Synced <path>` (S1).
- **The events of a subtitle** are the events of its video whose `subtitles_path` equals its `path`, newest first as Bazarr returns them. An event's identity is its `action` and `parsed_timestamp` together; entries with the same identity are one event (S4). The kinds are `action` 1 download, 2 manual download, 3 upgrade, 4 upload, 5 sync, 6 translation; every other action is ignored. A subtitle is currently synced when its newest event of these kinds is a sync.
- **Bazarr has restarted** when its `start_time` differs from the one read earlier (S3).

Bazarr's calls, all under `<address>/api`:

| Purpose | Call |
|---|---|
| status | `GET system/status`: `bazarr_version`, `start_time` |
| library | `GET movies`; `GET series`; `GET episodes` with `seriesid[]` |
| one video | `GET movies` with `radarrid[]`; `GET episodes` with `episodeid[]` |
| events | `GET movies/history` with `radarrid`; `GET episodes/history` with `episodeid` |
| queue | `GET system/jobs` |
| sync, fix | `PATCH subtitles` with `action`, `language`, `path`, `type` (`movie` or `episode`), `id` (movie or episode ID), `forced` and `hi` as `True` or `False`; a sync adds `reference`, `max_offset_seconds`, `no_fix_framerate=True`, `gss=True`, each only when its option is given |
| delete | `DELETE movies/subtitles` with `radarrid`, or `DELETE episodes/subtitles` with `seriesid` and `episodeid`; both with `language`, `forced`, `hi`, `path` |

`action` is `sync`, or for a fix: `remove_HI`, `remove_tags`, `OCR_fixes`, `common`, `fix_uppercase`, `emoji`, in F10's order. `reference` is `a:N` or `s:N` as given, or for `lang:CODE` the reference subtitle's `path`, which `sync` resolves.

### Selection

A function of the request and the library, which is every movie and series Bazarr lists, each with the subtitles of its videos. It has no access to Bazarr and returns every selected subtitle in the order of P2, or the error of G8. The whole selection exists before any change is sent.

### Commands

`list`, `sync`, `fix`, and `delete` each take the selection and drive the client, the failure store, and the reporter. `sync` alone submits syncs and owns the slots of G17. No command calls another.

### Failure store

The only part that opens a file. It loads the records of one address, answers with a path's record (its event identities, which may be none) or that it has none, forgets a record `sync` found lapsed, and records a failure. A load or a write that fails makes it *unusable*, with the cause: from then on it answers that no path has a record, and `sync` judges again every subtitle this run skipped for an earlier failure, so the run is as if none were recorded (G23).

### Reporter

The only part that writes standard output or standard error or reads standard input. It owns G5, the progress display, the confirmation prompt, and the totals. It decides "is a terminal" by the operating system's terminal query, never by file type: `/dev/null` is a character device and not a terminal.

## State

Bazarr is the record of truth for the library, the history, and the queue; nothing read from it is written to disk or kept for a later run. A run's memory holds the selection and, for each sync it follows, the baseline read before its submission: Bazarr's start time and the identities of the subtitle's events of kinds 1 to 6.

The state file is UTF-8 JSON, one object with two members: `"bazarrbulksync_state": 1`, and `"servers"`, an object keyed by address (as given, trailing slashes removed) whose values are objects keyed by subtitle path, each value the array of event identities of a recorded failure's baseline, written `"<action> <parsed_timestamp>"` and compared as opaque strings. A file of any other shape is not a state file.

- **Recording a failure** writes the whole new content to the temporary file, which is the state file's path with `.tmp` appended, closes it, and renames it over the state file, creating missing parent directories first. The record takes effect at the rename. A failure before the rename leaves the state file as it was, and the store becomes unusable.
- **Loading**: no state file means no records. When the state file is missing or is not a complete state, and the temporary file is a complete state, the temporary file is the state, and the next write first renames it into place. Otherwise a state file that cannot be read or is not a state file makes the store unusable: the run says so once, never writes that file, and treats every subtitle as having no record (G23).
- **A record `sync` found lapsed** in this run (G21) is left out of the next write. `--dry-run` loads and never writes.
- **A submitted sync** takes effect when Bazarr queues its job. When the tool dies, the job stays with Bazarr and the next run reads its result from Bazarr's history; a failure not yet recorded costs one repeated attempt.

## How the guarantees hold

### G2. The connection check

The client hands out reads and changes only after a successful check, so no command can skip it. A missing address or key fails the check without a request. HTTP 401 is a rejected key. The version is compared as numeric major, minor, and patch; a pre-release of 1.6.0, or a version that does not parse, is older than 1.6.0.

### G3, G4. Stopping

A stop is Ctrl-C, SIGTERM, or the client reporting Bazarr unreachable. Every request is bound to the stop: the one in flight is abandoned and none is sent afterwards. This holds by construction, since the client is the only sender (R1). The command then prints its totals and exits without another request. A stop also ends a pending confirmation prompt, as an answer other than yes. A `fix` or `delete` request abandoned by the stop is reported `unconfirmed`; a sync submitted, or being submitted, gets no outcome and no record.

### G15, G16, G18. Judged immediately before submission

Each time a slot is free, `sync` takes the next subtitle and reads, in this order, the video, the queue, the video's events, and Bazarr's start time, starting again when a read needed a retry, then decides from those reads alone: wait while the queue shows a sync job of it (G18), skip it under G15, or submit it at once. The queue is read before the events so that a sync Bazarr finished in between is in the baseline and is never taken for the run's own. The start time is read last, so that it is the last read before the submission that G20 names. A subtitle waiting under G18 holds no slot and is judged again from the first read. The video as just read replaces the subtitle's attributes; a selected subtitle it no longer shows keeps its selected attributes, is judged the same way, and when G15 does not skip it is submitted (G16), Bazarr's answer deciding its outcome. `--dry-run` makes the same reads, does not wait under G18, and reports what G15 decides from them.

### G17. Slots

A slot is taken before the request is sent and released only when the subtitle's outcome is reported. `sync` is the only caller of the submit operation and counts slots in its one loop, so the bound holds by construction. Limit: a job Bazarr is moving from its pending to its running queue is shown in neither, and the ending test below is sound only while that move completes within five seconds (S2).

### G19, G20, G21. The outcome of a submitted sync

- A refused submission is `failed` at once and is not recorded.
- Otherwise `sync` follows the subtitle. Its sync has **ended** when two successive queue reads, the second sent at least five seconds after the first was answered, both show no sync job of it. `sync` then reads the events, then the start time, and reports the first that applies: `synced` when a sync event's identity is not in the baseline; `unconfirmed` when Bazarr has restarted; `failed`, recorded with the baseline, when the submission was accepted; `unconfirmed` when it was unanswered.
- Every doubt ends in `unconfirmed`, which records nothing.
- A recorded failure has lapsed when the subtitle's events of kinds 1 to 6 include an identity that its record does not hold.
- Limit: two events of one kind for one path within one second are one event, because Bazarr's history shows nothing that tells them apart (S4); the tool knows only what Bazarr's API shows (`Product`, accepted limits).

### G22, G23. The state file

Kept by the write and load rules of `State`, by construction: only the failure store opens files (R1), and it replaces the state file only by rename (S5). Limit: nothing is flushed to the device; E4 excludes power loss and device failure. On Windows the entry waits on T3.

### G24, G27. One request, never a second

Kept by construction: the client's change has no retry (R3).

## Substrate assumptions

The arrangements rely on these assumptions, and none narrows a guarantee inside the product's operating envelope. S1 to S4 describe Bazarr 1.6.0 to 1.6.2, which E1 grants.

- **S1.** `PATCH subtitles` with `action=sync` queues a job and answers 204 before the sync runs, returning no job identifier. The job is named `Syncing <path>` with the path as sent, and is renamed `Synced <path>` when the sync has run and before the job leaves the running queue. Syncs Bazarr starts itself carry the same names. (https://github.com/morpheus65535/bazarr at v1.6.0 and v1.6.2: `bazarr/api/subtitles/subtitles.py`, `bazarr/subtitles/sync.py`)
- **S2.** A sync writes its history event before its job leaves the running queue, and a sync that does not replace the file writes none. Bazarr takes a job out of its pending queue and appends it to its running queue in one thread with no blocking call between the two steps, and a queue read in between shows the job in neither; the arrangements rely on that move, a few statements long, completing within five seconds. (same tags: `bazarr/subtitles/tools/subsyncer.py`, `bazarr/app/jobs_queue.py`)
- **S3.** The job queue lives in memory, and `start_time` is set once when the process starts: a restart empties the queue and changes `start_time`. (same tags: `bazarr/app/jobs_queue.py`, `bazarr/init.py`)
- **S4.** The history calls expose no event identifier. `parsed_timestamp` is the stored event time formatted to the second, and Bazarr sets no locale, so it is the same on every read. Bazarr 1.6.0 repeats each event once per external subtitle of the video; 1.6.2 does not. (same tags: `bazarr/api/movies/history.py`, `bazarr/api/episodes/history.py`)
- **S5.** On Linux and macOS a rename over an existing file replaces it atomically: another open finds the old file or the new one. On Windows, Go's rename is `MoveFileEx` with `MOVEFILE_REPLACE_EXISTING`, which neither Go nor Microsoft documents as atomic (T3). (rename(2), Linux man-pages; `os.Rename` and `internal/syscall/windows/syscall_windows.go`, Go 1.27.0; https://learn.microsoft.com/en-us/windows/win32/api/winbase/nf-winbase-movefileexw)
- **S6.** With cgo disabled, Go 1.27 builds an executable for linux, darwin, and windows on amd64 and arm64 from one Linux machine; the Linux executables are statically linked. (`go tool dist list` and a build of all six, Go 1.27.0)
- **S7.** Go verifies certificates against the operating system's trust: on Linux the system's CA bundle files and certificate directories, trusting none when there are none; on macOS and Windows the platform verifier. On all three, `SSL_CERT_FILE` or `SSL_CERT_DIR`, when the user sets one, replaces that trust. (`crypto/x509/root.go`, `root_linux.go`, and `doc/godebug.md` `x509sslcertoverrideplatform`, Go 1.27.0)
- **S8.** A Go program that registers for the interrupt and termination signals receives Ctrl-C on all three systems, and receives the SIGTERM of a container stop when it is process 1. (`os/signal` documentation, Go 1.27.0; a `docker stop` of a scratch image, Docker 29.7.2)

## Standing rules

- **R1.** Only the Bazarr client opens a network connection; only the failure store opens a file; only the reporter touches standard input, output, and error.
- **R2.** The API key travels only in the `X-API-KEY` header. It appears in no URL, no output line, and no file.
- **R3.** The client never repeats a change, and has no change other than the sync, the six fixes of F10, and the delete.
- **R4.** The queue is read before the events wherever both decide one outcome.
- **R5.** Nothing uses cgo.

## Product decisions settled here

- **P1.** (L2) The executables' default state file is `bazarrbulksync/state.json` under the user's configuration directory: `$XDG_CONFIG_HOME` or `~/.config` on Linux, `~/Library/Application Support` on macOS, `%AppData%` on Windows. `sync --help` prints the resolved path. When it cannot be resolved, the state file cannot be read or written (G23). The format is `State`'s.
- **P2.** (L4) Subtitles are listed and processed in one order: movies before episodes; movies and series as Bazarr lists them, by title; episodes by season, then episode number; the subtitles of one video by path in byte order.
- **P3.** (L3) A result line opens with its outcome word: `synced`, `applied`, `deleted`, `failed`, `unconfirmed`, or under `--dry-run` `would sync`, `would apply`, `would delete`, `would skip`; then the subtitle as `list` prints it; then the reason, when there is one. The skip reasons are `forced`, `currently synced`, `earlier failure`, `reference language`, `no reference subtitle`, and `replaces non-HI subtitle`. The prompt takes `y` or `yes` in any case as yes. Without `--dry-run`, a run prints no line for a subtitle it skips. Either way it counts the subtitle under one reason, the first that applies in the order above. Progress is the count of done out of selected, redrawn in place only when standard error is a terminal. All other wording is the implementer's.

## Left open

- **T1.** The implementer sets the request time limits by measuring reads and changes against a real Bazarr library. Each request has one, and none exceeds G3's 5 minutes. Until measured, every limit is 60 seconds.
- **T2.** The project owner decides how releases are built and published (F2) when the first release is cut; publishing needs credentials no build session holds. Until then the build produces the six executables and the image locally, as `Foundations` says.
- **T3.** Whether a kill during the replacing rename on Windows always leaves a complete state in the state file or the temporary file is unverified. Whoever builds the failure store settles it on a Windows machine with NTFS by killing the tool during the rename, repeatedly, and loading afterwards. G22 on Windows waits on it; if a kill can leave neither file complete, the conflict goes back to this document.
