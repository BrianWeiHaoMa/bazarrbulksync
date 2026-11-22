package commands_test

import (
	"bytes"
	"context"
	"encoding/json"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/fakebazarr"
	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/fakeclock"
)

// library holds a movie with an HI, a forced, and an embedded subtitle, a
// movie with none, and a series whose episodes include a special (season 0)
// and carry their own titles, which `list` must not show.
var library = fakebazarr.Library{
	Movies: []fakebazarr.Movie{
		{RadarrID: 7, Title: "Sintel", Subtitles: []fakebazarr.Subtitle{
			{Path: "/movies/m7/film.en.srt", Language: "en"},
			{Path: "/movies/m7/film.en.hi.srt", Language: "en", HI: true},
			{Path: "/movies/m7/film.fr.forced.srt", Language: "fr", Forced: true},
			{Embedded: true, Language: "de"},
		}},
		{RadarrID: 8, Title: "Big Buck Bunny"},
	},
	Series: []fakebazarr.Series{
		{SonarrSeriesID: 3, Title: "Fawlty Towers", Episodes: []fakebazarr.Episode{
			{SonarrEpisodeID: 101, Season: 1, Episode: 2, Title: "The Builders", Subtitles: []fakebazarr.Subtitle{
				{Path: "/tv/s3/Season 01/S01E02.de.srt", Language: "de"},
			}},
			{SonarrEpisodeID: 102, Season: 0, Episode: 1, Title: "Special", Subtitles: []fakebazarr.Subtitle{
				{Path: "/tv/s3/Specials/S00E01.en.srt", Language: "en"},
			}},
		}},
	},
}

// lineOf returns the one line of out that holds path.
func lineOf(t *testing.T, out, path string) string {
	t.Helper()
	var found []string
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, path) {
			found = append(found, l)
		}
	}
	if len(found) != 1 {
		t.Fatalf("%d lines hold %s, want 1, in:\n%s", len(found), path, out)
	}
	return found[0]
}

// hasWord reports whether word is one of line's space-separated words.
func hasWord(line, word string) bool { return slices.Contains(strings.Fields(line), word) }

func TestListLines(t *testing.T) {
	srv := fakebazarr.New(t, fakebazarr.Config{Library: library})
	o := runAgainst(t, srv, "list")
	if o.code != 0 || o.stderr != "" {
		t.Fatalf("code %d, stderr %q; want 0 and nothing on standard error", o.code, o.stderr)
	}
	plain := lineOf(t, o.stdout, "/movies/m7/film.en.srt")
	hi := lineOf(t, o.stdout, "/movies/m7/film.en.hi.srt")
	forced := lineOf(t, o.stdout, "/movies/m7/film.fr.forced.srt")
	episode := lineOf(t, o.stdout, "/tv/s3/Season 01/S01E02.de.srt")
	special := lineOf(t, o.stdout, "/tv/s3/Specials/S00E01.en.srt")

	for _, l := range []string{plain, hi, forced} {
		if !strings.Contains(l, "Sintel") || !strings.Contains(l, " 7]") {
			t.Errorf("%q: want the title and the movie ID 7", l)
		}
	}
	if !hasWord(plain, "en") || !hasWord(hi, "en") || !hasWord(forced, "fr") {
		t.Errorf("language codes missing in %q, %q, %q", plain, hi, forced)
	}
	// HI and forced are each told apart from the other and from neither.
	if hasWord(plain, "HI") || hasWord(plain, "forced") {
		t.Errorf("%q marks a plain subtitle HI or forced", plain)
	}
	if !hasWord(hi, "HI") || hasWord(hi, "forced") {
		t.Errorf("%q: want HI and not forced", hi)
	}
	if !hasWord(forced, "forced") || hasWord(forced, "HI") {
		t.Errorf("%q: want forced and not HI", forced)
	}
	// An episode's line: the series' title and ID, season and episode, never
	// the episode's own title or ID.
	if !strings.Contains(episode, "Fawlty Towers") || !strings.Contains(episode, "S01E02") || !hasWord(episode, "de") {
		t.Errorf("%q: want the series title, S01E02, and de", episode)
	}
	if strings.Contains(episode, "The Builders") || strings.Contains(episode, "101") {
		t.Errorf("%q shows the episode's own title or ID", episode)
	}
	if !strings.Contains(episode, " 3]") {
		t.Errorf("%q: want the series ID 3", episode)
	}
	if !strings.Contains(special, "S00E01") {
		t.Errorf("%q: want S00E01", special)
	}
	if strings.Contains(o.stdout, "Big Buck Bunny") {
		t.Errorf("a movie with no external subtitle is listed:\n%s", o.stdout)
	}

	// The ID shown selects the line again.
	again := runAgainst(t, srv, "list", "--series-id", "3")
	if again.code != 0 || lineOf(t, again.stdout, "S01E02.de.srt") != episode {
		t.Errorf("list --series-id 3 does not print %q again:\n%s", episode, again.stdout)
	}
}

