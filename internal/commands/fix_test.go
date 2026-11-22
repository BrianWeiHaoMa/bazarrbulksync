package commands_test

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/fakebazarr"
	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/fakeclock"
)

// patch is one PATCH subtitles request the fake received.
type patch struct{ path, action string }

// patches returns the PATCH subtitles requests the fake received, in order.
func patches(srv *fakebazarr.Server) []patch {
	var got []patch
	for _, r := range srv.Requests() {
		if r.Method == http.MethodPatch {
			got = append(got, patch{r.Params().Get("path"), r.Params().Get("action")})
		}
	}
	return got
}

func patchedPaths(srv *fakebazarr.Server) []string {
	var paths []string
	for _, p := range patches(srv) {
		paths = append(paths, p.path)
	}
	return paths
}

// listLine returns path's line as `list` prints it.
func listLine(t *testing.T, srv *fakebazarr.Server, path string) string {
	t.Helper()
	return lineOf(t, runAgainst(t, srv, "list").stdout, path)
}

func TestFixName(t *testing.T) {
	srv := fakebazarr.New(t, fakebazarr.Config{Library: library})
	for _, args := range [][]string{
		{"fix"}, {"fix", "--yes"}, {"fix", "--language", "en", "--yes"}, {"fix", "remove-tags", "remove-hi", "--yes"},
		{"fix", "shift", "--yes"}, {"fix", "color", "--yes"}, {"fix", "Remove-Tags", "--yes"}, {"fix", "remove_tags", "--yes"},
	} {
		o := runAgainst(t, srv, args...)
		failed(t, strings.Join(args, " "), o)
		if o.code != 2 || !strings.Contains(o.stderr, "fix --help") {
			t.Errorf("%q: code %d, stderr %q; want a usage error with fix's usage (G1)", args, o.code, o.stderr)
		}
	}
	if n := len(srv.Requests()); n != 0 {
		t.Errorf("the fake received %d requests, want none (G1)", n)
	}
	for _, args := range [][]string{{"fix", "remove-tags", "--movies", "--yes"}, {"fix", "remove-tags", "--title", "sintel", "--yes"}} {
		if o := runAgainst(t, srv, args...); o.code != 0 || !strings.Contains(o.stdout, "applied 3") {
			t.Errorf("%q: code %d, stdout %q, stderr %q; want Sintel's three subtitles applied", args, o.code, o.stdout, o.stderr)
		}
	}
}

// TestFixEachName: each of F10's names has Bazarr apply that fix, and no
// other (F10).
func TestFixEachName(t *testing.T) {
	for name, action := range map[string]string{
		"remove-hi": "remove_HI", "remove-tags": "remove_tags", "ocr-fixes": "OCR_fixes",
		"common-fixes": "common", "fix-uppercase": "fix_uppercase", "remove-emoji": "emoji",
	} {
		srv := fakebazarr.New(t, fakebazarr.Config{Library: library})
		o := runAgainst(t, srv, "fix", name, "--language", "en", "--no-hi", "--yes")
		got := patches(srv)
		if o.code != 0 || len(got) != 2 {
			t.Errorf("%s: code %d, %d requests, stderr %q; want the two applied", name, o.code, len(got), o.stderr)
		}
		for _, p := range got {
			if p.action != action {
				t.Errorf("%s sent action %q for %s, want %q", name, p.action, p.path, action)
			}
		}
	}
}

