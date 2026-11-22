package invocation_test

import (
	"strings"
	"testing"

	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/invocation"
)

// TestDeleteNeedsSelection: G26 counts only F6's options as a selection.
func TestDeleteNeedsSelection(t *testing.T) {
	withEnv := env(map[string]string{"BAZARR_URL": "http://h:6767", "BAZARR_API_KEY": "k"})
	for _, args := range [][]string{
		{"delete"}, {"delete", "--yes"}, {"delete", "--dry-run"}, {"delete", "--yes", "--dry-run"},
		{"delete", "--url", "http://h:6767", "--api-key", "k", "--yes"},
	} {
		ue := usageError(t, withEnv, args...)
		if ue != nil && (!strings.Contains(ue.Problem, "selection option") || !strings.Contains(ue.Hint(), "delete --help")) {
			t.Errorf("Parse(%q): %q, hint %q; want the selection option required, with delete's usage", args, ue.Problem, ue.Hint())
		}
	}
	for _, opt := range [][]string{
		{"--movies"}, {"--series"}, {"--title", "x"}, {"--movie-id", "1"}, {"--series-id", "1"},
		{"--season", "0"}, {"--language", "es"}, {"--hi"}, {"--no-hi"}, {"--forced"}, {"--no-forced"},
	} {
		req := request(t, withEnv, append([]string{"delete"}, opt...)...)
		if req.Command != invocation.CommandDelete || req.Change.Yes || req.Change.DryRun {
			t.Errorf("delete %q: %+v", opt, req)
		}
	}
	req := request(t, withEnv, "delete", "--language", "es", "--yes", "--dry-run")
	if !req.Change.Yes || !req.Change.DryRun || req.Connection.URL != "http://h:6767" {
		t.Errorf("delete --language es --yes --dry-run: %+v", req)
	}
}

// TestChangeOptionsBelongToChangingCommands: F7 gives --yes and --dry-run to
// the changing commands and --json to list alone (G1).
func TestChangeOptionsBelongToChangingCommands(t *testing.T) {
	for _, args := range [][]string{
		{"delete", "--language", "es", "--json"}, {"list", "--yes"}, {"list", "--dry-run"},
		{"delete", "--language", "es", "--yes=1"}, {"delete", "--language", "es", "extra"},
		{"delete", "--movies", "--series", "--yes"},
	} {
		usageError(t, noEnv, args...)
	}
}

// TestDeleteHelp: delete --help prints delete's usage, with or without a
// selection option (F5).
func TestDeleteHelp(t *testing.T) {
	for _, args := range [][]string{{"delete", "--help"}, {"delete", "--yes", "--help"}, {"delete", "--language", "es", "--help"}} {
		res := parse(t, noEnv, args...)
		for _, want := range []string{"Usage: bazarrbulksync delete", "--yes", "--dry-run", "--language CODE", "at least one is required"} {
			if !strings.Contains(res.Text, want) {
				t.Errorf("Parse(%q): help does not hold %q:\n%s", args, want, res.Text)
			}
		}
	}
}
