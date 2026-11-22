package commands_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/failurestore"
	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/fakebazarr"
	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/fakeclock"
)

// record records a failure of path under address in the state file at
// state, with the given event identities.
func record(t *testing.T, state, address, path string, ids ...string) {
	t.Helper()
	if err := failurestore.Load(state, address).Record(path, ids); err != nil {
		t.Fatal(err)
	}
}

// TestSyncDryRunEarlierFailure: a recorded failure skips its subtitle until
// Bazarr's history shows an event of kinds 1 to 6 it did not show, or with
// --retry-failed (G15, G21); records of another address skip nothing; the
// dry run writes nothing, a lapsed record included (G13).
func TestSyncDryRunEarlierFailure(t *testing.T) {
	srv := fakebazarr.New(t, fakebazarr.Config{Library: syncLibrary()})
	srv.AddMovieEvent(7, fakebazarr.Event{Action: 1, Time: "11/20/26 10:00:00", Path: sintelEn})
	srv.AddMovieEvent(7, fakebazarr.Event{Action: 1, Time: "11/20/26 10:00:00", Path: sintelEs})
	srv.AddMovieEvent(7, fakebazarr.Event{Action: 4, Time: "11/22/26 10:00:00", Path: sintelEs})
	dir := t.TempDir()
	state := filepath.Join(dir, "s.json")
	record(t, state, srv.URL+"/", sintelEn, "1 11/20/26 10:00:00")
	record(t, state, srv.URL, sintelEs, "1 11/20/26 10:00:00") // lapsed by the upload
	record(t, state, srv.URL, sintelEnHI)                      // no events then, none now
	record(t, state, "http://other:6767", tearsEs)
	before, err := os.ReadFile(state)
	if err != nil {
		t.Fatal(err)
	}
	o := dryRun(t, srv, state, "--movies", "--no-forced")
	expectVerdicts(t, "plain", o, map[string]string{
		sintelEn: "earlier failure", sintelEs: "sync", sintelEnHI: "earlier failure", tearsEs: "sync",
	})
	o = dryRun(t, srv, state, "--movies", "--no-forced", "--retry-failed")
	expectVerdicts(t, "--retry-failed", o, map[string]string{sintelEn: "sync", sintelEs: "sync", sintelEnHI: "sync"})
	srv.AddMovieEvent(7, fakebazarr.Event{Action: 6, Time: "11/22/26 11:00:00", Path: sintelEnHI})
	o = dryRun(t, srv, state, "--movies", "--no-forced")
	expectVerdicts(t, "a translation since", o, map[string]string{sintelEn: "earlier failure", sintelEnHI: "sync"})
	after, _ := os.ReadFile(state)
	if string(after) != string(before) {
		t.Errorf("the state file changed under --dry-run:\n%s\nwant\n%s", after, before)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Errorf("the directory holds %v, want the state file only", entries)
	}
	noChange(t, "earlier failures", srv)
}

// TestSyncDryRunTemporaryFile: a complete temporary file beside a missing
// state file is the state (State, Loading); the dry run renames nothing.
func TestSyncDryRunTemporaryFile(t *testing.T) {
	srv := fakebazarr.New(t, fakebazarr.Config{Library: syncLibrary()})
	dir := t.TempDir()
	state := filepath.Join(dir, "s.json")
	record(t, state, srv.URL, tearsEs)
	if err := os.Rename(state, state+".tmp"); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(state + ".tmp")
	o := dryRun(t, srv, state, "--movie-id", "8")
	expectVerdicts(t, "a lone temporary file", o, map[string]string{tearsEs: "earlier failure"})
	after, _ := os.ReadFile(state + ".tmp")
	if entries, _ := os.ReadDir(dir); len(entries) != 1 || entries[0].Name() != "s.json.tmp" || string(after) != string(before) {
		t.Errorf("the directory holds %v; want the temporary file alone, unchanged", entries)
	}
}

