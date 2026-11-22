package commands_test

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/fakebazarr"
	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/fakeclock"
)

// Paths of syncLibrary's subtitles.
const (
	sintelEn     = "/m/sintel/s.en.srt"
	sintelEnHI   = "/m/sintel/s.en.hi.srt"
	sintelEs     = "/m/sintel/s.es.srt"
	sintelForced = "/m/sintel/s.fr.forced.srt"
	tearsEn      = "/m/tears/t.en.forced.srt"
	tearsEs      = "/m/tears/t.es.srt"
	towersDouble = "/tv/f/S01E01-E02.en.srt" // one file, two episodes
	towersDe     = "/tv/f/S01E03.de.srt"
)

// syncLibrary has a movie with plain, HI, and forced subtitles, a movie
// whose only English subtitle is forced, and a series with a two-episode
// file.
func syncLibrary() fakebazarr.Library {
	return fakebazarr.Library{
		Movies: []fakebazarr.Movie{
			{RadarrID: 7, Title: "Sintel", Subtitles: []fakebazarr.Subtitle{
				{Path: sintelEn, Language: "en"}, {Path: sintelEnHI, Language: "en", HI: true},
				{Path: sintelEs, Language: "es"}, {Path: sintelForced, Language: "fr", Forced: true},
				{Embedded: true, Language: "de"},
			}},
			{RadarrID: 8, Title: "Tears of Steel", Subtitles: []fakebazarr.Subtitle{
				{Path: tearsEn, Language: "en", Forced: true}, {Path: tearsEs, Language: "es"},
			}},
		},
		Series: []fakebazarr.Series{
			{SonarrSeriesID: 3, Title: "Fawlty Towers", Episodes: []fakebazarr.Episode{
				{SonarrEpisodeID: 103, Season: 1, Episode: 3, Subtitles: []fakebazarr.Subtitle{{Path: towersDe, Language: "de"}}},
				{SonarrEpisodeID: 102, Season: 1, Episode: 2, Subtitles: []fakebazarr.Subtitle{{Path: towersDouble, Language: "en"}}},
				{SonarrEpisodeID: 101, Season: 1, Episode: 1, Subtitles: []fakebazarr.Subtitle{{Path: towersDouble, Language: "en"}}},
			}},
		},
	}
}

// dryRun runs `sync --dry-run` against srv with the state file at state
// and the arguments args.
func dryRun(t *testing.T, srv *fakebazarr.Server, state string, args ...string) outcome {
	t.Helper()
	return runAgainst(t, srv, append([]string{"sync", "--dry-run", "--state-file", state}, args...)...)
}

// verdict returns the reason in path's result line, or "sync" for
// `would sync`; it fails the test when the line is missing or another word.
func verdict(t *testing.T, stdout, path string) string {
	t.Helper()
	line := lineOf(t, stdout, path)
	switch {
	case strings.HasPrefix(line, "would sync  "):
		return "sync"
	case strings.HasPrefix(line, "would skip  "):
		_, reason, _ := strings.Cut(line, path+"  (")
		return strings.TrimSuffix(reason, ")")
	}
	t.Errorf("line %q opens with neither would sync nor would skip", line)
	return ""
}

func expectVerdicts(t *testing.T, name string, o outcome, want map[string]string) {
	t.Helper()
	if o.code != 0 {
		t.Errorf("%s: exit %d, stderr %q", name, o.code, o.stderr)
	}
	for path, w := range want {
		if got := verdict(t, o.stdout, path); got != w {
			t.Errorf("%s: %s judged %q, want %q\n%s", name, path, got, w, o.stdout)
		}
	}
}

// calls returns the calls the fake received, without the "/api/" prefix.
func calls(srv *fakebazarr.Server) []string {
	var got []string
	for _, r := range srv.Requests() {
		got = append(got, strings.TrimPrefix(r.Path, "/api/"))
	}
	return got
}

func noChange(t *testing.T, name string, srv *fakebazarr.Server) {
	t.Helper()
	for _, r := range srv.Requests() {
		if r.Method != http.MethodGet {
			t.Errorf("%s: the fake received %s %s, want no changing request (G13)", name, r.Method, r.Path)
		}
	}
}

