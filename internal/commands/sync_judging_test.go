package commands

import (
	"context"
	"path/filepath"
	"slices"
	"testing"

	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/bazarr"
	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/failurestore"
	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/fakebazarr"
	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/fakeclock"
	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/invocation"
	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/reporter"
	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/selection"
)

// judgeOne selects the subtitle at path from srv's library and judges it.
func judgeOne(t *testing.T, srv *fakebazarr.Server, path string, opts invocation.SyncOptions, store *failurestore.Store) judgment {
	t.Helper()
	clock := fakeclock.New(t)
	c, err := bazarr.Connect(context.Background(), bazarr.Connection{URL: srv.URL, APIKey: srv.APIKey(), Clock: clock})
	if err != nil {
		t.Fatal(err)
	}
	lib, _, err := c.Library(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	subs, err := selection.Select(selection.Criteria{}, lib)
	if err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(subs, func(s selection.Selected) bool { return s.Path == path })
	if i < 0 {
		t.Fatalf("%s not selected", path)
	}
	j, err := judge(context.Background(), c, clock, subs[i], opts, store)
	if err != nil {
		t.Fatal(err)
	}
	return j
}

// TestJudgeReference: F8's reference subtitle, from the video as read: not
// forced; the one that is not hearing-impaired when both exist; among
// several, the first path in byte order. Its path is the request's
// reference.
func TestJudgeReference(t *testing.T) {
	for _, tc := range []struct {
		name   string
		others []fakebazarr.Subtitle
		want   string
	}{
		{"HI and plain", []fakebazarr.Subtitle{{Path: "/m/a.en.hi.srt", Language: "en", HI: true}, {Path: "/m/z.en.srt", Language: "en"}}, "/m/z.en.srt"},
		{"two plain", []fakebazarr.Subtitle{{Path: "/m/b.en.srt", Language: "en"}, {Path: "/m/B.en.srt", Language: "en"}}, "/m/B.en.srt"},
		{"HI only", []fakebazarr.Subtitle{{Path: "/m/b.en.hi.srt", Language: "EN", HI: true}}, "/m/b.en.hi.srt"},
		{"forced only", []fakebazarr.Subtitle{{Path: "/m/b.en.srt", Language: "en", Forced: true}}, ""},
		{"embedded only", []fakebazarr.Subtitle{{Embedded: true, Language: "en"}}, ""},
	} {
		subs := append([]fakebazarr.Subtitle{{Path: "/m/x.es.srt", Language: "es"}}, tc.others...)
		srv := fakebazarr.New(t, fakebazarr.Config{Library: fakebazarr.Library{Movies: []fakebazarr.Movie{{RadarrID: 1, Title: "M", Subtitles: subs}}}})
		j := judgeOne(t, srv, "/m/x.es.srt", invocation.SyncOptions{ReferenceLanguage: "en", Bazarr: bazarr.SyncOptions{GSS: true}},
			failurestore.Load("", srv.URL))
		wantSkip := reporter.SkipReason("")
		if tc.want == "" {
			wantSkip = reporter.NoReference
		}
		if j.Options.Reference != tc.want || j.Skip != wantSkip || !j.Options.GSS {
			t.Errorf("%s: reference %q, skip %q, options %+v; want %q", tc.name, j.Options.Reference, j.Skip, j.Options, tc.want)
		}
	}
}

// TestJudgeHandsBack: besides its verdict the judging hands back the
// subtitle as now shown, the baseline (start time, identities of kinds 1
// to 6), its queue reads, and whether the record lapsed.
func TestJudgeHandsBack(t *testing.T) {
	srv := fakebazarr.New(t, fakebazarr.Config{Library: fakebazarr.Library{Movies: []fakebazarr.Movie{{RadarrID: 1, Title: "M",
		Subtitles: []fakebazarr.Subtitle{{Path: "/m/x.es.srt", Language: "es"}}}}}})
	srv.AddMovieEvent(1, fakebazarr.Event{Action: 1, Time: "11/20/26 10:00:00", Path: "/m/x.es.srt"})
	srv.AddMovieEvent(1, fakebazarr.Event{Action: 7, Time: "11/20/26 11:00:00", Path: "/m/x.es.srt"})
	srv.AddMovieEvent(1, fakebazarr.Event{Action: 4, Time: "11/21/26 10:00:00", Path: "/m/x.es.srt"})
	state := filepath.Join(t.TempDir(), "s.json")
	if err := failurestore.Load(state, srv.URL).Record("/m/x.es.srt", []string{"1 11/20/26 10:00:00"}); err != nil {
		t.Fatal(err)
	}
	j := judgeOne(t, srv, "/m/x.es.srt", invocation.SyncOptions{}, failurestore.Load(state, srv.URL))
	if !j.Shown || j.Subtitle.Title != "M" || j.Skip != "" || !j.Lapsed || len(j.Queues) != 1 ||
		j.Baseline.StartTime != fakebazarr.DefaultStartTime ||
		!slices.Equal(j.Baseline.Identities, []string{"4 11/21/26 10:00:00", "1 11/20/26 10:00:00"}) {
		t.Errorf("judgment %+v", j)
	}
	j = judgeOne(t, srv, "/m/x.es.srt", invocation.SyncOptions{}, failurestore.Load(filepath.Join(t.TempDir(), "none.json"), srv.URL))
	if j.Lapsed {
		t.Errorf("no record, yet lapsed")
	}
}
