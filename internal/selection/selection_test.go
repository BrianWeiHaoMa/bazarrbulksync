package selection_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/bazarr"
	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/selection"
)

func movieSub(id int, title, lang, path string, hi, forced bool) bazarr.Subtitle {
	return bazarr.Subtitle{Type: bazarr.TypeMovie, MovieID: id, Title: title, Language: lang, HI: hi, Forced: forced, Path: path}
}

func episodeSub(seriesID, episodeID int, title string, season, episode int, lang, path string, hi, forced bool) bazarr.Subtitle {
	return bazarr.Subtitle{Type: bazarr.TypeEpisode, SeriesID: seriesID, EpisodeID: episodeID, Title: title,
		Season: season, Episode: episode, Language: lang, HI: hi, Forced: forced, Path: path}
}

// The library every test selects from, in the order and shape D01's client
// hands it out: episodes as Bazarr lists them, newest first.
var (
	sintelEN     = movieSub(10, "Sintel", "en", "/movies/Sintel/Sintel.en.srt", false, false)
	sintelENHI   = movieSub(10, "Sintel", "en", "/movies/Sintel/Sintel.en.hi.srt", true, false)
	sintelES     = movieSub(10, "Sintel", "es", "/movies/Sintel/Sintel.es.srt", false, false)
	tearsDEForce = movieSub(3, "Tears of Steel", "de", "/movies/Tears/Tears.de.forced.srt", false, true)

	fawltyS01E01EN = episodeSub(7, 701, "Fawlty Towers", 1, 1, "en", "/tv/Fawlty/S01E01.en.srt", false, false)
	fawltyS01E02FR = episodeSub(7, 702, "Fawlty Towers", 1, 2, "fr", "/tv/Fawlty/S01E02.fr.srt", false, false)
	fawltyS02E01EN = episodeSub(7, 703, "Fawlty Towers", 2, 1, "en", "/tv/Fawlty/S02E01.en.hi.srt", true, false)
	othersS00E01IT = episodeSub(4, 401, "The Others", 0, 1, "it", "/tv/Others/S00E01.it.srt", false, false)

	library = bazarr.Library{
		Movies: []bazarr.Movie{
			{ID: 10, Title: "Sintel", Subtitles: []bazarr.Subtitle{sintelEN, sintelENHI, sintelES}},
			{ID: 1, Title: "Big Buck Bunny"}, // no external subtitle
			{ID: 3, Title: "Tears of Steel", Subtitles: []bazarr.Subtitle{tearsDEForce}},
		},
		Series: []bazarr.Series{
			{ID: 7, Title: "Fawlty Towers", Episodes: []bazarr.Episode{
				{ID: 703, Season: 2, Number: 1, Subtitles: []bazarr.Subtitle{fawltyS02E01EN}},
				{ID: 702, Season: 1, Number: 2, Subtitles: []bazarr.Subtitle{fawltyS01E02FR}},
				{ID: 701, Season: 1, Number: 1, Subtitles: []bazarr.Subtitle{fawltyS01E01EN}},
			}},
			{ID: 4, Title: "The Others", Episodes: []bazarr.Episode{
				{ID: 401, Season: 0, Number: 1, Subtitles: []bazarr.Subtitle{othersS00E01IT}},
				{ID: 402, Season: 1, Number: 1}, // an episode without subtitles
			}},
			{ID: 9, Title: "Empty Show"}, // a series with no subtitle at all
		},
	}
)

func selectOK(t *testing.T, c selection.Criteria) []bazarr.Subtitle {
	t.Helper()
	got, err := selection.Select(c, library)
	if err != nil {
		t.Fatalf("Select(%+v): %v", c, err)
	}
	return subtitlesOf(got)
}

func subtitlesOf(sel []selection.Selected) []bazarr.Subtitle {
	subs := make([]bazarr.Subtitle, len(sel))
	for i, s := range sel {
		subs[i] = s.Subtitle
	}
	return subs
}

func paths(subs []bazarr.Subtitle) []string {
	var p []string
	for _, s := range subs {
		p = append(p, s.Path)
	}
	return p
}