// TestSyncDryRunUnusableState: a state file that is not one is named once
// with its cause, left untouched, and the run judges as if no failure were
// recorded, exiting zero (G23, G6); so with no resolvable default path.
func TestSyncDryRunUnusableState(t *testing.T) {
	srv := fakebazarr.New(t, fakebazarr.Config{Library: syncLibrary()})
	state := filepath.Join(t.TempDir(), "s.json")
	if err := os.WriteFile(state, []byte("not a state file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	o := dryRun(t, srv, state, "--movie-id", "8")
	expectVerdicts(t, "not a state file", o, map[string]string{tearsEs: "sync"})
	if strings.Count(o.stderr, state) != 1 || !strings.Contains(o.stderr, "not a state file") {
		t.Errorf("standard error %q; want the path and the cause once", o.stderr)
	}
	if b, _ := os.ReadFile(state); string(b) != "not a state file\n" {
		t.Errorf("the file became %q", b)
	}

	o = runAgainst(t, srv, "sync", "--dry-run", "--movie-id", "8") // envFor gives no home
	expectVerdicts(t, "no default path", o, map[string]string{tearsEs: "sync"})
	if strings.Count(o.stderr, "state file cannot be used") != 1 || !strings.Contains(o.stderr, "cannot be resolved") {
		t.Errorf("standard error %q; want once that the state file cannot be used", o.stderr)
	}
}

// TestSyncDryRunDefaultStateFile: without --state-file the state file is
// P1's, under the configuration directory of the home the environment gives.
func TestSyncDryRunDefaultStateFile(t *testing.T) {
	srv := fakebazarr.New(t, fakebazarr.Config{Library: syncLibrary()})
	home := t.TempDir()
	config := filepath.Join(home, ".config")
	switch runtime.GOOS {
	case "darwin":
		config = filepath.Join(home, "Library", "Application Support")
	case "windows":
		config = home
	}
	record(t, filepath.Join(config, "bazarrbulksync", "state.json"), srv.URL, tearsEs)
	env := envFor(srv)
	env["HOME"], env["AppData"] = home, home
	o := run(context.Background(), fakeclock.New(t), env, "sync", "--dry-run", "--movie-id", "8")
	expectVerdicts(t, "the default state file", o, map[string]string{tearsEs: "earlier failure"})
	if o.stderr != "1 of 2 subtitles done\n2 of 2 subtitles done\n" {
		t.Errorf("standard error %q", o.stderr)
	}
}

// TestSyncRecordedFailure: a real run reads the recorded failures as the
// dry run does: a recorded subtitle is not submitted and counts under
// `earlier failure`; a file that is not a state file is named once and
// every subtitle is treated as unrecorded; either way the file's bytes stay
// as they were (G15, G21, G23).
func TestSyncRecordedFailure(t *testing.T) {
	srv := fakebazarr.New(t, fakebazarr.Config{Library: syncLibrary()})
	state := filepath.Join(t.TempDir(), "state.json")
	record(t, state, srv.URL, sintelEn)
	before, _ := os.ReadFile(state)
	s := startSyncState(t, srv, state, "--movie-id", "7", "--language", "en", "--no-hi")
	o := s.finish(nil)
	if n := len(syncsSent(srv)); n != 0 || o.code != 0 || !strings.Contains(o.stdout, "skipped (earlier failure) 1") {
		t.Errorf("a recorded failure: %d syncs sent, exit %d\n%s", n, o.code, o.stdout)
	}
	if after, _ := os.ReadFile(state); string(after) != string(before) {
		t.Errorf("the state file changed:\n%s", after)
	}

	srv = fakebazarr.New(t, fakebazarr.Config{Library: syncLibrary()})
	notState := filepath.Join(t.TempDir(), "notes.json")
	if err := os.WriteFile(notState, []byte(`{"servers": 1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	s = startSyncState(t, srv, notState, "--movie-id", "7", "--language", "en", "--no-hi")
	ran := map[string]bool{}
	o = s.finish(func() { runSubmitted(t, srv, ran, "11/22/26 08:15:02") })
	if strings.Count(o.stderr, "cannot be used") != 1 || outcomeOf(t, o.stdout, sintelEn) != "synced" || o.code != 0 {
		t.Errorf("not a state file: exit %d\n%s%s", o.code, o.stdout, o.stderr)
	}
	if after, _ := os.ReadFile(notState); string(after) != `{"servers": 1}` {
		t.Errorf("the file changed: %s", after)
	}
}