// TestSyncDryRunEverySubtitle: with no selection option every external
// subtitle is reported (G7, G13), each once, nobody is asked, and nothing is
// sent or written, with or without --yes.
func TestSyncDryRunEverySubtitle(t *testing.T) {
	for _, extra := range [][]string{nil, {"--yes"}} {
		srv := fakebazarr.New(t, fakebazarr.Config{Library: syncLibrary()})
		dir := t.TempDir()
		state := filepath.Join(dir, "missing", "state.json")
		args := append([]string{"sync", "--dry-run", "--state-file", state}, extra...)
		r := startWith(context.Background(), fakeclock.New(t), envFor(srv), typed(""), args...)
		o := await(t, r.done)
		expectVerdicts(t, strings.Join(args, " "), o, map[string]string{
			sintelEn: "sync", sintelEnHI: "sync", sintelEs: "sync", sintelForced: "forced",
			tearsEn: "forced", tearsEs: "sync", towersDe: "sync",
		})
		if lines := resultLines(o.stdout); len(lines) != 9 || strings.Count(o.stdout, towersDouble) != 2 {
			t.Errorf("%q: %d lines, want one per external subtitle (9):\n%s", extra, len(lines), o.stdout)
		}
		if !strings.Contains(o.stdout, "Total: 9 of 9 selected subtitles") || !strings.Contains(o.stdout, "would sync 7") ||
			!strings.Contains(o.stdout, "skipped (forced) 2") {
			t.Errorf("%q: totals missing:\n%s", extra, o.stdout)
		}
		if strings.Contains(o.stderr, "?") || strings.Contains(o.stderr, "Sync them") {
			t.Errorf("%q: standard error %q; nobody is asked under --dry-run", extra, o.stderr)
		}
		noChange(t, strings.Join(args, " "), srv)
		if entries, _ := os.ReadDir(dir); len(entries) != 0 {
			t.Errorf("%q: the dry run created %v (G13)", extra, entries)
		}
	}
}

// TestSyncDryRunReadsInOrder: for each selected subtitle, the video, the
// queue, the events (the histories of its video and the other videos with a
// subtitle at its path), and the start time, in that order (G15, G18, R4).
func TestSyncDryRunReadsInOrder(t *testing.T) {
	srv := fakebazarr.New(t, fakebazarr.Config{Library: syncLibrary()})
	o := dryRun(t, srv, filepath.Join(t.TempDir(), "s.json"), "--title", "tears", "--title", "fawlty", "--language", "es", "--language", "en")
	if o.code != 0 {
		t.Fatalf("exit %d, stderr %q", o.code, o.stderr)
	}
	got := calls(srv)
	first := slices.Index(got, "series")
	if first < 0 || len(got) < first+2 {
		t.Fatalf("calls %q", got)
	}
	got = got[first+2:] // after the status, movies, series, and episodes of the library read
	movie := []string{"movies", "system/jobs", "movies/history", "system/status"}
	episode := []string{"episodes", "system/jobs", "episodes/history", "episodes/history", "system/status"}
	want := slices.Concat(movie, movie, episode, episode)
	if !slices.Equal(got, want) {
		t.Errorf("reads after the library\n  %q\nwant\n  %q", got, want)
	}
	var radarrIDs []string
	for _, r := range srv.Requests() {
		if r.Path == "/api/movies/history" {
			radarrIDs = append(radarrIDs, r.Params().Get("radarrid"))
		}
	}
	if !slices.Equal(radarrIDs, []string{"8", "8"}) {
		t.Errorf("movie histories read %q, want the video's own", radarrIDs)
	}
}

// onFirstVideoRead runs change when the run reads one video again, after
// the library read, so that Bazarr changes between the two.
func onFirstVideoRead(srv *fakebazarr.Server, change func()) {
	var once sync.Once
	srv.Respond(func(r fakebazarr.Request) fakebazarr.Behavior {
		if strings.Contains(r.RawQuery, "radarrid%5B%5D") || strings.Contains(r.RawQuery, "episodeid%5B%5D") {
			once.Do(change)
		}
		return fakebazarr.Serve
	})
}