func expect(t *testing.T, name string, c selection.Criteria, want ...bazarr.Subtitle) {
	t.Helper()
	got := selectOK(t, c)
	if !reflect.DeepEqual(paths(got), paths(want)) {
		t.Errorf("%s: selected\n  %q\nwant\n  %q", name, paths(got), paths(want))
	}
}

func TestIdentityAnyOf(t *testing.T) {
	expect(t, "title of a movie with ID of a series",
		selection.Criteria{Titles: []string{"sintel"}, SeriesIDs: []int{7}},
		sintelENHI, sintelEN, sintelES, fawltyS01E01EN, fawltyS01E02FR, fawltyS02E01EN)
	expect(t, "the same, English only",
		selection.Criteria{Titles: []string{"sintel"}, SeriesIDs: []int{7}, Languages: []string{"en"}},
		sintelENHI, sintelEN, fawltyS01E01EN, fawltyS02E01EN)
	expect(t, "a movie ID with a series title",
		selection.Criteria{MovieIDs: []int{3}, Titles: []string{"others"}},
		tearsDEForce, othersS00E01IT)
	expect(t, "two movie IDs", selection.Criteria{MovieIDs: []int{3, 10}},
		sintelENHI, sintelEN, sintelES, tearsDEForce)
	// Radarr and Sonarr number independently: a movie ID never selects a
	// series with that number, nor the reverse.
	expect(t, "movie ID equal to a series ID", selection.Criteria{MovieIDs: []int{10}, SeriesIDs: []int{4}},
		sintelENHI, sintelEN, sintelES, othersS00E01IT)
}

func TestKinds(t *testing.T) {
	all := []bazarr.Subtitle{sintelENHI, sintelEN, sintelES, tearsDEForce,
		fawltyS01E01EN, fawltyS01E02FR, fawltyS02E01EN, othersS00E01IT}
	expect(t, "no option", selection.Criteria{}, all...)
	expect(t, "--movies", selection.Criteria{Type: bazarr.TypeMovie}, sintelENHI, sintelEN, sintelES, tearsDEForce)
	expect(t, "--series", selection.Criteria{Type: bazarr.TypeEpisode},
		fawltyS01E01EN, fawltyS01E02FR, fawltyS02E01EN, othersS00E01IT)
	expect(t, "--hi", selection.Criteria{HI: selection.Only}, sintelENHI, fawltyS02E01EN)
	expect(t, "--no-hi", selection.Criteria{HI: selection.Without},
		sintelEN, sintelES, tearsDEForce, fawltyS01E01EN, fawltyS01E02FR, othersS00E01IT)
	expect(t, "--forced", selection.Criteria{Forced: selection.Only}, tearsDEForce)
	expect(t, "--no-forced", selection.Criteria{Forced: selection.Without},
		sintelENHI, sintelEN, sintelES, fawltyS01E01EN, fawltyS01E02FR, fawltyS02E01EN, othersS00E01IT)
	expect(t, "--season 1 keeps no movie", selection.Criteria{Seasons: []int{1}}, fawltyS01E01EN, fawltyS01E02FR)
	expect(t, "--season 0", selection.Criteria{Seasons: []int{0}}, othersS00E01IT)
	expect(t, "--season 2 --season 0", selection.Criteria{Seasons: []int{2, 0}}, fawltyS02E01EN, othersS00E01IT)
	expect(t, "--season with --movies", selection.Criteria{Seasons: []int{1}, Type: bazarr.TypeMovie})
	expect(t, "--language EN ignores case", selection.Criteria{Languages: []string{"EN"}},
		sintelENHI, sintelEN, fawltyS01E01EN, fawltyS02E01EN)
	expect(t, "--language es --language Fr", selection.Criteria{Languages: []string{"es", "Fr"}}, sintelES, fawltyS01E02FR)
	expect(t, "--language en --no-hi --series",
		selection.Criteria{Languages: []string{"en"}, HI: selection.Without, Type: bazarr.TypeEpisode}, fawltyS01E01EN)
}

