package invocation_test

import (
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/bazarr"
	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/invocation"
	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/selection"
)

// home gives a home on every system; homeStateFile is P1's default there.
var home = env(map[string]string{"HOME": "/home/ana", "AppData": `C:\Users\ana\AppData\Roaming`})

func homeStateFile() string {
	switch runtime.GOOS {
	case "darwin":
		return "/home/ana/Library/Application Support/bazarrbulksync/state.json"
	case "windows":
		return `C:\Users\ana\AppData\Roaming\bazarrbulksync\state.json`
	}
	return "/home/ana/.config/bazarrbulksync/state.json"
}

// TestSyncMalformedValues: a value outside F8 is a usage error (G1); the
// values F8 admits are accepted.
func TestSyncMalformedValues(t *testing.T) {
	for _, args := range [][]string{
		{"--concurrency", "0"}, {"--concurrency", "1.5"}, {"--concurrency", "-1"}, {"--concurrency", ""},
		{"--concurrency", "99999999999999999999"}, {"--max-offset-seconds", "0"}, {"--max-offset-seconds", "x"},
		{"--reference", "a:10"}, {"--reference", "a:"}, {"--reference", "x:1"}, {"--reference", "lang:"},
		{"--reference", "en"}, {"--reference", "A:1"}, {"--reference", "s:-1"}, {"--reference", "a:1 "}, {"--reference", "lang:en "}, {"--reference", "lang: en"},
		{"--state-file", ""}, {"--concurrency"}, {"--reference"},
	} {
		usageError(t, home, append([]string{"sync"}, args...)...)
	}
	for _, tc := range []struct {
		args []string
		want invocation.SyncOptions
	}{
		{[]string{"--concurrency", "3"}, invocation.SyncOptions{Concurrency: 3}},
		{[]string{"--max-offset-seconds", "120"}, invocation.SyncOptions{Concurrency: 1, Bazarr: bazarr.SyncOptions{MaxOffsetSeconds: 120}}},
		{[]string{"--reference", "a:0"}, invocation.SyncOptions{Concurrency: 1, Bazarr: bazarr.SyncOptions{Reference: "a:0"}}},
		{[]string{"--reference=s:9"}, invocation.SyncOptions{Concurrency: 1, Bazarr: bazarr.SyncOptions{Reference: "s:9"}}},
		{[]string{"--reference", "lang:en"}, invocation.SyncOptions{Concurrency: 1, ReferenceLanguage: "en"}},
		{[]string{"--reference", "lang:en", "--reference", "a:2"}, invocation.SyncOptions{Concurrency: 1, Bazarr: bazarr.SyncOptions{Reference: "a:2"}}},
		{[]string{"--no-fix-framerate", "--gss", "--resync", "--retry-failed"},
			invocation.SyncOptions{Concurrency: 1, Resync: true, RetryFailed: true, Bazarr: bazarr.SyncOptions{NoFixFramerate: true, GSS: true}}},
		{nil, invocation.SyncOptions{Concurrency: 1}},
	} {
		req := request(t, home, append([]string{"sync"}, tc.args...)...)
		tc.want.StateFile = homeStateFile()
		if req.Command != invocation.CommandSync || !reflect.DeepEqual(req.Sync, tc.want) {
			t.Errorf("sync %q: %+v, want %+v", tc.args, req.Sync, tc.want)
		}
	}
	// The product specification's example.
	req := request(t, home, "sync", "--title", "breaking bad", "--season", "2", "--resync", "--max-offset-seconds", "120")
	if !req.Sync.Resync || req.Sync.Bazarr.MaxOffsetSeconds != 120 ||
		!reflect.DeepEqual(req.Selection, selection.Criteria{Titles: []string{"breaking bad"}, Seasons: []int{2}}) {
		t.Errorf("the specification's example: %+v", req)
	}
}

// TestSyncOptionsAreSyncs: F8's options belong to sync, F7's --json to
// list; sync takes no positional word and needs no selection option (G7).
func TestSyncOptionsAreSyncs(t *testing.T) {
	for _, args := range [][]string{
		{"list", "--resync"}, {"delete", "--language", "es", "--concurrency", "2"}, {"fix", "remove-tags", "--reference", "a:1"},
		{"sync", "--json"}, {"sync", "now"}, {"sync", "remove-tags"}, {"list", "--state-file", "/x"},
	} {
		usageError(t, home, args...)
	}
	req := request(t, home, "sync", "--yes", "--dry-run")
	if !req.Change.Yes || !req.Change.DryRun || !reflect.DeepEqual(req.Selection, selection.Criteria{}) {
		t.Errorf("sync --yes --dry-run: %+v", req)
	}
}

// TestSyncStateFile: --state-file is used as given; the default is P1's,
// resolved from the environment the parse is given, or unresolved with
// why when it gives no home (P1, L2).
func TestSyncStateFile(t *testing.T) {
	if got := request(t, home, "sync", "--state-file", "rel/s.json").Sync; got.StateFile != "rel/s.json" || got.StateFileProblem != "" {
		t.Errorf("--state-file rel/s.json: %+v", got)
	}
	if got := request(t, noEnv, "sync", "--state-file", "/srv/s.json").Sync; got.StateFile != "/srv/s.json" {
		t.Errorf("--state-file without a home: %+v", got)
	}
	got := request(t, noEnv, "sync").Sync
	if got.StateFile != "" || got.StateFileProblem == "" {
		t.Errorf("no home: %+v, want no path and the reason", got)
	}
}

// TestSyncHelp: sync --help names every option of F8 and F7 and prints the
// default state file's path, or that it cannot be resolved (P1).
func TestSyncHelp(t *testing.T) {
	text := parse(t, home, "sync", "--help").Text
	for _, want := range []string{"Usage: bazarrbulksync sync [options]", "--yes", "--dry-run", "--resync", "--retry-failed",
		"--concurrency N", "--state-file PATH", "--max-offset-seconds N", "--no-fix-framerate", "--gss", "--reference REF",
		"a:N", "s:N", "lang:CODE", "--language CODE", homeStateFile()} {
		if !strings.Contains(text, want) {
			t.Errorf("sync --help lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "--json") || strings.Contains(text, "at least one is required") {
		t.Errorf("sync --help offers another command's option or demands a selection:\n%s", text)
	}
	noHome := parse(t, noEnv, "sync", "--help").Text
	if !strings.Contains(noHome, "cannot be resolved") || strings.Contains(noHome, "state.json") {
		t.Errorf("sync --help without a home:\n%s", noHome)
	}
	if program := parse(t, noEnv, "--help").Text; !strings.Contains(program, "  list ") ||
		strings.Index(program, "  sync ") < strings.Index(program, "  list ") || strings.Index(program, "  fix ") < strings.Index(program, "  sync ") {
		t.Errorf("program help does not list sync after list (F5):\n%s", program)
	}
}
