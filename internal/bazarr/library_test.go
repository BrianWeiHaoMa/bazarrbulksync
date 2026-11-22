package bazarr_test

import (
	"context"
	"fmt"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/bazarr"
	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/fakebazarr"
)

func TestLibrary(t *testing.T) {
	oddPath := `\\nas\media\\Movies//Zoë Ünïcode/Zoë.fr.srt `
	srv := fakebazarr.New(t, fakebazarr.Config{Library: fakebazarr.Library{
		Movies: []fakebazarr.Movie{
			{RadarrID: 20, Title: "Zoë", Subtitles: []fakebazarr.Subtitle{
				{Path: oddPath, Language: "fr", HI: true},
				{Embedded: true, Language: "en"},
			}},
			{RadarrID: 5, Title: "Alpha"}, // no subtitle at all
			{RadarrID: 7, Title: "Beta", Subtitles: []fakebazarr.Subtitle{{Embedded: true, Language: "de"}}},
		},
		Series: []fakebazarr.Series{
			{SonarrSeriesID: 9, Title: "The Show", Episodes: []fakebazarr.Episode{
				{SonarrEpisodeID: 901, Season: 1, Episode: 1, Title: "Pilot", Subtitles: []fakebazarr.Subtitle{
					{Path: "/tv/Show/S01E01.en.forced.srt", Language: "en", Forced: true},
					{Embedded: true, Language: "en"},
				}},
				{SonarrEpisodeID: 902, Season: 0, Episode: 3, Title: "Special"},
			}},
			{SonarrSeriesID: 2, Title: "Another Show"}, // no episode
		},
	}})
	client := connect(t, srv, nil, bazarr.Limits{})
	lib, retried, err := client.Library(context.Background())
	if err != nil || retried {
		t.Fatalf("retried %v, err %v", retried, err)
	}

	var movieIDs, seriesIDs []int
	for _, m := range lib.Movies {
		movieIDs = append(movieIDs, m.ID)
	}
	for _, s := range lib.Series {
		seriesIDs = append(seriesIDs, s.ID)
	}
	if !reflect.DeepEqual(movieIDs, []int{20, 5, 7}) || !reflect.DeepEqual(seriesIDs, []int{9, 2}) {
		t.Errorf("movies %v, series %v; want every one, in Bazarr's order: [20 5 7], [9 2]", movieIDs, seriesIDs)
	}
	if n := len(lib.Series[0].Episodes); n != 2 {
		t.Errorf("The Show has %d episodes, want 2", n)
	}

	want := []bazarr.Subtitle{
		{Type: bazarr.TypeMovie, MovieID: 20, Title: "Zoë", Language: "fr", HI: true, Path: oddPath},
		{Type: bazarr.TypeEpisode, SeriesID: 9, EpisodeID: 901, Title: "The Show", Season: 1, Episode: 1,
			Language: "en", Forced: true, Path: "/tv/Show/S01E01.en.forced.srt"},
	}
	if got := lib.Subtitles(); !reflect.DeepEqual(got, want) {
		t.Errorf("subtitles:\n got %+v\nwant %+v", got, want)
	}
}

func TestLibraryManySeries(t *testing.T) {
	var library fakebazarr.Library
	for id := 1; id <= 60; id++ {
		library.Series = append(library.Series, fakebazarr.Series{SonarrSeriesID: id, Title: fmt.Sprint("S", id),
			Episodes: []fakebazarr.Episode{{SonarrEpisodeID: 1000 + id, Season: 1, Episode: 1,
				Subtitles: []fakebazarr.Subtitle{{Path: fmt.Sprintf("/tv/%d.en.srt", id), Language: "en"}}}}})
	}
	srv := fakebazarr.New(t, fakebazarr.Config{Library: library})
	lib, _, err := connect(t, srv, nil, bazarr.Limits{}).Library(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	subs := lib.Subtitles()
	if len(subs) != 60 {
		t.Fatalf("%d subtitles, want one for each of 60 series", len(subs))
	}
	for i, s := range subs {
		if s.SeriesID != i+1 || s.Title != fmt.Sprint("S", i+1) {
			t.Errorf("subtitle %d belongs to series %d %q, want %d", i, s.SeriesID, s.Title, i+1)
		}
	}
	// Episodes are read for a bounded number of series at a time.
	var batches []int
	for _, r := range srv.Requests() {
		if strings.HasSuffix(r.Path, "/api/episodes") {
			q, _ := url.ParseQuery(r.RawQuery)
			batches = append(batches, len(q["seriesid[]"]))
		}
	}
	if !reflect.DeepEqual(batches, []int{25, 25, 10}) {
		t.Errorf("episodes read for %v series per request, want [25 25 10]", batches)
	}
}
