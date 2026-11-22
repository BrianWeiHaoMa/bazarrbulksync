package invocation_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/bazarr"
	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/invocation"
)

// fixNames are F10's six names, in its order, each with Bazarr's fix.
var fixNames = []struct {
	name string
	fix  bazarr.Fix
}{
	{"remove-hi", bazarr.FixRemoveHI}, {"remove-tags", bazarr.FixRemoveTags}, {"ocr-fixes", bazarr.FixOCR},
	{"common-fixes", bazarr.FixCommon}, {"fix-uppercase", bazarr.FixUppercase}, {"remove-emoji", bazarr.FixEmoji},
}

// TestFixName: fix takes exactly one of F10's names, and no other word
// Bazarr might know (N3); with it, any selection option or none (G7).
func TestFixName(t *testing.T) {
	for _, args := range [][]string{
		{"fix"}, {"fix", "--yes"}, {"fix", "--movies", "--yes"}, {"fix", "--dry-run"},
		{"fix", "remove-tags", "remove-hi"}, {"fix", "remove-tags", "remove-tags"}, {"fix", "remove-tags", "--yes", "extra"},
		{"fix", "shift"}, {"fix", "color"}, {"fix", "Remove-Tags"}, {"fix", "REMOVE-HI"}, {"fix", "remove_tags"},
		{"fix", "remove_HI"}, {"fix", "common"}, {"fix", "emoji"}, {"fix", ""}, {"fix", " remove-tags"},
		{"fix", "--", "remove-tags"}, {"fix", "-remove-tags"}, {"fix", "--remove-tags"},
		{"fix", "remove-tags", "--json"}, {"fix", "remove-tags", "--movies", "--series"},
	} {
		if ue := usageError(t, noEnv, args...); ue != nil && !strings.Contains(ue.Hint(), "fix --help") {
			t.Errorf("Parse(%q): hint %q, want fix's usage", args, ue.Hint())
		}
	}
	for _, f := range fixNames {
		req := request(t, noEnv, "fix", f.name)
		if req.Command != invocation.CommandFix || req.Fix != f.fix || req.Selection.Type != "" || req.Change.Yes {
			t.Errorf("fix %s: %+v", f.name, req)
		}
	}
	for _, args := range [][]string{
		{"fix", "remove-tags", "--movies"}, {"fix", "--movies", "remove-tags"}, {"fix", "--movies", "--yes", "remove-tags"},
	} {
		if req := request(t, noEnv, args...); req.Fix != bazarr.FixRemoveTags || req.Selection.Type != bazarr.TypeMovie {
			t.Errorf("%q: %+v", args, req)
		}
	}
	req := request(t, noEnv, "fix", "remove-tags", "--title", "T", "--yes")
	if req.Fix != bazarr.FixRemoveTags || !slices.Equal(req.Selection.Titles, []string{"T"}) || !req.Change.Yes {
		t.Errorf("fix remove-tags --title T --yes: %+v", req)
	}
}

// TestFixNameBesideValues: an option's value is never taken for the fix's
// name, and the name never for a value (G1).
func TestFixNameBesideValues(t *testing.T) {
	for _, args := range [][]string{
		{"fix", "remove-tags", "--title", "remove-hi", "--dry-run"},
		{"fix", "--title", "remove-hi", "remove-tags", "--dry-run"},
		{"fix", "--title=remove-hi", "--dry-run", "remove-tags"},
	} {
		req := request(t, noEnv, args...)
		if req.Fix != bazarr.FixRemoveTags || !slices.Equal(req.Selection.Titles, []string{"remove-hi"}) || !req.Change.DryRun {
			t.Errorf("%q: fix %v, titles %q; want remove-tags with title remove-hi", args, req.Fix, req.Selection.Titles)
		}
	}
	req := request(t, noEnv, "fix", "remove-hi", "--language", "remove-tags")
	if req.Fix != bazarr.FixRemoveHI || !slices.Equal(req.Selection.Languages, []string{"remove-tags"}) {
		t.Errorf("fix remove-hi --language remove-tags: %+v", req)
	}
	// A value left out is not filled by the name, nor the name by a value.
	usageError(t, noEnv, "fix", "--title", "--dry-run", "remove-tags")
	usageError(t, noEnv, "fix", "--title", "remove-hi")
	// Commands without a word still refuse a stray one.
	usageError(t, noEnv, "list", "remove-tags")
	usageError(t, noEnv, "delete", "--language", "es", "remove-tags")
	if req := request(t, noEnv, "delete", "--title", "remove-tags"); req.Selection.Titles[0] != "remove-tags" {
		t.Errorf("delete --title remove-tags: %+v", req)
	}
}

// TestFixHelp: the program's help lists fix, and fix --help names the six
// fixes, with or without a name (F5, F10).
func TestFixHelp(t *testing.T) {
	for _, args := range [][]string{{"fix", "--help"}, {"fix", "remove-hi", "--help"}, {"fix", "--yes", "--help"}} {
		res := parse(t, noEnv, args...)
		if res.Request != nil {
			t.Fatalf("Parse(%q) gave a request", args)
		}
		want := []string{"Usage: bazarrbulksync fix FIX", "--yes", "--dry-run", "--language CODE", "with none, every external subtitle is selected"}
		for _, f := range fixNames {
			want = append(want, "  "+f.name+" ")
		}
		for _, w := range want {
			if !strings.Contains(res.Text, w) {
				t.Errorf("Parse(%q): help does not hold %q:\n%s", args, w, res.Text)
			}
		}
	}
	usageError(t, noEnv, "fix", "shift", "--help")
	usageError(t, noEnv, "fix", "remove-hi", "remove-tags", "--help")
}
