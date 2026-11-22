package commands

import (
	"bytes"
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/bazarr"
	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/fakebazarr"
	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/fakeclock"
	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/invocation"
	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/reporter"
	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/selection"
)

// TestSkipRule: the shared run asks a command's skip rule, made from the
// whole library as read, for each selected subtitle in order just before
// acting; a skip has a line only under --dry-run, and is counted under its
// reason either way (P3, G14). No command built yet has a rule, so the run
// is driven here with delete's request and a rule of the test's.
func TestSkipRule(t *testing.T) {
	lib := fakebazarr.Library{Movies: []fakebazarr.Movie{{RadarrID: 7, Title: "Sintel", Subtitles: []fakebazarr.Subtitle{
		{Path: "/m/a.en.srt", Language: "en"}, {Path: "/m/b.en.srt", Language: "en"},
		{Path: "/m/c.en.srt", Language: "en"}, {Path: "/m/d.fr.srt", Language: "fr"},
	}}}}
	for _, dryRun := range []bool{false, true} {
		srv := fakebazarr.New(t, fakebazarr.Config{Library: lib})
		var asked []string
		cmd := deleteCommand
		cmd.skip = func(read bazarr.Library) func(bazarr.Subtitle) reporter.SkipReason {
			if n := len(read.Movies[0].Subtitles); n != 4 {
				t.Errorf("the rule got %d subtitles of the video, want the library as read (4)", n)
			}
			return func(s bazarr.Subtitle) reporter.SkipReason {
				asked = append(asked, s.Path)
				if s.Path == "/m/b.en.srt" {
					return "test reason"
				}
				return ""
			}
		}
		var out, errOut bytes.Buffer
		req := &invocation.Request{Selection: selection.Criteria{Languages: []string{"en"}},
			Change: invocation.ChangeOptions{Yes: true, DryRun: dryRun}}
		conn := bazarr.Connection{URL: srv.URL, APIKey: srv.APIKey(), Clock: fakeclock.New(t)}
		if err := runChanges(context.Background(), conn, req, reporter.New(&out, &errOut), cmd); err != nil {
			t.Fatalf("dry run %v: %v", dryRun, err)
		}
		if want := []string{"/m/a.en.srt", "/m/b.en.srt", "/m/c.en.srt"}; !slices.Equal(asked, want) {
			t.Errorf("dry run %v: rule asked for %q, want %q", dryRun, asked, want)
		}
		var deleted []string
		for _, r := range srv.Requests() {
			if r.Method == http.MethodDelete {
				deleted = append(deleted, r.Params().Get("path"))
			}
		}
		skipLine := strings.Contains(out.String(), "would skip  Sintel [movie 7]  en  /m/b.en.srt  (test reason)\n")
		if dryRun {
			if len(deleted) != 0 || !skipLine || !strings.Contains(out.String(), "would delete 2, skipped (test reason) 1") {
				t.Errorf("dry run: deleted %q, stdout %q", deleted, out.String())
			}
		} else if !slices.Equal(deleted, []string{"/m/a.en.srt", "/m/c.en.srt"}) || strings.Contains(out.String(), "b.en.srt") ||
			!strings.Contains(out.String(), "deleted 2, skipped (test reason) 1") {
			t.Errorf("deleted %q, stdout %q; want the skip without a line, counted", deleted, out.String())
		}
		if !strings.Contains(errOut.String(), "3 of 3 subtitles done") {
			t.Errorf("dry run %v: standard error %q, want the skip counted as done", dryRun, errOut.String())
		}
	}
}