func TestTitle(t *testing.T) {
	expect(t, "part of a title, other case", selection.Criteria{Titles: []string{"TOWER"}},
		fawltyS01E01EN, fawltyS01E02FR, fawltyS02E01EN)
	expect(t, "two titles", selection.Criteria{Titles: []string{"of st", "OTHERS"}}, tearsDEForce, othersS00E01IT)
	greek := movieSub(8, "ΟΔΥΣΣΕΎΣ", "el", "/movies/Odysseus/Odysseus.el.srt", false, false)
	greekLib := bazarr.Library{Movies: []bazarr.Movie{{ID: 8, Title: "ΟΔΥΣΣΕΎΣ", Subtitles: []bazarr.Subtitle{greek}}}}
	if got, err := selection.Select(selection.Criteria{Titles: []string{"Οδυσσεύς"}}, greekLib); err != nil || len(got) != 1 {
		t.Errorf("a final sigma against a capital one: selected %q, err %v; want the movie", paths(subtitlesOf(got)), err)
	}
	lib := bazarr.Library{Series: []bazarr.Series{{ID: 1, Title: "Show", Episodes: []bazarr.Episode{
		{ID: 11, Season: 1, Number: 1, Subtitles: []bazarr.Subtitle{episodeSub(1, 11, "Show", 1, 1, "en", "/a.srt", false, false)}},
	}}}}
	// The library carries no episode's own title (bazarr.Episode has none),
	// so an episode title such as "Pilot" can match nothing.
	_, err := selection.Select(selection.Criteria{Titles: []string{"Pilot"}}, lib)
	var nf *selection.NotFoundError
	if !errors.As(err, &nf) || !reflect.DeepEqual(nf.Titles, []string{"Pilot"}) {
		t.Errorf("a text in no movie or series title: err %v, want it named", err)
	}
}

func TestNotFound(t *testing.T) {
	for _, tc := range []struct {
		name string
		c    selection.Criteria
		want selection.NotFoundError
	}{
		{"a bad title beside a good one", selection.Criteria{Titles: []string{"sintel", "nosuch"}},
			selection.NotFoundError{Titles: []string{"nosuch"}}},
		{"an unknown movie ID beside a known one", selection.Criteria{MovieIDs: []int{10, 99}},
			selection.NotFoundError{MovieIDs: []int{99}}},
		{"an unknown series ID beside a known one", selection.Criteria{SeriesIDs: []int{7, 98}},
			selection.NotFoundError{SeriesIDs: []int{98}}},
		{"a series ID given as a movie ID", selection.Criteria{MovieIDs: []int{7}},
			selection.NotFoundError{MovieIDs: []int{7}}},
		{"all at once, with options that match", selection.Criteria{Titles: []string{"x-none", "towers"},
			MovieIDs: []int{3, 50}, SeriesIDs: []int{51, 4}, Languages: []string{"en"}},
			selection.NotFoundError{Titles: []string{"x-none"}, MovieIDs: []int{50}, SeriesIDs: []int{51}}},
	} {
		got, err := selection.Select(tc.c, library)
		var nf *selection.NotFoundError
		if !errors.As(err, &nf) {
			t.Errorf("%s: selected %q, err %v; want a NotFoundError", tc.name, paths(subtitlesOf(got)), err)
			continue
		}
		if !reflect.DeepEqual(*nf, tc.want) {
			t.Errorf("%s: names %+v, want %+v", tc.name, *nf, tc.want)
		}
	}

	err := &selection.NotFoundError{Titles: []string{"nosuch"}, MovieIDs: []int{99}, SeriesIDs: []int{98}}
	for _, part := range []string{`"nosuch"`, "movie with ID 99", "series with ID 98"} {
		if !strings.Contains(err.Error(), part) {
			t.Errorf("message %q does not name %s", err.Error(), part)
		}
	}

	// Bazarr has these videos; they just have no external subtitle (G9, not G8).
	expect(t, "a movie without subtitles", selection.Criteria{MovieIDs: []int{1}})
	expect(t, "a series without subtitles", selection.Criteria{SeriesIDs: []int{9}})
	expect(t, "a title without subtitles", selection.Criteria{Titles: []string{"bunny"}})
	expect(t, "a series' episode without subtitles", selection.Criteria{SeriesIDs: []int{4}, Seasons: []int{1}})
}

