package commands_test

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/commands"
	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/failurestore"
	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/fakebazarr"
	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/fakeclock"
	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/reporter"
)

// sintelEnOnly selects Sintel's plain English subtitle alone.
var sintelEnOnly = []string{"--movie-id", "7", "--language", "en", "--no-hi"}

// recordOf returns path's record under address in the state file at state,
// as a later run loads it.
func recordOf(t *testing.T, state, address, path string) ([]string, bool) {
	t.Helper()
	store := failurestore.Load(state, address)
	if store.Err() != nil {
		t.Fatalf("the state file cannot be loaded: %v", store.Err())
	}
	return store.Lookup(path)
}

// fileBytes returns the content of the file at path.
func fileBytes(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// runLater returns a check that runs to its end each sync job srv queues
// from now on, writing a sync event at eventTime unless it is empty.
func runLater(t *testing.T, srv *fakebazarr.Server, eventTime string) func() {
	t.Helper()
	done := len(syncsSent(srv))
	return func() {
		for _, p := range syncsSent(srv)[done:] {
			done++
			runJob(t, srv, p, eventTime)
		}
	}
}

// TestSyncRecordsFailure: a sync Bazarr ran that ended without a sync event
// is recorded under the address as given, less its trailing slash; the
// same command run again does not submit it and counts it under `earlier
// failure`, exiting zero; --retry-failed submits it again, and so does a
// run against another address with the same state file (G21, G15, State).
// The file holds the address, the path, and the baseline, never the key
// (F9, R2).
func TestSyncRecordsFailure(t *testing.T) {
	srv := fakebazarr.New(t, fakebazarr.Config{Library: syncLibrary()})
	srv.AddMovieEvent(7, fakebazarr.Event{Action: 1, Time: "11/20/26 10:00:00", Path: sintelEn})
	state := filepath.Join(t.TempDir(), "state.json")
	s := startSyncState(t, srv, state, append(sintelEnOnly, "--url", srv.URL+"/")...)
	o := s.finish(runLater(t, srv, ""))
	if outcomeOf(t, o.stdout, sintelEn) != "failed" || o.code == 0 {
		t.Fatalf("a sync that wrote no event: exit %d\n%s", o.code, o.stdout)
	}
	want := `{"bazarrbulksync_state":1,"servers":{` + strconv.Quote(srv.URL) + `:{` + strconv.Quote(sintelEn) +
		`:["1 11/20/26 10:00:00"]}}}` + "\n"
	got := fileBytes(t, state)
	if got != want {
		t.Errorf("state file %q,\nwant %q", got, want)
	}
	if strings.Contains(got, srv.APIKey()) {
		t.Errorf("the state file holds the API key")
	}

	s = startSyncState(t, srv, state, sintelEnOnly...)
	o = s.finish(nil)
	if n := len(syncsSent(srv)); n != 1 || o.code != 0 || len(resultLines(o.stdout)) != 0 ||
		!strings.Contains(o.stdout, "Total: 1 of 1 selected subtitle\nskipped (earlier failure) 1\n") {
		t.Errorf("run again: %d syncs sent in all, exit %d\n%s", n, o.code, o.stdout)
	}

	s = startSyncState(t, srv, state, append(sintelEnOnly, "--retry-failed")...)
	o = s.finish(runLater(t, srv, "11/22/26 08:15:02"))
	if n := len(syncsSent(srv)); n != 2 || outcomeOf(t, o.stdout, sintelEn) != "synced" {
		t.Errorf("--retry-failed: %d syncs sent in all\n%s", n, o.stdout)
	}
	// The new sync event lapses the record, which goes at once (State).
	if ids, ok := recordOf(t, state, srv.URL, sintelEn); ok {
		t.Errorf("--retry-failed: the lapsed record is still there after the sync: %q", ids)
	}

	other := fakebazarr.New(t, fakebazarr.Config{Library: syncLibrary()})
	other.AddMovieEvent(7, fakebazarr.Event{Action: 1, Time: "11/20/26 10:00:00", Path: sintelEn})
	s = startSyncState(t, other, state, sintelEnOnly...)
	o = s.finish(runLater(t, other, "11/22/26 08:15:02"))
	if n := len(syncsSent(other)); n != 1 || outcomeOf(t, o.stdout, sintelEn) != "synced" {
		t.Errorf("another address: %d syncs sent\n%s", n, o.stdout)
	}
}

// TestSyncRecordsBaseline: the record holds the events as read before the
// failed sync was submitted, so an upload Bazarr records while that sync
// runs lapses it and the next run submits the subtitle; with nothing new,
// the next run skips it, also when it never had an event (G21).
func TestSyncRecordsBaseline(t *testing.T) {
	for _, c := range []struct {
		name           string
		events, upload bool
	}{{"an upload during the sync", true, true}, {"nothing new", true, false}, {"no events ever", false, false}} {
		srv := fakebazarr.New(t, fakebazarr.Config{Library: syncLibrary()})
		var base []string
		if c.events {
			srv.AddMovieEvent(7, fakebazarr.Event{Action: 1, Time: "11/20/26 10:00:00", Path: sintelEn})
			base = []string{"1 11/20/26 10:00:00"}
		}
		state := filepath.Join(t.TempDir(), "state.json")
		s := startSyncState(t, srv, state, sintelEnOnly...)
		s.until("submitted", func() bool { return len(syncsSent(srv)) == 1 }, nil)
		id, _ := srv.SyncJob(sintelEn)
		srv.SetJob(id, fakebazarr.JobRunning)
		if c.upload {
			srv.AddMovieEvent(7, fakebazarr.Event{Action: 4, Time: "11/22/26 08:15:00", Path: sintelEn})
		}
		srv.SyncRan(id, "")
		srv.SetJob(id, fakebazarr.JobCompleted)
		if o := s.finish(nil); outcomeOf(t, o.stdout, sintelEn) != "failed" {
			t.Fatalf("%s: %s", c.name, o.stdout)
		}
		if ids, ok := recordOf(t, state, srv.URL, sintelEn); !ok || !slices.Equal(ids, base) {
			t.Errorf("%s: recorded %q (%v), want the baseline %q", c.name, ids, ok, base)
		}
		s = startSyncState(t, srv, state, sintelEnOnly...)
		o := s.finish(runLater(t, srv, "11/22/26 09:00:00"))
		if submitted := len(syncsSent(srv)) == 2; submitted != c.upload {
			t.Errorf("%s: the next run submitted it: %v\n%s", c.name, submitted, o.stdout)
		}
		if !c.upload && !strings.Contains(o.stdout, "skipped (earlier failure) 1") {
			t.Errorf("%s: next run\n%s", c.name, o.stdout)
		}
	}
}

// TestSyncForgetsLapsed: a record the judging finds lapsed is gone from the
// state file before the subtitle's sync ends, and stays gone however it
// ends unless Bazarr ran it and recorded no sync, which records it again
// with the new baseline; every other record and server is kept (State,
// G21).
func TestSyncForgetsLapsed(t *testing.T) {
	for _, ending := range []string{"synced", "refused", "unconfirmed", "failed"} {
		srv := fakebazarr.New(t, fakebazarr.Config{Library: syncLibrary()})
		srv.AddMovieEvent(7, fakebazarr.Event{Action: 1, Time: "11/20/26 10:00:00", Path: sintelEn})
		srv.AddMovieEvent(7, fakebazarr.Event{Action: 4, Time: "11/22/26 10:00:00", Path: sintelEn})
		srv.AddMovieEvent(7, fakebazarr.Event{Action: 1, Time: "11/20/26 10:00:00", Path: sintelEs})
		state := filepath.Join(t.TempDir(), "state.json")
		record(t, state, srv.URL, sintelEn, "1 11/20/26 10:00:00") // lapsed by the upload
		record(t, state, srv.URL, sintelEs, "1 11/20/26 10:00:00")
		record(t, state, "http://other:6767", tearsEs)
		if ending == "refused" {
			srv.Respond(func(r fakebazarr.Request) fakebazarr.Behavior {
				if r.Method == http.MethodPatch {
					return fakebazarr.Status(http.StatusBadRequest)
				}
				return fakebazarr.Serve
			})
		}
		kept := func(when string) {
			t.Helper()
			if _, ok := recordOf(t, state, srv.URL, sintelEs); !ok {
				t.Errorf("%s, %s: the record of %s is gone", ending, when, sintelEs)
			}
			if _, ok := recordOf(t, state, "http://other:6767", tearsEs); !ok {
				t.Errorf("%s, %s: the other server's record is gone", ending, when)
			}
		}
		s := startSyncState(t, srv, state, "--movie-id", "7", "--no-forced", "--no-hi")
		handled := false
		o := s.finish(func() {
			if _, queued := srv.SyncJob(sintelEn); !queued || handled {
				return
			}
			handled = true
			if _, ok := recordOf(t, state, srv.URL, sintelEn); ok {
				t.Errorf("%s: the lapsed record is still in the state file while its sync is followed", ending)
			}
			kept("while its sync is followed")
			switch ending {
			case "synced":
				runJob(t, srv, sintelEn, "11/22/26 11:00:00")
			case "unconfirmed":
				srv.Restart("1763800000.5")
			case "failed":
				runJob(t, srv, sintelEn, "")
			}
		})
		word := ending
		if ending == "refused" {
			word = "failed"
		}
		if got := outcomeOf(t, o.stdout, sintelEn); got != word || !strings.Contains(o.stdout, "skipped (earlier failure) 1") {
			t.Errorf("%s: %s\n%s", ending, got, o.stdout)
		}
		ids, ok := recordOf(t, state, srv.URL, sintelEn)
		if ending == "failed" {
			if want := []string{"4 11/22/26 10:00:00", "1 11/20/26 10:00:00"}; !ok || !slices.Equal(ids, want) {
				t.Errorf("failed: recorded %q (%v), want %q", ids, ok, want)
			}
		} else if ok {
			t.Errorf("%s: the lapsed record is back after the run: %q", ending, ids)
		}
		kept("after the run")
	}
}

// TestSyncWriteFails: when a write of the state file fails, the run says so
// once with the path and the cause, leaves the file as it was, and goes on
// as if no failure were recorded: the subtitle skipped for an earlier
// failure is judged again and submitted, counted once, and later failures
// say nothing more about the file (G23, Failure store, G14). The write is
// a failure's record, or the forgetting of a lapsed record.
func TestSyncWriteFails(t *testing.T) {
	for _, write := range []string{"record", "forget"} {
		srv := fakebazarr.New(t, fakebazarr.Config{Library: syncLibrary()})
		state := filepath.Join(t.TempDir(), "state.json")
		record(t, state, srv.URL, sintelEnHI) // no events then, none now: skipped
		eventTime := ""                       // every sync fails
		if write == "forget" {
			srv.AddMovieEvent(7, fakebazarr.Event{Action: 1, Time: "11/20/26 10:00:00", Path: sintelEn})
			srv.AddMovieEvent(7, fakebazarr.Event{Action: 4, Time: "11/22/26 10:00:00", Path: sintelEn})
			record(t, state, srv.URL, sintelEn, "1 11/20/26 10:00:00") // lapsed
			eventTime = "11/22/26 11:00:00"
		}
		before := fileBytes(t, state)
		// A directory where the temporary file goes makes every write fail.
		blockWrites := func() {
			if err := os.Mkdir(state+".tmp", 0o700); err != nil {
				t.Fatal(err)
			}
		}
		if write == "forget" {
			blockWrites()
		}
		runEach := runLater(t, srv, eventTime)
		s := startSyncState(t, srv, state, sintelThree...)
		s.until("submitted", func() bool { return len(syncsSent(srv)) == 1 }, nil)
		if write == "record" {
			blockWrites()
		}
		o := s.finish(runEach)
		if n := strings.Count(o.stderr, "cannot be used"); n != 1 || !strings.Contains(o.stderr, state) ||
			!strings.Contains(o.stderr, "cannot write") {
			t.Errorf("%s: standard error\n%s", write, o.stderr)
		}
		if got := slices.Sorted(slices.Values(syncsSent(srv))); !slices.Equal(got, []string{sintelEnHI, sintelEn, sintelEs}) {
			t.Errorf("%s: syncs sent %q", write, got)
		}
		word, code := "failed", 1
		if write == "forget" {
			word, code = "synced", 0
		}
		for _, p := range []string{sintelEnHI, sintelEn, sintelEs} {
			if got := outcomeOf(t, o.stdout, p); got != word {
				t.Errorf("%s: %s %s, want %s", write, p, got, word)
			}
		}
		if !strings.HasSuffix(o.stdout, "\nTotal: 3 of 3 selected subtitles\n"+word+" 3\n") || o.code != code {
			t.Errorf("%s: exit %d\n%s", write, o.code, o.stdout)
		}
		if !strings.HasSuffix(o.stderr, "\n3 of 3 subtitles done\n") {
			t.Errorf("%s: progress\n%s", write, o.stderr)
		}
		if after := fileBytes(t, state); after != before {
			t.Errorf("%s: the state file changed:\n%s", write, after)
		}
	}
}

// TestSyncNotAStateFile: a file that is not a state file is named once,
// every subtitle is treated as unrecorded, and a failure Bazarr ran leaves
// the file's bytes as they were and no temporary file (G23).
func TestSyncNotAStateFile(t *testing.T) {
	srv := fakebazarr.New(t, fakebazarr.Config{Library: syncLibrary()})
	dir := t.TempDir()
	notState := filepath.Join(dir, "notes.json")
	if err := os.WriteFile(notState, []byte(`{"servers": 1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	s := startSyncState(t, srv, notState, sintelEnOnly...)
	o := s.finish(runLater(t, srv, ""))
	if strings.Count(o.stderr, "cannot be used") != 1 || !strings.Contains(o.stderr, notState) ||
		outcomeOf(t, o.stdout, sintelEn) != "failed" || o.code == 0 {
		t.Errorf("exit %d\n%s%s", o.code, o.stdout, o.stderr)
	}
	if got := fileBytes(t, notState); got != `{"servers": 1}` {
		t.Errorf("the file changed: %s", got)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Errorf("the directory holds %v, want the file only", entries)
	}
}

// TestSyncCreatesStateFile: a run in which nothing failed creates neither
// the state file nor its directory; the first recorded failure creates
// both, at --state-file's path (G23, F3).
func TestSyncCreatesStateFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "new")
	state := filepath.Join(dir, "state.json")
	srv := fakebazarr.New(t, fakebazarr.Config{Library: syncLibrary()})
	s := startSyncState(t, srv, state, sintelEnOnly...)
	if o := s.finish(runLater(t, srv, "11/22/26 08:15:02")); o.code != 0 {
		t.Fatalf("exit %d\n%s", o.code, o.stdout)
	}
	if _, err := os.Stat(dir); err == nil {
		t.Errorf("a run with no failure created %s", dir)
	}
	srv = fakebazarr.New(t, fakebazarr.Config{Library: syncLibrary()})
	s = startSyncState(t, srv, state, sintelEnOnly...)
	s.finish(runLater(t, srv, ""))
	if _, ok := recordOf(t, state, srv.URL, sintelEn); !ok {
		t.Errorf("no record after the first failure")
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Errorf("%s holds %v, want the state file only", dir, entries)
	}
}

// TestSyncRecordsAtOnce: a failure is in the state file, complete, when it
// is reported, not when the run ends: while the run follows a later sync,
// another command on that file skips the subtitle for an earlier failure
// (G22).
func TestSyncRecordsAtOnce(t *testing.T) {
	srv := fakebazarr.New(t, fakebazarr.Config{Library: syncLibrary()})
	s := startSync(t, srv, sintelThree...)
	s.until("submitted", func() bool { return len(syncsSent(srv)) == 1 }, nil)
	runJob(t, srv, sintelEnHI, "")
	s.until("the second is submitted", func() bool { return len(syncsSent(srv)) == 2 }, nil)
	if outcomeOf(t, s.stdout.String(), sintelEnHI) != "failed" {
		t.Fatalf("the first sync: %s", s.stdout.String())
	}
	if ids, ok := recordOf(t, s.state, srv.URL, sintelEnHI); !ok || len(ids) != 0 {
		t.Errorf("while the next sync is followed: recorded %q (%v)", ids, ok)
	}
	o := dryRun(t, srv, s.state, sintelThree...)
	if got := verdict(t, o.stdout, sintelEnHI); got != "earlier failure" {
		t.Errorf("another command on the state file: %s judged %q", sintelEnHI, got)
	}
	s.stop()
	<-s.ended
}

// lineWatcher is standard output that calls onWrite with each write, as the
// reporter makes it, before keeping it.
type lineWatcher struct {
	syncBuffer
	onWrite func(text string)
}

func (w *lineWatcher) Write(p []byte) (int, error) {
	w.onWrite(string(p))
	return w.syncBuffer.Write(p)
}

// TestSyncRecordsBeforeItsLine: when the `failed` line of a sync Bazarr ran
// is written, its failure is already in the state file, so that a kill
// after the line cannot lose it (G22).
func TestSyncRecordsBeforeItsLine(t *testing.T) {
	srv := fakebazarr.New(t, fakebazarr.Config{Library: syncLibrary()})
	state := filepath.Join(t.TempDir(), "state.json")
	var atLine []bool
	out := &lineWatcher{onWrite: func(text string) {
		if strings.HasPrefix(text, "failed  ") {
			_, ok := failurestore.Load(state, srv.URL).Lookup(sintelEn)
			atLine = append(atLine, ok)
		}
	}}
	runWatched(t, srv, state, out, io.Discard, runLater(t, srv, ""), sintelEnOnly...)
	if len(atLine) != 1 || !atLine[0] {
		t.Errorf("the failure in the state file when its line was written: %v\n%s", atLine, out.String())
	}
}

// runWatched runs `sync --yes` with args against srv to its end, with the
// state file at state and standard output and error going to out and
// errOut; at each still state, check runs.
func runWatched(t *testing.T, srv *fakebazarr.Server, state string, out, errOut io.Writer, check func(), args ...string) {
	t.Helper()
	clock := fakeclock.New(t)
	rep := reporter.FromStreams(reporter.Streams{In: strings.NewReader(""), Out: out, Err: errOut})
	env := envFor(srv)
	ended := make(chan struct{})
	go func() {
		defer close(ended)
		commands.Run(context.Background(), append([]string{"sync", "--yes", "--state-file", state}, args...),
			commands.Env{Getenv: func(name string) string { return env[name] }, Reporter: rep, Clock: clock})
	}()
	for range 400 {
		d, ok := clock.WaitSleepUnless(ended)
		if !ok {
			return
		}
		check()
		clock.Advance(d)
	}
	t.Fatal("the run never ended")
}

// TestSyncForgetsAfterSubmitting: removing a lapsed record, and saying that
// the state file cannot be written, come after the submission the judging
// decided: when Bazarr queues its own sync of the subtitle meanwhile, no
// second sync is sent beside it (G15, G18).
func TestSyncForgetsAfterSubmitting(t *testing.T) {
	srv := fakebazarr.New(t, fakebazarr.Config{Library: syncLibrary()})
	srv.AddMovieEvent(7, fakebazarr.Event{Action: 1, Time: "11/20/26 10:00:00", Path: sintelEs})
	srv.AddMovieEvent(7, fakebazarr.Event{Action: 4, Time: "11/22/26 10:00:00", Path: sintelEs})
	state := filepath.Join(t.TempDir(), "state.json")
	record(t, state, srv.URL, sintelEs, "1 11/20/26 10:00:00") // lapsed
	if err := os.Mkdir(state+".tmp", 0o700); err != nil {
		t.Fatal(err) // the forgetting fails
	}
	// Bazarr queues its own sync of sintelEs while the run says the state
	// file cannot be used; the run's queue reads were clear just before.
	var bazarrsJob atomic.Int64
	var pending atomic.Bool
	errOut := &lineWatcher{onWrite: func(text string) {
		if strings.Contains(text, "cannot be used") && bazarrsJob.Load() == 0 {
			bazarrsJob.Store(int64(srv.AddJob("Syncing "+sintelEs, fakebazarr.JobPending)))
			pending.Store(true)
		}
	}}
	var besideBazarrs atomic.Bool
	srv.Respond(func(r fakebazarr.Request) fakebazarr.Behavior {
		if r.Method == http.MethodPatch && pending.Load() {
			besideBazarrs.Store(true)
		}
		return fakebazarr.Serve
	})
	// Job IDs count up from 1: the run's syncs and Bazarr's one.
	ran := map[int]bool{}
	runWatched(t, srv, state, io.Discard, errOut, func() {
		jobs := len(syncsSent(srv))
		if bazarrsJob.Load() != 0 {
			jobs++
		}
		for id := 1; id <= jobs; id++ {
			if ran[id] {
				continue
			}
			ran[id] = true
			if id == int(bazarrsJob.Load()) {
				srv.SetJob(id, fakebazarr.JobCompleted)
				pending.Store(false)
				continue
			}
			srv.SetJob(id, fakebazarr.JobRunning)
			srv.SyncRan(id, "11/22/26 11:00:00")
			srv.SetJob(id, fakebazarr.JobCompleted)
		}
	}, "--movie-id", "7", "--no-forced", "--no-hi")
	if bazarrsJob.Load() == 0 || besideBazarrs.Load() {
		t.Errorf("a sync sent while Bazarr showed its own (its job %d, sent beside it: %v)", bazarrsJob.Load(), besideBazarrs.Load())
	}
	if got := syncsSent(srv); len(got) == 0 || got[0] != sintelEn {
		t.Errorf("syncs sent %q, want %s first", got, sintelEn)
	}
}