// TestSyncDryRunJudgesLaterReads: the verdict and the line come from the
// video as read just before the judging, not from the library read when the
// run began; a subtitle the video no longer shows keeps its selected
// attributes and is judged the same way.
func TestSyncDryRunJudgesLaterReads(t *testing.T) {
	srv := fakebazarr.New(t, fakebazarr.Config{Library: syncLibrary()})
	onFirstVideoRead(srv, func() {
		lib := syncLibrary()
		lib.Movies[0].Subtitles[2].Forced = true // sintelEs
		srv.SetLibrary(lib)
	})
	o := dryRun(t, srv, filepath.Join(t.TempDir(), "s.json"), "--movie-id", "7", "--language", "es")
	if line := lineOf(t, o.stdout, sintelEs); line != "would skip  Sintel [movie 7]  es forced  "+sintelEs+"  (forced)" {
		t.Errorf("a subtitle become forced: %q", line)
	}

	srv = fakebazarr.New(t, fakebazarr.Config{Library: syncLibrary()})
	onFirstVideoRead(srv, func() {
		lib := syncLibrary()
		lib.Movies[1].Subtitles = append(lib.Movies[1].Subtitles, fakebazarr.Subtitle{Path: "/m/tears/t.en.srt", Language: "en"})
		srv.SetLibrary(lib)
	})
	o = dryRun(t, srv, filepath.Join(t.TempDir(), "s.json"), "--movie-id", "8", "--language", "es", "--reference", "lang:en")
	expectVerdicts(t, "a video gaining the reference subtitle", o, map[string]string{tearsEs: "sync"})

	srv = fakebazarr.New(t, fakebazarr.Config{Library: syncLibrary()})
	onFirstVideoRead(srv, func() {
		lib := syncLibrary()
		lib.Movies[1].Subtitles = lib.Movies[1].Subtitles[:1] // tearsEs deleted
		srv.SetLibrary(lib)
	})
	o = dryRun(t, srv, filepath.Join(t.TempDir(), "s.json"), "--movie-id", "8", "--language", "es")
	if line := lineOf(t, o.stdout, tearsEs); line != "would sync  Tears of Steel [movie 8]  es  "+tearsEs {
		t.Errorf("a subtitle no longer shown: %q, want it under its selected attributes", line)
	}
	srv = fakebazarr.New(t, fakebazarr.Config{Library: syncLibrary()})
	srv.AddMovieEvent(8, fakebazarr.Event{Action: 5, Time: "11/21/26 10:00:00", Path: tearsEs})
	onFirstVideoRead(srv, func() { srv.SetLibrary(fakebazarr.Library{}) }) // the movie itself gone
	o = dryRun(t, srv, filepath.Join(t.TempDir(), "s.json"), "--movie-id", "8", "--language", "es")
	expectVerdicts(t, "a movie no longer listed, its subtitle synced", o, map[string]string{tearsEs: "currently synced"})
}

// TestSyncDryRunStartsAgain: when a read needed a retry and the reads took
// longer than the read limit, the video and the queue are read again before
// the verdict; a shorter retry does not start again.
func TestSyncDryRunStartsAgain(t *testing.T) {
	for _, tc := range []struct {
		name    string
		failFor time.Duration
		want    []string
	}{
		{"longer than the limit", 61 * time.Second, []string{"movies", "system/jobs", "movies/history", "system/status",
			"movies", "system/jobs", "movies/history", "system/status"}},
		{"shorter than the limit", time.Second, []string{"movies", "system/jobs", "movies/history", "system/status"}},
	} {
		srv := fakebazarr.New(t, fakebazarr.Config{Library: syncLibrary()})
		clock := fakeclock.New(t)
		t0 := clock.Now()
		srv.Respond(func(r fakebazarr.Request) fakebazarr.Behavior {
			if r.Path == "/api/movies/history" && clock.Now().Before(t0.Add(tc.failFor)) {
				return fakebazarr.Status(http.StatusServiceUnavailable)
			}
			return fakebazarr.Serve
		})
		done := startRun(context.Background(), clock, envFor(srv), "sync", "--dry-run", "--state-file",
			filepath.Join(t.TempDir(), "s.json"), "--movie-id", "8", "--language", "es")
		for clock.Now().Before(t0.Add(tc.failFor)) {
			clock.Advance(clock.WaitSleep())
		}
		o := await(t, done)
		expectVerdicts(t, tc.name, o, map[string]string{tearsEs: "sync"})
		var got []string
		for _, c := range calls(srv)[4:] { // after the library read
			if len(got) == 0 || got[len(got)-1] != c {
				got = append(got, c) // one entry per read, its retries folded
			}
		}
		if !slices.Equal(got, tc.want) {
			t.Errorf("%s: reads %q, want %q", tc.name, got, tc.want)
		}
	}
}

