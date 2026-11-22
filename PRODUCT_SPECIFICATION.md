# bazarrbulksync — Product Specification

## Authority

This document outranks every other document, decision, and line of code in this project. `TECHNICAL_SPECIFICATION.md` is written from it and ranks second. `Fixed decisions` and `Non-goals` hold everywhere. `Guarantees` hold for every use inside `Operating envelope` and bind exactly as strongly there; outside the envelope nothing is promised. Unassigned implementation choices are deliberately open for downstream work at its own level. An explicitly pending decision remains with its named owner and is not a delegated choice. Work that finds a line here unworkable stops the dependent work and reports the conflict and a proposed amendment; independent work continues. It does not work around the line, narrow it, or change the envelope to make a failure disappear. Labels (`E1`, `F1`, `G1`, `N1`, `L1`) are permanent and are how every downstream document cites this one.

## Product

bazarrbulksync is a command-line tool for people who run Bazarr and need to act on the subtitles already in their library across the whole library at once; Bazarr itself acts on one movie or one series at a time. Its main job is to have Bazarr synchronize every subtitle that is not yet synchronized, over hours or days, unattended. It also applies Bazarr's text clean-ups in bulk, deletes subtitles in bulk, and lists them.

Two terms are used throughout:

- An **external subtitle** is a subtitle file that Bazarr has indexed for a movie or an episode. Tracks embedded in a video file are never selected.
- An external subtitle is **currently synced** when Bazarr's history for its file path holds a sync event newer than every download (automatic or manual), upgrade, upload, and translation event for that path.

What matters most:

- A `sync` run can stop at any moment, and running the same command again continues it: without `--resync`, a subtitle whose sync from an earlier run Bazarr recorded is not synced again until Bazarr's history shows it replaced.
- A subtitle reported as `synced` is one whose sync Bazarr recorded.
- Bazarr is handed syncs only as fast as `--concurrency` allows, one at a time unless the user asks for more.

Accepted limits:

- Every change is made by Bazarr to the subtitle files themselves. The tool keeps no backup and cannot undo a sync, a fix, or a deletion.
- The tool knows only what Bazarr's API shows. A subtitle changed on disk without Bazarr recording it keeps the status Bazarr's history gives it.
- Bazarr's history gives event times as local times to the second, so two events of the same kind for one subtitle with the same such time count as one.
- Bazarr records a sync it performs while downloading, upgrading, or uploading a subtitle before it records the download, upgrade, or upload itself, so such a subtitle is not currently synced and `sync` has it synchronized again.
- A sync already handed to Bazarr when a run stops is left to Bazarr, which finishes it unless it restarts or someone removes it first; the tool does not cancel it.
- The memory of failed syncs is one local file. Losing it costs repeated attempts, nothing else.
- With Bazarr 1.6.0 to 1.6.2 set to keep subtitles in a custom folder, Bazarr's `remove-hi` deletes a hearing-impaired subtitle that has no non-hearing-impaired counterpart and then fails; `fix remove-hi` reports it `failed` and the file is gone.

## In use

```
export BAZARR_URL=http://192.168.1.10:6767
export BAZARR_API_KEY=0123456789abcdef
bazarrbulksync list --language en
bazarrbulksync sync --dry-run
bazarrbulksync sync
```

`list` prints the English external subtitles of the whole library and their totals (G10). `sync --dry-run` prints, for every external subtitle, whether a sync would be requested or why not, and changes nothing (G13). `sync` prints how many subtitles are selected and asks for confirmation (G12); it then hands them to Bazarr one at a time (G17), printing each outcome as Bazarr finishes it (G14, G19), until every subtitle has been handled. The user presses Ctrl-C at any point (G4) and later runs `bazarrbulksync sync` again: subtitles currently synced are skipped, and subtitles reported `failed` after Bazarr ran their sync are skipped too (G15, G21).

Run every night from a scheduler, the same command catches up on whatever Bazarr downloaded since:

```
docker run --rm -e BAZARR_URL -e BAZARR_API_KEY -v bazarrbulksync:/data wayhowma/bazarrbulksync sync --yes
```

Narrower runs use the same selection options on every command (F6):

```
bazarrbulksync sync --title "breaking bad" --season 2 --resync --max-offset-seconds 120
bazarrbulksync sync --language ar --reference lang:en
bazarrbulksync fix remove-tags --movies
bazarrbulksync delete --language es --series-id 12
```

The most common failure is a wrong address or API key: the command names the address and the cause, and changes nothing (G2).

## Non-goals

