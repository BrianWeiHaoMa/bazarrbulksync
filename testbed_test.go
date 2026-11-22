package main_test

import (
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/bazarr"
	"github.com/BrianWeiHaoMa/bazarrbulksync/testbed/bed"
)

// TestTestbed runs the built executable against the real Bazarr of the test
// bed (`go run ./testbed up`). It runs only when BAZARRBULKSYNC_TESTBED
// names the bed's file, so that `go test ./...` stays offline.
func TestTestbed(t *testing.T) {
	m, ok, err := bed.Load()
	if !ok {
		t.Skipf("no test bed: set %s to the file `go run ./testbed up` writes", bed.EnvVar)
	}
	if err != nil {
		t.Fatal(err)
	}
	shown := pathsInAnswer(t, m)

	t.Run("lines", func(t *testing.T) {
		r := runExecutable(t, m.URL, m.APIKey, "list")
		if r.code != 0 || r.stderr != "" {
			t.Fatalf("code %d, stderr %q", r.code, r.stderr)
		}
		lines, _, _ := strings.Cut(r.stdout, "\n\n")
		var printed []string
		for _, l := range strings.Split(lines, "\n") {
			i := slices.IndexFunc(m.Subtitles, func(s bazarr.Subtitle) bool { return strings.HasSuffix(l, "  "+s.Path) })
			if i < 0 {
				t.Errorf("line %q names no subtitle the bed placed", l)
				continue
			}
			s := m.Subtitles[i]
			printed = append(printed, s.Path)
			id := fmt.Sprintf("[movie %d]", s.MovieID)
			if s.Type == bazarr.TypeEpisode {
				id = fmt.Sprintf("[series %d] S%02dE%02d", s.SeriesID, s.Season, s.Episode)
			}
			if named := strings.TrimSuffix(l, "  "+s.Path); !strings.HasPrefix(named, s.Title+" "+id) {
				t.Errorf("line %q does not open with the title and %s", l, id)
			}
		}
		if !sameSet(printed, shown) || !sameSet(printed, paths(m.Subtitles)) {
			t.Errorf("lines for %v,\nwant exactly one for each placed file %v,\nwhich Bazarr shows as %v", printed, paths(m.Subtitles), shown)
		}
		for _, video := range m.Embedded {
			if strings.Contains(lines, video) {
				t.Errorf("a line names %s, the video with only an embedded track", video)
			}
		}
		if !strings.Contains(r.stdout, fmt.Sprintf("Total: %d subtitles", len(m.Subtitles))) {
			t.Errorf("totals do not count %d subtitles:\n%s", len(m.Subtitles), r.stdout)
		}
	})

	t.Run("json", func(t *testing.T) {
		got := listJSON(t, m)
		var printed []string
		for _, obj := range got {
			printed = append(printed, obj.Path)
			i := slices.IndexFunc(m.Subtitles, func(s bazarr.Subtitle) bool { return s.Path == obj.Path })
			if i < 0 {
				t.Errorf("object for %s, which the bed did not place", obj.Path)
				continue
			}
			if want := objectOf(m.Subtitles[i]); !equalObjects(obj, want) {
				t.Errorf("object %+v, want %+v", obj, want)
			}
		}
		if !sameSet(printed, shown) || len(printed) != len(m.Subtitles) {
			t.Errorf("objects for %v, want one for each path Bazarr shows %v", printed, shown)
		}
	})

	// Each selection option selects what the bed's library predicts.
	movieID, seriesID := m.Movies[1].ID, m.Series[0].ID
	predictions := []struct {
		args    []string
		matches func(bazarr.Subtitle) bool
	}{
		{[]string{"--language", "ES"}, func(s bazarr.Subtitle) bool { return s.Language == "es" }},
		{[]string{"--movies"}, func(s bazarr.Subtitle) bool { return s.Type == bazarr.TypeMovie }},
		{[]string{"--series"}, func(s bazarr.Subtitle) bool { return s.Type == bazarr.TypeEpisode }},
		{[]string{"--season", "2"}, func(s bazarr.Subtitle) bool { return s.Type == bazarr.TypeEpisode && s.Season == 2 }},
		{[]string{"--title", "STEEL"}, func(s bazarr.Subtitle) bool { return s.Title == "Tears of Steel" }},
		{[]string{"--movie-id", strconv.Itoa(movieID)}, func(s bazarr.Subtitle) bool { return s.Type == bazarr.TypeMovie && s.MovieID == movieID }},
		{[]string{"--series-id", strconv.Itoa(seriesID)}, func(s bazarr.Subtitle) bool { return s.Type == bazarr.TypeEpisode && s.SeriesID == seriesID }},
		{[]string{"--hi"}, func(s bazarr.Subtitle) bool { return s.HI }},
		{[]string{"--no-hi"}, func(s bazarr.Subtitle) bool { return !s.HI }},
		{[]string{"--forced"}, func(s bazarr.Subtitle) bool { return s.Forced }},
		{[]string{"--no-forced"}, func(s bazarr.Subtitle) bool { return !s.Forced }},
	}
	for _, p := range predictions {
		t.Run(strings.Join(p.args, " "), func(t *testing.T) {
			var want []string
			for _, s := range m.Subtitles {
				if p.matches(s) {
					want = append(want, s.Path)
				}
			}
			if len(want) == 0 || len(want) == len(m.Subtitles) {
				t.Fatalf("the bed's library does not tell this option apart: %d of %d subtitles", len(want), len(m.Subtitles))
			}
			var printed []string
			for _, obj := range listJSON(t, m, p.args...) {
				printed = append(printed, obj.Path)
			}
			if !sameSet(printed, want) {
				t.Errorf("selected %v, want %v", printed, want)
			}
		})
	}

	t.Run("wrong address or key", func(t *testing.T) {
		for name, c := range map[string]struct{ url, key string }{
			"nothing listens": {"http://127.0.0.1:1", m.APIKey},
			"wrong base path": {m.URL + "/nowhere", m.APIKey},
			"wrong key":       {m.URL, "0123456789abcdef0123456789abcdef"},
		} {
			r := runExecutable(t, c.url, c.key, "list")
			if r.code == 0 || r.stdout != "" || !strings.Contains(r.stderr, c.url) {
				t.Errorf("%s: code %d, stdout %q, stderr %q; want the address named on standard error, nonzero (G2)", name, r.code, r.stdout, r.stderr)
			}
		}
	})
}