func TestOrder(t *testing.T) {
	sub := func(season, episode int, path string) bazarr.Subtitle {
		return episodeSub(5, season*100+episode, "Long", season, episode, "en", path, false, false)
	}
	ep := func(season, number int, subs ...bazarr.Subtitle) bazarr.Episode {
		return bazarr.Episode{ID: season*100 + number, Season: season, Number: number, Subtitles: subs}
	}
	s10e2 := sub(10, 2, "/tv/Long/S10E02.en.srt")
	s2e10 := sub(2, 10, "/tv/Long/S02E10.en.srt")
	s2e2b := sub(2, 2, "/tv/Long/S02E02.en.srt")
	s2e2a := sub(2, 2, "/tv/Long/S02E02.EN.srt") // "E" < "e" in byte order
	s2e2c := sub(2, 2, "/tv/Long/S02E02.é.srt")  // a multi-byte path sorts after ASCII
	m1b := movieSub(2, "Zulu", "en", "/m/Zulu/z.srt", false, false)
	m1a := movieSub(2, "Zulu", "en", "/m/Zulu/Z.srt", false, false)
	m2 := movieSub(1, "Alpha", "en", "/m/Alpha/a.srt", false, false)
	other := episodeSub(6, 600, "Another", 1, 1, "en", "/tv/Another/S01E01.srt", false, false)
	lib := bazarr.Library{
		// Bazarr's order is kept for movies and series, whatever their titles.
		Movies: []bazarr.Movie{{ID: 2, Title: "Zulu", Subtitles: []bazarr.Subtitle{m1b, m1a}},
			{ID: 1, Title: "Alpha", Subtitles: []bazarr.Subtitle{m2}}},
		Series: []bazarr.Series{
			{ID: 5, Title: "Long", Episodes: []bazarr.Episode{
				ep(10, 2, s10e2), ep(2, 10, s2e10), ep(2, 2, s2e2c, s2e2b, s2e2a),
			}},
			{ID: 6, Title: "Another", Episodes: []bazarr.Episode{{ID: 600, Season: 1, Number: 1, Subtitles: []bazarr.Subtitle{other}}}},
		},
	}
	got, err := selection.Select(selection.Criteria{}, lib)
	if err != nil {
		t.Fatal(err)
	}
	want := []bazarr.Subtitle{m1a, m1b, m2, s2e2a, s2e2b, s2e2c, s2e10, s10e2, other}
	if !reflect.DeepEqual(subtitlesOf(got), want) {
		t.Errorf("order\n  %q\nwant\n  %q", paths(subtitlesOf(got)), paths(want))
	}
	if lib.Series[0].Episodes[0].Season != 10 {
		t.Errorf("Select reordered the caller's library")
	}
}

// TestOthers: each selected subtitle comes with the other videos of the
// whole library showing a subtitle at its path, selected or not, and never
// its own video.
func TestOthers(t *testing.T) {
	double := "/tv/Long/S01E10-S02E01.en.srt" // one file Bazarr shows for two episodes
	lib := bazarr.Library{Series: []bazarr.Series{{ID: 5, Title: "Long", Episodes: []bazarr.Episode{
		{ID: 510, Season: 1, Number: 10, Subtitles: []bazarr.Subtitle{episodeSub(5, 510, "Long", 1, 10, "en", double, false, false)}},
		{ID: 521, Season: 2, Number: 1, Subtitles: []bazarr.Subtitle{
			episodeSub(5, 521, "Long", 2, 1, "en", double, false, false),
			episodeSub(5, 521, "Long", 2, 1, "de", "/tv/Long/S02E01.de.srt", false, false),
		}},
	}}}}
	got, err := selection.Select(selection.Criteria{Seasons: []int{2}}, lib)
	if err != nil || len(got) != 2 {
		t.Fatalf("selected %+v, %v", got, err)
	}
	want := []bazarr.Video{{Type: bazarr.TypeEpisode, ID: 510}}
	if got[0].Path != double || !reflect.DeepEqual(got[0].Others, want) || len(got[1].Others) != 0 {
		t.Errorf("others %+v and %+v; want the unselected episode for the shared path only", got[0], got[1])
	}
}