- **N1.** No action other than the four commands of F5: no translation, subtitle search or download, upload, extraction of embedded tracks, blacklisting, or change to Bazarr's profiles or settings.
- **N2.** No backup, undo, or restore of any subtitle.
- **N3.** No subtitle modification of Bazarr other than the fixes of F10: no time shift, frame-rate change, color, reverse RTL, or two-point alignment.
- **N4.** No scheduler, daemon, watch mode, web interface, or full-screen interface: each invocation performs one run and exits.
- **N5.** No synchronization engine or subtitle processing of its own.
- **N6.** No configuration file, and no reading of Bazarr's settings.
- **N7.** No record of work done other than the failed syncs of G21, and no log-file option.
- **N8.** No support for Bazarr older than 1.6.0 or for forks of Bazarr, and no compatibility with the commands or configuration of bazarrbulksync 0.3.0 and earlier.
- **N9.** No selection by tag, monitored status, language profile, audio language, or file format.

## Operating envelope

Behavior outside these assumptions is not promised.

- **E1.** The server at the given address is upstream Bazarr, not a fork. When it is version 1.6.0 or newer, its HTTP API behaves, for the calls the tool makes, as Bazarr 1.6 does. No login other than the API key stands in front of it, and over HTTPS its certificate is trusted by the machine running the tool. A server that is older, does not answer, or rejects the key is admitted.
- **E2.** One bazarrbulksync run at a time uses a given Bazarr server and a given state file.
- **E3.** During a run, nobody removes or cancels that run's syncs in Bazarr's job queue. Bazarr's own tasks, its web interface, and restarts of Bazarr are admitted. Bazarr shows a sync it starts as running within five seconds of no longer showing it as queued.
- **E4.** The state file is on a local filesystem and nothing else writes it. Interruption of the tool at any moment is admitted; power loss and device failure are not.

## Fixed decisions

- **F1.** The product and its command are named `bazarrbulksync`.
- **F2.** It is released as one self-contained executable each for Linux, macOS, and Windows on x86-64 and arm64, attached to the GitHub releases of the project's repository, and as the container image `wayhowma/bazarrbulksync` for linux/amd64 and linux/arm64 whose entry point is the command. Running it requires no interpreter, runtime, or other installed software. It is not published as a Python package.
- **F3.** Its only contact with anything outside itself, apart from the name lookup that reaches the address, is Bazarr's HTTP API, authenticated with Bazarr's API key. The only files it reads or writes, apart from what the operating system consults to reach the address and trust its certificate, are the state file of F9 and a temporary file beside it.
- **F4.** The connection is given by the environment variables `BAZARR_URL` (the address including any base path, for example `https://host/bazarr`) and `BAZARR_API_KEY`; the options `--url` and `--api-key` override them.
- **F5.** The commands are `list`, `sync`, `fix`, and `delete`. `bazarrbulksync --version` prints the version; `--help` on the program or on a command prints its usage.
- **F6.** Every command accepts these selection options:
  - `--movies`, `--series`: only movie subtitles, or only episode subtitles;
  - `--title TEXT` (repeatable): movies and series whose title contains TEXT, ignoring case;
  - `--movie-id N`, `--series-id N` (repeatable): the movie or series with that Radarr or Sonarr ID, as Bazarr shows it;
  - `--season N` (repeatable): only episode subtitles of that season;
  - `--language CODE` (repeatable): subtitles in that language, by the language code `list` prints, ignoring case;
  - `--hi`, `--no-hi`: only, or no, hearing-impaired subtitles;
  - `--forced`, `--no-forced`: only, or no, forced subtitles.