// object is one object `list --json` prints.
type object struct {
	Type      string `json:"type"`
	Title     string `json:"title"`
	MovieID   *int   `json:"movie_id"`
	SeriesID  *int   `json:"series_id"`
	EpisodeID *int   `json:"episode_id"`
	Season    *int   `json:"season"`
	Episode   *int   `json:"episode"`
	Language  string `json:"language"`
	HI        bool   `json:"hi"`
	Forced    bool   `json:"forced"`
	Path      string `json:"path"`
	fields    []string
}

// listJSON runs `list --json` with args and decodes each line, keeping the
// field names it held.
func listJSON(t *testing.T, m bed.Manifest, args ...string) []object {
	t.Helper()
	r := runExecutable(t, m.URL, m.APIKey, append([]string{"list", "--json"}, args...)...)
	if r.code != 0 || r.stderr != "" {
		t.Fatalf("list --json %v: code %d, stderr %q", args, r.code, r.stderr)
	}
	var objs []object
	for _, l := range strings.Split(strings.TrimSuffix(r.stdout, "\n"), "\n") {
		var obj object
		var raw map[string]json.RawMessage
		if err := json.Unmarshal([]byte(l), &obj); err != nil {
			t.Fatalf("line %q: %v", l, err)
		}
		if err := json.Unmarshal([]byte(l), &raw); err != nil {
			t.Fatalf("line %q: %v", l, err)
		}
		for k := range raw {
			obj.fields = append(obj.fields, k)
		}
		slices.Sort(obj.fields)
		objs = append(objs, obj)
	}
	return objs
}

// objectOf is the object F7 describes for s.
func objectOf(s bazarr.Subtitle) object {
	o := object{Type: string(s.Type), Title: s.Title, Language: s.Language, HI: s.HI, Forced: s.Forced, Path: s.Path,
		fields: []string{"episode", "episode_id", "forced", "hi", "language", "movie_id", "path", "season", "series_id", "title", "type"}}
	if s.Type == bazarr.TypeMovie {
		o.MovieID = &s.MovieID
	} else {
		o.SeriesID, o.EpisodeID, o.Season, o.Episode = &s.SeriesID, &s.EpisodeID, &s.Season, &s.Episode
	}
	return o
}

func equalObjects(a, b object) bool {
	same := func(x, y *int) bool { return (x == nil) == (y == nil) && (x == nil || *x == *y) }
	return a.Type == b.Type && a.Title == b.Title && a.Language == b.Language && a.HI == b.HI && a.Forced == b.Forced &&
		a.Path == b.Path && same(a.MovieID, b.MovieID) && same(a.SeriesID, b.SeriesID) && same(a.EpisodeID, b.EpisodeID) &&
		same(a.Season, b.Season) && same(a.Episode, b.Episode) && slices.Equal(a.fields, b.fields)
}

func paths(subs []bazarr.Subtitle) []string {
	var out []string
	for _, s := range subs {
		out = append(out, s.Path)
	}
	return out
}

// sameSet reports whether a and b hold the same paths, each once.
func sameSet(a, b []string) bool {
	a, b = slices.Clone(a), slices.Clone(b)
	slices.Sort(a)
	slices.Sort(b)
	return slices.Equal(a, b) && len(slices.Compact(a)) == len(b)
}

// pathsInAnswer returns every subtitle path that is not null in Bazarr's
// own answer.
func pathsInAnswer(t *testing.T, m bed.Manifest) []string {
	t.Helper()
	videos, err := m.Shown()
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, v := range videos {
		for _, p := range v.Subtitles {
			if p != nil {
				out = append(out, *p)
			}
		}
	}
	return out
}