// TestSyncDryRunSyncInBazarr: a subtitle the queue shows a sync job of is
// skipped for `sync in Bazarr` at once, never waited for.
func TestSyncDryRunSyncInBazarr(t *testing.T) {
	srv := fakebazarr.New(t, fakebazarr.Config{Library: syncLibrary()})
	srv.AddJob("Syncing "+tearsEs, fakebazarr.JobPending)
	srv.AddJob("Synced "+sintelEs, fakebazarr.JobRunning)
	srv.AddJob("Syncing "+sintelEn, fakebazarr.JobCompleted)
	o := dryRun(t, srv, filepath.Join(t.TempDir(), "s.json"), "--movies", "--no-forced")
	expectVerdicts(t, "queued syncs", o, map[string]string{
		tearsEs: "sync in Bazarr", sintelEs: "sync in Bazarr", sintelEn: "sync", sintelEnHI: "sync",
	})
	noChange(t, "queued syncs", srv)
}

// TestSyncDryRunSkips: G15's forced and currently synced subtitles, with
// and without --resync; one reason per subtitle, the first in P3's order.
func TestSyncDryRunSkips(t *testing.T) {
	srv := fakebazarr.New(t, fakebazarr.Config{Library: syncLibrary()})
	srv.AddMovieEvent(7, fakebazarr.Event{Action: 1, Time: "11/20/26 10:00:00", Path: sintelEn})
	srv.AddMovieEvent(7, fakebazarr.Event{Action: 5, Time: "11/21/26 10:00:00", Path: sintelEn})
	srv.AddMovieEvent(7, fakebazarr.Event{Action: 5, Time: "11/21/26 10:00:00", Path: sintelEs})
	srv.AddMovieEvent(7, fakebazarr.Event{Action: 4, Time: "11/22/26 10:00:00", Path: sintelEs})
	srv.AddMovieEvent(7, fakebazarr.Event{Action: 5, Time: "11/21/26 11:00:00", Path: sintelForced})
	srv.AddMovieEvent(7, fakebazarr.Event{Action: 5, Time: "11/22/26 09:00:00", Path: sintelEnHI})
	srv.AddMovieEvent(7, fakebazarr.Event{Action: 0, Time: "11/22/26 12:00:00", Path: sintelEnHI}) // not an event kind
	state := filepath.Join(t.TempDir(), "s.json")
	record(t, state, srv.URL, sintelForced, "5 11/21/26 11:00:00")
	o := dryRun(t, srv, state, "--movie-id", "7")
	expectVerdicts(t, "plain", o, map[string]string{
		sintelEn: "currently synced", sintelEs: "sync", sintelForced: "forced", sintelEnHI: "currently synced",
	})
	if !strings.Contains(o.stdout, "Total: 4 of 4 selected subtitles\n") ||
		!strings.Contains(o.stdout, "skipped (forced) 1") || strings.Contains(o.stdout, "earlier failure") {
		t.Errorf("a forced, synced subtitle with a record counts once, under forced:\n%s", o.stdout)
	}
	o = dryRun(t, srv, state, "--movie-id", "7", "--resync")
	expectVerdicts(t, "--resync", o, map[string]string{
		sintelEn: "sync", sintelEs: "sync", sintelForced: "forced", sintelEnHI: "sync",
	})
}

// TestSyncDryRunReference: with --reference lang:CODE, the reference is
// sought in the subtitle's video as read, never among the selected ones.
func TestSyncDryRunReference(t *testing.T) {
	srv := fakebazarr.New(t, fakebazarr.Config{Library: syncLibrary()})
	state := filepath.Join(t.TempDir(), "s.json")
	o := dryRun(t, srv, state, "--language", "es", "--reference", "lang:en")
	expectVerdicts(t, "es against en", o, map[string]string{sintelEs: "sync", tearsEs: "no reference subtitle"})
	o = dryRun(t, srv, state, "--language", "en", "--no-forced", "--reference", "lang:EN")
	expectVerdicts(t, "en against en", o, map[string]string{
		sintelEn: "reference language", sintelEnHI: "reference language",
	})
	if !strings.Contains(o.stdout, "skipped (reference language) 4") {
		t.Errorf("en against en: %s", o.stdout)
	}
	o = dryRun(t, srv, state, "--language", "de", "--reference", "lang:en")
	expectVerdicts(t, "an episode's other subtitle", o, map[string]string{towersDe: "no reference subtitle"})
}