func TestFixOutcomes(t *testing.T) {
	t.Run("one refused, one unanswered, one accepted", func(t *testing.T) {
		srv := fakebazarr.New(t, fakebazarr.Config{Library: library})
		lines := listLines(t, srv)
		var mu sync.Mutex
		n := 0
		srv.Respond(func(r fakebazarr.Request) fakebazarr.Behavior {
			if r.Method != http.MethodPatch {
				return fakebazarr.Serve
			}
			mu.Lock()
			defer mu.Unlock()
			n++
			switch n {
			case 1:
				return fakebazarr.Reply(409, `"Unable to take action on subtitles file. Check logs."`)
			case 2:
				return fakebazarr.Status(504)
			}
			return fakebazarr.Serve
		})
		o := runAgainst(t, srv, "fix", "remove-tags", "--language", "en", "--yes")
		if o.code == 0 {
			t.Errorf("exit code 0, want nonzero for failed and unconfirmed (G6)")
		}
		want := []string{
			"failed  " + lines[enHI] + "  (Unable to take action on subtitles file. Check logs.)",
			"unconfirmed  " + lines[enPlain],
			"applied  " + lines[enSpecial],
		}
		if got := resultLines(o.stdout); strings.Join(got, "\n") != strings.Join(want, "\n") {
			t.Errorf("result lines\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
		}
		if p := patchedPaths(srv); !slices.Equal(p, selectedEn) {
			t.Errorf("fixed %q, want each selected subtitle once, in order (G24)", p)
		}
		for _, w := range []string{"Total: 3 of 3", "applied 1", "failed 1", "unconfirmed 1"} {
			if !strings.Contains(o.stdout, w) {
				t.Errorf("standard output %q lacks %q (G14)", o.stdout, w)
			}
		}
	})
	t.Run("all accepted, twice", func(t *testing.T) {
		srv := fakebazarr.New(t, fakebazarr.Config{Library: library})
		for range 2 {
			if o := runAgainst(t, srv, "fix", "remove-tags", "--language", "en", "--yes"); o.code != 0 || !strings.Contains(o.stdout, "applied 3") {
				t.Errorf("code %d, stdout %q, stderr %q; want exit 0, applied 3", o.code, o.stdout, o.stderr)
			}
		}
		if p := patchedPaths(srv); !slices.Equal(p, append(slices.Clone(selectedEn), selectedEn...)) {
			t.Errorf("fixed %q, want the same three requests on each run (G24 keeps no record)", p)
		}
	})
}

func TestFixDryRun(t *testing.T) {
	for _, extra := range [][]string{nil, {"--yes"}} {
		srv := fakebazarr.New(t, fakebazarr.Config{Library: library})
		all := strings.Split(strings.TrimSpace(runAgainst(t, srv, "list").stdout), "\n\n")[0]
		o := runAgainst(t, srv, append([]string{"fix", "remove-tags", "--dry-run"}, extra...)...)
		for _, r := range srv.Requests() {
			if r.Method != http.MethodGet {
				t.Errorf("%q: the dry run sent %s %s, want reads only (G13)", extra, r.Method, r.Path)
			}
		}
		if o.code != 0 {
			t.Fatalf("%q: code %d, stderr %q; want exit 0 (G13)", extra, o.code, o.stderr)
		}
		var want []string
		for _, l := range strings.Split(all, "\n") {
			want = append(want, "would apply  "+l)
		}
		if got := resultLines(o.stdout); strings.Join(got, "\n") != strings.Join(want, "\n") {
			t.Errorf("%q: result lines\n%s\nwant every external subtitle (G7)\n%s", extra, strings.Join(got, "\n"), strings.Join(want, "\n"))
		}
		if !strings.Contains(o.stdout, "would apply 5") {
			t.Errorf("%q: standard output %q, want the totals", extra, o.stdout)
		}
	}
}

func TestFixConfirmation(t *testing.T) {
	srv := fakebazarr.New(t, fakebazarr.Config{Library: library})
	o := runAgainst(t, srv, "fix", "remove-tags", "--language", "en")
	failed(t, "not a terminal", o)
	if !strings.Contains(o.stderr, "--yes") {
		t.Errorf("standard error %q, want --yes required (G12)", o.stderr)
	}
	// Every selected subtitle would be skipped: confirmation is still needed.
	o = runAgainst(t, srv, "fix", "remove-hi", "--hi", "--language", "en")
	failed(t, "all skipped, not a terminal", o)
	if !strings.Contains(o.stderr, "--yes") {
		t.Errorf("standard error %q, want --yes required (G12)", o.stderr)
	}
	if p := patches(srv); len(p) != 0 {
		t.Errorf("sent %v without confirmation", p)
	}
	keys := newKeyboard()
	r := startWith(context.Background(), fakeclock.New(t), envFor(srv), stdin{reader: keys, terminal: true}, "fix", "remove-tags", "--language", "en")
	keys.waitAsked(t)
	if s := r.stderr.String(); !strings.Contains(s, "3 subtitles selected. Apply remove-tags to them? [y/N]") {
		t.Errorf("standard error %q, want the number selected and the question (G12)", s)
	}
	keys.lines <- "y\n"
	if o := await(t, r.done); o.code != 0 || len(patches(srv)) != 3 {
		t.Errorf("code %d, %d requests, stderr %q; want the three applied", o.code, len(patches(srv)), o.stderr)
	}
}

func TestFixStop(t *testing.T) {
	srv := fakebazarr.New(t, fakebazarr.Config{Library: library})
	lines := listLines(t, srv)
	srv.Respond(func(r fakebazarr.Request) fakebazarr.Behavior {
		if r.Method == http.MethodPatch && r.Params().Get("path") == enPlain {
			return fakebazarr.Hold
		}
		return fakebazarr.Serve
	})
	ctx, stop := context.WithCancel(context.Background())
	r := startWith(ctx, fakeclock.New(t), envFor(srv), notTerminal, "fix", "remove-tags", "--language", "en", "--yes")
	srv.WaitHeld()
	sent := len(srv.Requests())
	stop()
	o := await(t, r.done)
	if o.code == 0 {
		t.Errorf("exit code 0, want nonzero (G4)")
	}
	want := []string{"applied  " + lines[enHI], "unconfirmed  " + lines[enPlain]}
	if got := resultLines(o.stdout); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("result lines %q, want %q (G4)", got, want)
	}
	for _, w := range []string{"Total: 2 of 3", "applied 1", "unconfirmed 1"} {
		if !strings.Contains(o.stdout, w) {
			t.Errorf("standard output %q lacks %q (G4)", o.stdout, w)
		}
	}
	if n := len(srv.Requests()); n != sent {
		t.Errorf("%d requests after the stop, want none (G4)", n-sent)
	}
}