func TestListTotals(t *testing.T) {
	srv := fakebazarr.New(t, fakebazarr.Config{Library: library})
	o := runAgainst(t, srv, "list")
	if o.code != 0 {
		t.Fatalf("code %d, stderr %q", o.code, o.stderr)
	}
	lines := strings.Split(strings.TrimRight(o.stdout, "\n"), "\n")
	subtitleLines := 0
	for _, l := range lines {
		if strings.Contains(l, "/movies/") || strings.Contains(l, "/tv/") {
			subtitleLines++
		}
	}
	if subtitleLines != 5 {
		t.Errorf("%d subtitle lines, want 5 (no embedded track):\n%s", subtitleLines, o.stdout)
	}
	totals := strings.Join(lines[subtitleLines:], "\n")
	// en: the plain, the HI, and the special; fr: the forced one; de: the episode.
	for _, want := range []string{"Total: 5 subtitles", "de 1", "en 3", "fr 1"} {
		if !strings.Contains(totals, want) {
			t.Errorf("totals %q lack %q", totals, want)
		}
	}
	sum := 0
	for _, f := range strings.FieldsFunc(strings.TrimPrefix(totals[strings.Index(totals, "By language:"):], "By language:"), func(r rune) bool { return r == ',' }) {
		parts := strings.Fields(f)
		n, err := strconv.Atoi(parts[len(parts)-1])
		if err != nil {
			t.Fatalf("count %q: %v", f, err)
		}
		sum += n
	}
	if sum != 5 {
		t.Errorf("the per-language counts add up to %d, want the total 5", sum)
	}
}

func TestListJSON(t *testing.T) {
	srv := fakebazarr.New(t, fakebazarr.Config{Library: library})
	o := runAgainst(t, srv, "list", "--json")
	if o.code != 0 || o.stderr != "" {
		t.Fatalf("code %d, stderr %q", o.code, o.stderr)
	}
	lines := strings.Split(strings.TrimSuffix(o.stdout, "\n"), "\n")
	if len(lines) != 5 {
		t.Fatalf("%d lines, want one object per subtitle and nothing else:\n%s", len(lines), o.stdout)
	}
	byPath := map[string]map[string]any{}
	for _, l := range lines {
		var obj map[string]any
		dec := json.NewDecoder(bytes.NewReader([]byte(l)))
		dec.UseNumber()
		if err := dec.Decode(&obj); err != nil || dec.More() {
			t.Fatalf("line %q is not exactly one JSON object: %v", l, err)
		}
		keys := make([]string, 0, len(obj))
		for k := range obj {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		want := []string{"episode", "episode_id", "forced", "hi", "language", "movie_id", "path", "season", "series_id", "title", "type"}
		if !slices.Equal(keys, want) {
			t.Errorf("fields %v, want exactly %v (F7)", keys, want)
		}
		byPath[obj["path"].(string)] = obj
	}
	check := func(path string, want map[string]any) {
		t.Helper()
		obj := byPath[path]
		for k, v := range want {
			got := obj[k]
			if n, ok := got.(json.Number); ok {
				got = n.String()
			}
			if got != v {
				t.Errorf("%s: %s is %#v, want %#v", path, k, got, v)
			}
		}
	}
	check("/movies/m7/film.en.hi.srt", map[string]any{"type": "movie", "title": "Sintel", "movie_id": "7",
		"series_id": nil, "episode_id": nil, "season": nil, "episode": nil, "language": "en", "hi": true, "forced": false})
	check("/movies/m7/film.fr.forced.srt", map[string]any{"hi": false, "forced": true, "language": "fr"})
	check("/tv/s3/Season 01/S01E02.de.srt", map[string]any{"type": "episode", "title": "Fawlty Towers",
		"movie_id": nil, "series_id": "3", "episode_id": "101", "season": "1", "episode": "2", "hi": false, "forced": false})
	check("/tv/s3/Specials/S00E01.en.srt", map[string]any{"season": "0", "episode": "1"})
}

func TestEmptySelection(t *testing.T) {
	srv := fakebazarr.New(t, fakebazarr.Config{Library: library})
	for _, args := range [][]string{
		{"list", "--language", "xx"}, {"list", "--language", "xx", "--json"}, {"list", "--movie-id", "8"},
	} {
		o := runAgainst(t, srv, args...)
		if o.code != 0 || o.stdout != "" || strings.TrimSpace(o.stderr) == "" {
			t.Errorf("%q: code %d, stdout %q, stderr %q; want exit 0, the message on standard error only (G9)", args, o.code, o.stdout, o.stderr)
		}
	}
}

func TestFailures(t *testing.T) {
	srv := fakebazarr.New(t, fakebazarr.Config{Library: library})
	for args, want := range map[string]string{
		"list --title nosuch":                 "nosuch",
		"list --title sintel --title Nowhere": "Nowhere",
		"list --movie-id 99":                  "99",
		"list --series-id 3 --series-id 41":   "41",
	} {
		o := runAgainst(t, srv, strings.Fields(args)...)
		failed(t, args, o)
		if !strings.Contains(o.stderr, want) {
			t.Errorf("%s: standard error %q does not name %q (G8)", args, o.stderr, want)
		}
	}
	// A key pasted as a title is never printed (R2), nor is the
	// environment's key when the option overrides it.
	o := runAgainst(t, srv, "list", "--title", srv.APIKey())
	failed(t, "key as title", o)
	if strings.Contains(o.stderr, srv.APIKey()) {
		t.Errorf("standard error %q holds the API key", o.stderr)
	}
	const envKey = "environment-key-3d8f2b61"
	o = run(context.Background(), fakeclock.New(t), map[string]string{"BAZARR_URL": srv.URL, "BAZARR_API_KEY": envKey},
		"list", "--api-key", srv.APIKey(), "--title", envKey)
	failed(t, "overridden key as title", o)
	if strings.Contains(o.stderr, envKey) {
		t.Errorf("standard error %q holds the environment's API key", o.stderr)
	}
}