// TestSyncDryRunOtherVideos: the events of a two-episode file are read in
// both episodes' histories, so a sync recorded through one episode makes
// the other's line currently synced too.
func TestSyncDryRunOtherVideos(t *testing.T) {
	srv := fakebazarr.New(t, fakebazarr.Config{Library: syncLibrary()})
	srv.AddEpisodeEvent(101, fakebazarr.Event{Action: 5, Time: "11/21/26 10:00:00", Path: towersDouble})
	o := dryRun(t, srv, filepath.Join(t.TempDir(), "s.json"), "--series", "--language", "en")
	if lines := resultLines(o.stdout); len(lines) != 2 ||
		lines[0] != "would skip  Fawlty Towers [series 3] S01E01  en  "+towersDouble+"  (currently synced)" ||
		lines[1] != "would skip  Fawlty Towers [series 3] S01E02  en  "+towersDouble+"  (currently synced)" {
		t.Errorf("lines %q; want both episodes currently synced", lines)
	}
	if o := runAgainst(t, srv, "list", "--series", "--language", "en"); len(resultLines(o.stdout)) != 2 {
		t.Errorf("list: %q", o.stdout)
	}
}

// TestSyncDryRunEnds: a rejected key ends before any other read (G2); an
// unmatched title is named (G8); an empty selection says so and exits zero
// (G9).
func TestSyncDryRunEnds(t *testing.T) {
	srv := fakebazarr.New(t, fakebazarr.Config{Library: syncLibrary()})
	state := filepath.Join(t.TempDir(), "s.json")
	env := envFor(srv)
	env["BAZARR_API_KEY"] = "wrong-key-0000"
	o := run(context.Background(), fakeclock.New(t), env, "sync", "--dry-run", "--state-file", state)
	failed(t, "a rejected key", o)
	if got := calls(srv); !slices.Equal(got, []string{"system/status"}) {
		t.Errorf("a rejected key: calls %q, want the status only", got)
	}
	o = dryRun(t, srv, state, "--title", "nothing like it")
	failed(t, "an unmatched title", o)
	if !strings.Contains(o.stderr, `"nothing like it"`) {
		t.Errorf("an unmatched title: %q", o.stderr)
	}
	o = dryRun(t, srv, state, "--language", "xx")
	if o.code != 0 || !strings.Contains(o.stderr, "no external subtitle matches") {
		t.Errorf("an empty selection: exit %d, stderr %q", o.code, o.stderr)
	}
	if _, err := os.Stat(state); err == nil {
		t.Errorf("a state file was created")
	}
}

// TestSyncDryRunStop: a stop during the reads ends the dry run with the
// totals for the subtitles already reported, exiting nonzero (G4).
func TestSyncDryRunStop(t *testing.T) {
	srv := fakebazarr.New(t, fakebazarr.Config{Library: syncLibrary()})
	videoReads := 0
	srv.Respond(func(r fakebazarr.Request) fakebazarr.Behavior {
		if strings.Contains(r.RawQuery, "radarrid%5B%5D") {
			if videoReads++; videoReads == 2 {
				return fakebazarr.Hold
			}
		}
		return fakebazarr.Serve
	})
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	done := startRun(ctx, fakeclock.New(t), envFor(srv), "sync", "--dry-run", "--state-file",
		filepath.Join(t.TempDir(), "s.json"), "--movie-id", "8")
	srv.WaitHeld()
	stop()
	o := await(t, done)
	if o.code == 0 || len(resultLines(o.stdout)) != 1 || !strings.Contains(o.stdout, "Total: 1 of 2 selected subtitles") {
		t.Errorf("exit %d, stdout %q; want one line, the totals, nonzero", o.code, o.stdout)
	}
	noChange(t, "a stop", srv)
}
