package main_test

import (
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/bazarr"
	"github.com/BrianWeiHaoMa/bazarrbulksync/testbed/bed"
)

// changesVar must be 1, beside BAZARRBULKSYNC_TESTBED, for TestBedChanges to
// run: it deletes and rewrites the bed's files, so a plain `go test ./...`
// with the bed's variable set, which also runs the read checks in parallel
// packages, never runs it.
const changesVar = "BAZARRBULKSYNC_TESTBED_CHANGES"

// TestBedChanges runs `delete` and `fix` through the built executable
// against the test bed's real Bazarr, and compares what `list` prints and
// what the subtitle files hold, before and after. It leaves the bed changed;
// `go run ./testbed up` provisions it again. Each subtest first verifies that
// what it needs is as the bed placed it, so that none passes because another
// ran first; run alone or in this order on a fresh bed, each passes.
func TestBedChanges(t *testing.T) {
	m, ok, err := bed.Load()
	if !ok {
		t.Skipf("no test bed: set %s to the file `go run ./testbed up` writes", bed.EnvVar)
	}
	if err != nil {
		t.Fatal(err)
	}
	if os.Getenv(changesVar) != "1" {
		t.Skipf("these checks change the bed: set %s=1 to run them", changesVar)
	}
	b := newChangeBed(t, m)

	t.Run("refused", func(t *testing.T) {
		es := b.language("es")
		b.needShown(t, es...)
		before, files := b.list(t), b.files(t)
		for _, c := range []struct {
			args []string
			says string // on standard error
		}{
			{[]string{"delete"}, "selection option"},
			{[]string{"delete", "--yes"}, "selection option"},
			// Standard input is the null device: not a terminal (G12).
			{[]string{"delete", "--language", "es"}, "--yes"},
		} {
			args := c.args
			r, sent := b.exec(t, args...)
			if r.code == 0 || r.stdout != "" || !strings.Contains(r.stderr, c.says) {
				t.Errorf("%q: code %d, stdout %q, stderr %q; want %q named on standard error, nonzero", args, r.code, r.stdout, r.stderr, c.says)
			}
			if changes(sent) != nil || (len(args) < 3 && sent != nil) {
				t.Errorf("%q sent %v; want nothing without a selection option (G26), and no change without --yes (G12)", args, sent)
			}
		}
		if after := b.list(t); after != before {
			t.Errorf("list printed\n%s\nbefore, and after\n%s", before, after)
		}
		b.unchanged(t, files, b.files(t))
	})

	t.Run("G25", func(t *testing.T) {
		hi, plain := b.hiBesidePlain(t)
		forced := b.one(t, "a forced subtitle beside the plain one", func(s bazarr.Subtitle) bool {
			return s.Forced && sameVideoLanguage(s, plain)
		})
		lone := b.loneHI(t)
		b.needProvisioned(t, hi.Path, plain.Path, forced.Path)
		r := b.run(t, 0, "fix", "remove-hi", "--dry-run")
		b.sends(t, 0)
		lines := strings.Split(r.stdout, "\n")
		for _, want := range []string{
			"would skip  " + b.listLine(t, hi) + "  (replaces non-HI subtitle)",
			"would apply  " + b.listLine(t, lone),
		} {
			if !slices.Contains(lines, want) {
				t.Errorf("fix remove-hi --dry-run printed no line %q:\n%s", want, r.stdout)
			}
		}

		files := b.files(t)
		r = b.run(t, 0, "fix", "remove-hi", "--forced", "--title", forced.Title, "--yes")
		b.sends(t, 0)
		if strings.Contains(r.stdout, "applied") || !slices.Contains(strings.Split(r.stdout, "\n"), "skipped (replaces non-HI subtitle) 1") {
			t.Errorf("fix remove-hi on the forced subtitle beside a plain one: want it skipped and nothing applied:\n%s", r.stdout)
		}
		b.unchanged(t, files, b.files(t))
		b.needShown(t, plain, forced)
	})

	t.Run("remove-tags", func(t *testing.T) {
		title := m.Series[0].Title
		var ofTitle, others []string
		for _, s := range m.Subtitles {
			if s.Title == title {
				ofTitle = append(ofTitle, s.Path)
			}
		}
		for _, p := range b.planted("remove-tags") {
			if !slices.Contains(ofTitle, p.Path) {
				others = append(others, p.Path)
			}
		}
		b.needProvisioned(t, append(slices.Clone(ofTitle), others...)...)
		files := b.files(t)
		for _, p := range append(slices.Clone(ofTitle), others...) {
			if !styleTag.Match(files[p]) {
				t.Fatalf("%s holds no style tag, so the check would show nothing", p)
			}
		}
		if len(others) == 0 {
			t.Fatalf("no file of another title holds a style tag, so a fix of every title would pass")
		}
		listed := b.listLines(t, "--title", title)
		r := b.run(t, 0, "fix", "remove-tags", "--title", title, "--yes")
		b.sends(t, len(ofTitle))
		b.reports(t, r, "applied", listed)
		after := b.files(t)
		for _, p := range ofTitle {
			if tag := styleTag.Find(after[p]); tag != nil {
				t.Errorf("%s still holds the style tag %q", p, tag)
			}
			for _, l := range textLines(files[p]) {
				if l = string(styleTag.ReplaceAll([]byte(l), nil)); !slices.Contains(textLines(after[p]), l) {
					t.Errorf("%s lost the text %q", p, l)
				}
			}
		}
		b.unchanged(t, files, after, ofTitle...)
	})

	for _, fix := range []string{"ocr-fixes", "common-fixes", "fix-uppercase", "remove-emoji"} {
		t.Run(fix, func(t *testing.T) {
			planted := b.planted(fix)
			if len(planted) == 0 {
				t.Fatalf("the bed planted nothing for %s", fix)
			}
			target := b.subtitle(t, planted[0].Path)
			b.needProvisioned(t, target.Path)
			files := b.files(t)
			alone := b.selectAlone(t, target)
			listed := b.listLines(t, alone...)
			r := b.run(t, 0, append([]string{"fix", fix, "--yes"}, alone...)...)
			b.sends(t, 1)
			b.reports(t, r, "applied", listed)
			after := b.files(t)
			b.corrected(t, planted, files, after, target.Path)
			b.unchanged(t, files, after, target.Path)
		})
	}

	t.Run("remove-hi", func(t *testing.T) {
		lone := b.loneHI(t)
		planted := b.planted("remove-hi")
		if len(planted) == 0 || planted[0].Path != lone.Path {
			t.Fatalf("the bed planted no hearing-impaired cues in %s", lone.Path)
		}
		b.needProvisioned(t, lone.Path)
		files := b.files(t)
		line := b.listLine(t, lone)
		r := b.run(t, 0, append([]string{"fix", "remove-hi", "--yes"}, b.selectAlone(t, lone)...)...)
		b.sends(t, 1)
		b.reports(t, r, "applied", []string{line})
		// Bazarr 1.6.2 writes the result to the video's path for a subtitle
		// of that language that is neither HI nor forced, removing the
		// fixed file (subtitles/tools/mods.py).
		result := strings.TrimSuffix(lone.Path, ".hi.srt") + ".srt"
		after := b.files(t)
		if _, ok := after[lone.Path]; ok {
			t.Errorf("%s is still there; Bazarr 1.6.2 was seen moving the result to %s", lone.Path, result)
		}
		if _, err := m.ReadFile(result); err != nil {
			t.Fatalf("the result: %v", err)
		}
		b.corrected(t, planted, files, after, result)
		listed := b.listedPaths(t)
		if shown := pathsInAnswer(t, m); !sameSet(listed, shown) || !slices.Contains(shown, result) {
			t.Errorf("list names %v,\nBazarr shows %v; want the same, %s among them", listed, shown, result)
		}
		b.unchanged(t, files, after, lone.Path, result)
	})

	t.Run("delete es", func(t *testing.T) {
		es := b.language("es")
		b.needShown(t, es...)
		listES := b.listLines(t, "--language", "es")
		files := b.files(t)
		r := b.run(t, 0, "delete", "--language", "es", "--dry-run")
		b.sends(t, 0)
		b.reports(t, r, "would delete", listES)
		b.unchanged(t, files, b.files(t))
		b.needShown(t, es...)

		others := map[string]string{}
		for _, s := range m.Subtitles {
			if s.Language != "es" {
				others[s.Language] = b.list(t, "--language", s.Language)
			}
		}
		r = b.run(t, 0, "delete", "--language", "es", "--yes")
		b.sends(t, len(es))
		b.reports(t, r, "deleted", listES)
		if r := runExecutable(t, m.URL, m.APIKey, "list", "--language", "es"); r.code != 0 || r.stdout != "" || r.stderr == "" {
			t.Errorf("list --language es afterwards: code %d, stdout %q, stderr %q; want an empty selection noted, exit 0", r.code, r.stdout, r.stderr)
		}
		after := b.files(t)
		var esPaths []string
		for _, s := range es {
			esPaths = append(esPaths, s.Path)
			if _, ok := after[s.Path]; ok {
				t.Errorf("%s is still on the media volume", s.Path)
			}
		}
		for lang, before := range others {
			if got := b.list(t, "--language", lang); got != before {
				t.Errorf("list --language %s printed\n%s\nbefore, and after\n%s", lang, before, got)
			}
		}
		b.unchanged(t, files, after, esPaths...)
	})
}

// styleTag is a style tag of an SRT file.
var styleTag = regexp.MustCompile(`</?(?:i|b|u|s|font)(?:\s[^>]*)?>|\{\\[^}]*\}`)