- **F7.** `sync`, `fix`, and `delete` accept `--yes` and `--dry-run`. `list` accepts `--json`, whose objects have exactly the fields `type` (`"movie"` or `"episode"`), `title` (the movie or series title), `movie_id`, `series_id`, `episode_id`, `season`, `episode`, `language`, `hi`, `forced`, and `path`; a field that does not apply to the subtitle's type is `null`.
- **F8.** `sync` accepts `--resync`, `--retry-failed`, `--concurrency N` (a whole number of at least 1; default 1), `--state-file PATH`, and the sync options `--max-offset-seconds N` (a whole number of at least 1), `--no-fix-framerate`, `--gss`, and `--reference REF`. REF is `a:N` or `s:N` with N from 0 to 9 (audio track N or embedded subtitle track N of each video, as Bazarr numbers them) or `lang:CODE` (the same video's external, non-forced subtitle in language CODE; the one that is not hearing-impaired when both exist, and among several such, the one whose path comes first in byte order).
- **F9.** The state file holds only what G21 needs. In the container image its default path is `/data/state.json`.
- **F10.** `fix` takes exactly one fix name: `remove-hi`, `remove-tags`, `ocr-fixes`, `common-fixes`, `fix-uppercase`, or `remove-emoji`. Each is Bazarr's subtitle modification of that meaning, applied by Bazarr.

## Guarantees

### Every command

- **G1.** Run with no command and neither `--help` nor `--version`, or with an unknown command or option, a malformed value, or both options of a pair (`--movies` with `--series`, `--hi` with `--no-hi`, `--forced` with `--no-forced`), the program prints a usage error on standard error, exits nonzero, and contacts nothing.
- **G2.** Before reading or changing anything in Bazarr, a command checks the connection. A missing address or API key, a server that does not answer, a rejected API key, or a Bazarr that does not report a version of 1.6.0 or newer is named on standard error, with the address when one was given; the command exits nonzero and changes nothing.
- **G3.** When Bazarr stops answering, or answers a read with an error, during a run, the command keeps retrying for 5 minutes. If Bazarr has not answered by then, the command stops as G4 describes.
- **G4.** On Ctrl-C, or on SIGTERM, which a container stop sends by default, a command sends no further changing request and exits nonzero without waiting for syncs still in Bazarr; `sync`, `fix`, and `delete` first print the totals of G14 for what finished. Syncs still in Bazarr are neither reported as `failed` nor recorded in the state file.
- **G5.** Result lines and totals go to standard output; prompts, progress, and errors go to standard error. When standard output is not a terminal it carries plain lines only, with no color and no cursor movement.
- **G6.** A command exits nonzero when one of G1 to G4, G8, G12, or G26 stops it, or when it reports any subtitle as `failed` or `unconfirmed`; otherwise it exits zero.

### Selection

- **G7.** A subtitle is selected when it satisfies every kind of selection option given. Among `--title`, `--movie-id`, and `--series-id`, matching any one given value suffices; a repeated `--season` or `--language` matches any of its values. `--season` excludes movie subtitles. With no selection option, every external subtitle is selected.
- **G8.** A `--title` value that matches no movie or series, or an ID Bazarr does not have, is named on standard error; the command exits nonzero before changing anything.
- **G9.** When none of G1 to G4, G8, and G26 stops the command and the selection matches no external subtitle, the command says so on standard error, changes nothing, and exits zero.

### `list`

- **G10.** `list` prints one line per selected subtitle showing the movie or series title, the season and episode numbers of an episode, the language code, whether it is hearing-impaired or forced, the file path, and the movie or series ID; then the total count and the count per language. It changes nothing.
- **G11.** `list --json` prints on standard output exactly one JSON object per selected subtitle, one per line, with the fields of F7, and no totals.

### `sync`, `fix`, and `delete`

- **G12.** When at least one subtitle is selected and neither `--yes` nor `--dry-run` is given, the command needs confirmation. With standard input a terminal, it prints the number of selected subtitles and asks; any answer other than yes changes nothing and exits nonzero. With standard input not a terminal, it says on standard error that `--yes` is required, exits nonzero, and changes nothing.
- **G13.** With `--dry-run`, the command prints for each selected subtitle whether it would act on it or skip it and why, then the totals; it sends no changing request and does not write the state file.
- **G14.** A run prints one line per subtitle it acts on, naming the subtitle and its outcome, with the reason Bazarr gave when it refused a request; it shows on standard error how many subtitles are done out of those selected; and it ends with the count per outcome and per skip reason.

### `sync`

- **G15.** `sync` never submits, judging by Bazarr as read immediately before the submission: a forced subtitle (Bazarr does not synchronize forced subtitles); a currently synced subtitle, unless `--resync` is given; a subtitle with a recorded failure (G21), unless `--retry-failed` is given; and, with `--reference lang:CODE`, a subtitle in language CODE or one whose video has no reference subtitle of F8.
- **G16.** Every other selected subtitle is submitted to Bazarr, once G17 and G18 allow, as one sync request carrying the sync options given. A sync option not given is left out of the request.
- **G17.** At most `--concurrency` of the run's sync requests are queued or running in Bazarr at any moment; a request Bazarr never answered stops counting once its subtitle is reported `unconfirmed` (G20).
- **G18.** A subtitle is not submitted when Bazarr, read immediately before the submission, shows a sync of it queued or running; `sync` waits until Bazarr no longer shows that sync, then handles the subtitle under G15 and G16.
- **G19.** `sync` reports a subtitle as `synced` only once Bazarr's history shows a sync event for its path that was not there when read immediately before the submission. It reports `failed` when Bazarr refuses the request, or when Bazarr accepted the request, has not restarted since, no longer shows the sync queued or running, and shows no such event in its history.
- **G20.** When neither outcome of G19 can be established, because Bazarr restarted after it was last read before the submission, or never answered the request and shows the sync neither queued nor running, the subtitle is reported as `unconfirmed`: nothing is recorded in the state file, and the run continues with the next subtitle. When Bazarr stops answering and answers again within G3 without having restarted, `sync` goes on waiting for the syncs Bazarr still shows.
- **G21.** When Bazarr ran a sync and it ended without a sync event (the second case of G19), `sync` records the failure in the state file under the Bazarr address. A request Bazarr refused is not recorded. A recorded failure lasts until Bazarr's history shows, for that path, a download (automatic or manual), upgrade, upload, translation, or sync event it did not show when read immediately before the failed sync was submitted.
- **G22.** After `sync` is killed at any moment, every failure recorded before the kill is read by the next run; the failure being recorded at the kill is either absent or complete.
- **G23.** When the state file cannot be read or written, or is not a state file, `sync` says so on standard error, leaves that file untouched, and from then on runs as if no failure were recorded. A state file that does not exist yet is created when the first failure is recorded.

### `fix`

- **G24.** `fix` asks Bazarr to apply the named fix to each selected subtitle that G25 does not skip, one request at a time. The outcome is `applied` when Bazarr accepts the request, `failed` when it refuses, and `unconfirmed`, without a second request, when it never answers. `fix` keeps no record: run again, it asks again for every such subtitle.
- **G25.** `fix remove-hi` skips a subtitle whose video has another external subtitle of the same language that is neither hearing-impaired nor forced, or that follows, in the run's order, a selected subtitle of the same video and language that it does not skip, because Bazarr writes the result in that subtitle's place.

### `delete`

- **G26.** `delete` given no selection option says on standard error that one is required, exits nonzero, and changes nothing, without contacting Bazarr.
- **G27.** `delete` asks Bazarr to delete each selected subtitle, one request at a time. The outcome is `deleted` when Bazarr accepts the request, `failed` when it refuses, and `unconfirmed`, without a second request, when it never answers.

## Left open

- **L1.** The technical writer chooses the implementation language and libraries, how the tool reads the library and history from Bazarr, and how it learns that a queued sync has ended, under G17 to G20 and E1.
- **L2.** The technical writer chooses the state file's format and its default path for the executables, under G21 to G23: the path is per user, the same on every run, and printed by `sync --help`.
- **L3.** The technical writer chooses the wording and layout of human-readable output, the progress display, and the confirmation prompt, under G5, G10, and G12 to G14.
- **L4.** The technical writer chooses the order in which selected subtitles are processed.

## Done when

- On Linux, macOS, and Windows machines with no interpreter or runtime installed, the downloaded executable runs `bazarrbulksync list` against a Bazarr 1.6 server and prints every external subtitle the server's web interface shows. `docker run -e BAZARR_URL -e BAZARR_API_KEY wayhowma/bazarrbulksync list --json` prints one JSON object per subtitle with exactly the fields of F7, each `path` equal to the path the web interface shows.
- Against a Bazarr holding at least six external subtitles, none forced, currently synced, or with a recorded failure, across movies and episodes, `bazarrbulksync sync --yes` is stopped with Ctrl-C after two are reported `synced` and then run again to its end. Bazarr's job queue never held more than one sync from the same run; Bazarr's history holds exactly one new sync event for each of the six; and a third run reports all six skipped as currently synced and submits nothing. Repeated on other subtitles with `--concurrency 2`, the queue at some moment holds two syncs from one run and never three.
- A subtitle whose sync Bazarr accepts but cannot complete (for example, one requested against an audio track the video lacks) is reported `failed`, never `synced`, and the run exits nonzero. The next plain `sync` skips it, naming the earlier failure, and exits zero. Starting each time with that failure recorded, `sync --retry-failed` submits it again; so does a plain `sync` after a new subtitle is uploaded to the same path through Bazarr; and so does a plain `sync`, with the same state file, against a second Bazarr server holding a subtitle at the same path that is not currently synced.
- Bazarr is restarted while a sync from a running `bazarrbulksync sync --yes` is still pending in its queue and comes back within 5 minutes: that subtitle is reported `unconfirmed`, the run goes on to the remaining subtitles, the state file holds no failure for it, and the next run submits it. When instead the network between the tool and Bazarr is cut for one minute with a sync in the queue, the tool submits nothing meanwhile and reports that subtitle `synced` once Bazarr records its sync.
- `bazarrbulksync delete`, `bazarrbulksync delete --yes`, and `bazarrbulksync delete --language es` with standard input not a terminal each exit nonzero and leave `list` output unchanged. `delete --language es --dry-run` prints exactly the subtitles `list --language es` prints and deletes none. `delete --language es --yes` leaves `list --language es` empty and the `list` output for every other language identical to before.
- `bazarrbulksync fix remove-tags --title <one title> --yes`, for a title whose subtitle files are all SRT, leaves that title's subtitle files without style tags and every other title's subtitle files byte-for-byte unchanged. Each other fix name of F10, run on an SRT subtitle containing what it removes or corrects (for `remove-hi`, one G25 does not skip), changes that file accordingly.
