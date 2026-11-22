package main_test

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/bazarr"
	"github.com/BrianWeiHaoMa/bazarrbulksync/testbed/bed"
)

// TestTestbed runs the product (testbed_host_test.go) against the real Bazarr of the test
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
		r := bedRun(t, m.URL, m.APIKey, "list")
		if r.code != 0 || r.stderr != "" {
			t.Fatalf("code %d, stderr %q", r.code, r.stderr)
		}
		lines, _, _ := strings.Cut(r.stdout, "\n\n")
		var printed []string
		seen := map[bazarr.Subtitle]bool{}
		for _, l := range strings.Split(lines, "\n") {
			// A file holding two episodes is two subtitles of one path, told
			// apart by the episode the line opens with.
			i := slices.IndexFunc(m.Subtitles, func(s bazarr.Subtitle) bool {
				return strings.HasSuffix(l, "  "+s.Path) && strings.HasPrefix(l, s.Title+" "+videoOf(s)+"  ")
			})
			if i < 0 {
				t.Errorf("line %q names no subtitle the bed placed, opening with its title and video", l)
				continue
			}
			if s := m.Subtitles[i]; seen[s] {
				t.Errorf("a second line for %s of %s", s.Path, videoOf(s))
			} else {
				seen[s] = true
				printed = append(printed, s.Path)
			}
		}
		if !samePaths(printed, shown) || !samePaths(printed, paths(m.Subtitles)) {
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
		printed := placedListed(t, m)
		if !samePaths(paths(printed), shown) || !sameSubtitles(printed, m.Subtitles) {
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
			var want []bazarr.Subtitle
			for _, s := range m.Subtitles {
				if p.matches(s) {
					want = append(want, s)
				}
			}
			if len(want) == 0 || len(want) == len(m.Subtitles) {
				t.Fatalf("the bed's library does not tell this option apart: %d of %d subtitles", len(want), len(m.Subtitles))
			}
			if printed := placedListed(t, m, p.args...); !sameSubtitles(printed, want) {
				t.Errorf("selected %v, want %v", printed, want)
			}
		})
	}

	t.Run("second server", func(t *testing.T) {
		first, second := listJSON(t, m), listJSON(t, m.OnSecond())
		if !slices.EqualFunc(first, second, equalObjects) {
			t.Errorf("the second Bazarr lists\n%+v\nthe first\n%+v\nwant the same library at the same paths", second, first)
		}
		if m.Second.APIKey == m.APIKey || m.Second.URL == m.URL {
			t.Errorf("the second Bazarr shares the first's key or address")
		}
	})

	t.Run("wrong address or key", func(t *testing.T) {
		for name, c := range map[string]struct{ url, key string }{
			"nothing listens": {"http://127.0.0.1:1", m.APIKey},
			"wrong base path": {m.URL + "/nowhere", m.APIKey},
			"wrong key":       {m.URL, "0123456789abcdef0123456789abcdef"},
		} {
			r := bedRun(t, c.url, c.key, "list")
			if r.code == 0 || r.stdout != "" || !strings.Contains(r.stderr, c.url) {
				t.Errorf("%s: code %d, stdout %q, stderr %q; want the address named on standard error, nonzero (G2)", name, r.code, r.stdout, r.stderr)
			}
		}
	})

	// A run reaches the product with each argument as given, wherever it
	// runs (G8).
	t.Run("title as given", func(t *testing.T) {
		const title = `Tears of Steel's "director's cut"`
		r := bedRun(t, m.URL, m.APIKey, "list", "--title", title)
		if r.code == 0 || r.stdout != "" || !strings.Contains(r.stderr, strconv.Quote(title)) {
			t.Errorf("code %d, stdout %q, stderr %q; want %s named on standard error, nonzero (G8)", r.code, r.stdout, r.stderr, strconv.Quote(title))
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

// listJSON runs `list --json` with args and decodes what it printed.
func listJSON(t *testing.T, m bed.Manifest, args ...string) []object {
	t.Helper()
	r := bedRun(t, m.URL, m.APIKey, append([]string{"list", "--json"}, args...)...)
	if r.code != 0 || r.stderr != "" {
		t.Fatalf("list --json %v: code %d, stderr %q", args, r.code, r.stderr)
	}
	return decodeObjects(t, r.stdout)
}

// decodeObjects decodes each line `list --json` printed, keeping the field
// names it held.
func decodeObjects(t *testing.T, stdout string) []object {
	t.Helper()
	var objs []object
	for _, l := range strings.Split(strings.TrimSuffix(stdout, "\n"), "\n") {
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
		for _, k := range []string{"hi", "forced"} {
			if v := string(raw[k]); v != "true" && v != "false" {
				t.Errorf("line %q: %s is %s, want true or false (F7)", l, k, v)
			}
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

// samePaths reports whether a and b hold the same paths, each as often:
// once, or once per episode of a file holding several.
func samePaths(a, b []string) bool {
	a, b = slices.Clone(a), slices.Clone(b)
	slices.Sort(a)
	slices.Sort(b)
	return slices.Equal(a, b)
}

// videoOf is how `list` names s's video after its title.
func videoOf(s bazarr.Subtitle) string {
	if s.Type == bazarr.TypeEpisode {
		return fmt.Sprintf("[series %d] S%02dE%02d", s.SeriesID, s.Season, s.Episode)
	}
	return fmt.Sprintf("[movie %d]", s.MovieID)
}

// placedAt returns the subtitles the bed placed at path: one, or one per
// episode of a file holding several.
func placedAt(m bed.Manifest, path string) []bazarr.Subtitle {
	var out []bazarr.Subtitle
	for _, s := range m.Subtitles {
		if s.Path == path {
			out = append(out, s)
		}
	}
	return out
}

// sameSubtitles reports whether a and b hold the same subtitles, each as
// often.
func sameSubtitles(a, b []bazarr.Subtitle) bool {
	order := func(x, y bazarr.Subtitle) int { return strings.Compare(fmt.Sprintf("%+v", x), fmt.Sprintf("%+v", y)) }
	a, b = slices.Clone(a), slices.Clone(b)
	slices.SortFunc(a, order)
	slices.SortFunc(b, order)
	return slices.Equal(a, b)
}

// placedListed returns, for each object `list --json` with args prints, the
// subtitle the bed placed that it is, failing the check on an object that is
// none of them.
func placedListed(t *testing.T, m bed.Manifest, args ...string) []bazarr.Subtitle {
	t.Helper()
	var out []bazarr.Subtitle
	for _, obj := range listJSON(t, m, args...) {
		s, ok := placedAs(m, obj)
		if !ok {
			t.Fatalf("list --json %v printed %+v, which is none of the subtitles the bed placed at its path %+v", args, obj, placedAt(m, obj.Path))
		}
		out = append(out, s)
	}
	return out
}

// placedAs returns the subtitle the bed placed whose F7 object is obj.
func placedAs(m bed.Manifest, obj object) (bazarr.Subtitle, bool) {
	for _, s := range placedAt(m, obj.Path) {
		if equalObjects(objectOf(s), obj) {
			return s, true
		}
	}
	return bazarr.Subtitle{}, false
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

// changingSets are the switches, each beside bed.EnvVar, of the sets of
// checks that change the bed. Each set needs a fresh bed, so no two run in
// one invocation; a new set adds its switch here.
var changingSets = []string{changesVar, syncVar, pathsVar, outagesVar, imageVar, platformVar}

// changingBed loads the bed for the changing set whose switch is mine. It
// skips without the bed or without that switch, and fails while another
// changing set's switch is set too.
func changingBed(t *testing.T, mine string) bed.Manifest {
	t.Helper()
	m, ok, err := bed.Load()
	if !ok {
		t.Skipf("no test bed: set %s to the file `go run ./testbed up` writes", bed.EnvVar)
	}
	if err != nil {
		t.Fatal(err)
	}
	if os.Getenv(mine) != "1" {
		t.Skipf("these checks change the bed: set %s=1 to run them", mine)
	}
	for _, other := range changingSets {
		if other != mine && os.Getenv(other) == "1" {
			t.Fatalf("%s and %s are both set: each set of checks that changes the bed needs a fresh bed, so run them apart", mine, other)
		}
	}
	return m
}