// removeHILibrary holds, per video, the cases of G25's rule: Bazarr writes
// remove-hi's result in place of the video's subtitle of that language that
// is neither hearing-impaired nor forced.
var removeHILibrary = fakebazarr.Library{
	Movies: []fakebazarr.Movie{
		{RadarrID: 1, Title: "Alpha", Subtitles: []fakebazarr.Subtitle{
			{Path: "/m/a.en.hi.srt", Language: "en", HI: true}, // beside the plain en: skipped
			{Path: "/m/a.en.srt", Language: "en"},              // beside an HI only: applied
			{Path: "/m/a.es.srt", Language: "es"},              // alone in its language: applied
			{Path: "/m/a.fr.hi.srt", Language: "fr", HI: true}, // its only sibling in another language: applied
		}},
		{RadarrID: 2, Title: "Bravo", Subtitles: []fakebazarr.Subtitle{
			{Path: "/m/b.en.forced.hi.srt", Language: "en", Forced: true, HI: true}, // no plain en, first of its language: applied
			{Path: "/m/b.en.forced.srt", Language: "en", Forced: true},              // would write over the first's result: skipped
		}},
		{RadarrID: 3, Title: "Charlie", Subtitles: []fakebazarr.Subtitle{
			{Path: "/m/c.en.forced.srt", Language: "en", Forced: true}, // Bazarr would write over the plain en: skipped
			{Path: "/m/c.en.srt", Language: "en"},                      // beside a forced only: applied
		}},
		{RadarrID: 4, Title: "Delta", Subtitles: []fakebazarr.Subtitle{
			{Path: "/m/d.en.1.srt", Language: "en"}, // two plain en: both skipped
			{Path: "/m/d.en.2.srt", Language: "en"},
		}},
		{RadarrID: 5, Title: "Echo", Subtitles: []fakebazarr.Subtitle{
			{Path: "/m/e.en.forced.srt", Language: "en", Forced: true}, // first of its language: applied
			{Path: "/m/e.en.hi.srt", Language: "en", HI: true},         // would write over the first's result: skipped
		}},
	},
	Series: []fakebazarr.Series{
		{SonarrSeriesID: 6, Title: "Foxtrot", Episodes: []fakebazarr.Episode{
			{SonarrEpisodeID: 61, Season: 1, Episode: 1, Subtitles: []fakebazarr.Subtitle{
				{Path: "/tv/f1.en.hi.srt", Language: "en", HI: true}, // the plain en is another episode's: applied
			}},
			{SonarrEpisodeID: 62, Season: 1, Episode: 2, Subtitles: []fakebazarr.Subtitle{
				{Path: "/tv/f2.en.srt", Language: "en"},
			}},
		}},
	},
}

var (
	removeHIApplied = []string{"/m/a.en.srt", "/m/a.es.srt", "/m/a.fr.hi.srt", "/m/b.en.forced.hi.srt", "/m/c.en.srt",
		"/m/e.en.forced.srt", "/tv/f1.en.hi.srt", "/tv/f2.en.srt"}
	removeHISkipped = []string{"/m/a.en.hi.srt", "/m/b.en.forced.srt", "/m/c.en.forced.srt", "/m/d.en.1.srt",
		"/m/d.en.2.srt", "/m/e.en.hi.srt"}
)

// TestRemoveHIRule: fix remove-hi sends no fix that would have Bazarr
// replace another subtitle (G25), and still fixes the others.
func TestRemoveHIRule(t *testing.T) {
	srv := fakebazarr.New(t, fakebazarr.Config{Library: removeHILibrary})
	o := runAgainst(t, srv, "fix", "remove-hi", "--yes")
	if p := patchedPaths(srv); o.code != 0 || !slices.Equal(p, removeHIApplied) {
		t.Errorf("code %d, fixed\n%q\nwant\n%q\nstderr %q", o.code, p, removeHIApplied, o.stderr)
	}
	for _, path := range removeHISkipped {
		if strings.Contains(o.stdout, path) {
			t.Errorf("standard output names the skipped %s; without --dry-run a skip has no line (P3)", path)
		}
	}
	if !strings.Contains(o.stdout, "applied 8") || !strings.Contains(o.stdout, "skipped (replaces non-HI subtitle) 6") {
		t.Errorf("standard output %q, want the skips counted under their reason (G14)", o.stdout)
	}

	srv = fakebazarr.New(t, fakebazarr.Config{Library: removeHILibrary})
	list := runAgainst(t, srv, "list").stdout
	o = runAgainst(t, srv, "fix", "remove-hi", "--dry-run")
	if o.code != 0 || len(patches(srv)) != 0 {
		t.Fatalf("dry run: code %d, %d requests", o.code, len(patches(srv)))
	}
	for _, path := range removeHIApplied {
		if l := "would apply  " + lineOf(t, list, path) + "\n"; !strings.Contains(o.stdout, l) {
			t.Errorf("dry run: standard output lacks %q", l)
		}
	}
	for _, path := range removeHISkipped {
		if l := "would skip  " + lineOf(t, list, path) + "  (replaces non-HI subtitle)\n"; !strings.Contains(o.stdout, l) {
			t.Errorf("dry run: standard output lacks %q (P3)", l)
		}
	}
	if !strings.Contains(o.stdout, "would apply 8") || !strings.Contains(o.stdout, "skipped (replaces non-HI subtitle) 6") {
		t.Errorf("dry run: standard output %q, want the run's totals predicted", o.stdout)
	}
}

// TestRemoveHISkips: the subtitle that would be replaced is sought in the
// library as read, not among the selected; only remove-hi skips.
func TestRemoveHISkips(t *testing.T) {
	srv := fakebazarr.New(t, fakebazarr.Config{Library: removeHILibrary})
	o := runAgainst(t, srv, "fix", "remove-hi", "--hi", "--yes")
	want := []string{"/m/a.fr.hi.srt", "/m/b.en.forced.hi.srt", "/m/e.en.hi.srt", "/tv/f1.en.hi.srt"} // Echo's forced is not selected, so not fixed first
	if p := patchedPaths(srv); o.code != 0 || !slices.Equal(p, want) {
		t.Errorf("--hi: code %d, fixed %q, want %q (G25)", o.code, p, want)
	}

	srv = fakebazarr.New(t, fakebazarr.Config{Library: removeHILibrary})
	o = runAgainst(t, srv, "fix", "remove-hi", "--title", "alpha", "--language", "en", "--hi", "--yes")
	if o.code != 0 || len(patches(srv)) != 0 || !strings.Contains(o.stdout, "Total: 1 of 1 selected subtitle\nskipped (replaces non-HI subtitle) 1") {
		t.Errorf("all skipped: code %d, %d requests, stdout %q; want none sent, exit 0 (G6)", o.code, len(patches(srv)), o.stdout)
	}

	for name, action := range map[string]string{"remove-tags": "remove_tags", "fix-uppercase": "fix_uppercase"} {
		srv = fakebazarr.New(t, fakebazarr.Config{Library: removeHILibrary})
		o = runAgainst(t, srv, "fix", name, "--yes")
		if p := patches(srv); o.code != 0 || len(p) != 14 || p[0] != (patch{"/m/a.en.hi.srt", action}) {
			t.Errorf("%s: code %d, fixed %v; want all fourteen, no skips", name, o.code, p)
		}
	}
}

// TestRemoveHIWithinRun: a video with only a forced and an HI subtitle of
// one language has its first fixed and its second skipped, since Bazarr
// writes both results in one place; the dry run predicts the same.
func TestRemoveHIWithinRun(t *testing.T) {
	srv := fakebazarr.New(t, fakebazarr.Config{Library: removeHILibrary})
	forced, hi := listLine(t, srv, "/m/e.en.forced.srt"), listLine(t, srv, "/m/e.en.hi.srt")
	o := runAgainst(t, srv, "fix", "remove-hi", "--title", "echo", "--yes")
	if p := patchedPaths(srv); o.code != 0 || !slices.Equal(p, []string{"/m/e.en.forced.srt"}) {
		t.Errorf("code %d, fixed %q; want the forced alone", o.code, p)
	}
	if got := resultLines(o.stdout); !slices.Equal(got, []string{"applied  " + forced}) {
		t.Errorf("result lines %q, want the applied alone (P3)", got)
	}
	if !strings.Contains(o.stdout, "applied 1, skipped (replaces non-HI subtitle) 1") || !strings.Contains(o.stderr, "2 of 2 subtitles done") {
		t.Errorf("stdout %q, stderr %q; want the skip counted and done (G14)", o.stdout, o.stderr)
	}
	o = runAgainst(t, srv, "fix", "remove-hi", "--title", "echo", "--dry-run")
	want := []string{"would apply  " + forced, "would skip  " + hi + "  (replaces non-HI subtitle)"}
	if got := resultLines(o.stdout); !slices.Equal(got, want) {
		t.Errorf("dry run: result lines %q, want %q (G13)", got, want)
	}
}
