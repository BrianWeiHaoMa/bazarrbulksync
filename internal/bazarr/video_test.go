package bazarr_test

import (
	"context"
	"slices"
	"testing"

	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/bazarr"
	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/fakebazarr"
	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/fakeclock"
)

func videoNow(t *testing.T, client *bazarr.Client, sub bazarr.Subtitle) bazarr.VideoNow {
	t.Helper()
	v, retried, err := client.VideoNow(context.Background(), sub)
	if err != nil || retried {
		t.Fatalf("video of %s: retried %v, %v", sub.Path, retried, err)
	}
	return v
}

// The read of one video shows its external subtitles as Bazarr shows them
// now, each with the selected subtitle's title and numbering.
func TestVideoNow(t *testing.T) {
	lib := changeLibrary()
	lib.Movies[0].Subtitles = append(lib.Movies[0].Subtitles, fakebazarr.Subtitle{Embedded: true, Language: "en"})
	lib.Series[0].Episodes[0].Title = "A Touch of Class"
	lib.Series[0].Episodes[0].Subtitles = append(lib.Series[0].Episodes[0].Subtitles,
		fakebazarr.Subtitle{Path: "/tv/Show/Season 01/Show - S01E02.en.srt", Language: "en"})
	lib.Movies = append(lib.Movies, fakebazarr.Movie{RadarrID: 8, Title: "Bare"})
	srv := fakebazarr.New(t, fakebazarr.Config{Library: lib})
	client := connect(t, srv, nil, bazarr.Limits{})

	de := movieSub
	de.Path, de.Language, de.Forced = "/movies/Amélie/Amélie.de.srt", "de", false
	if v := videoNow(t, client, movieSub); !v.Listed || !slices.Equal(v.Subtitles, []bazarr.Subtitle{movieSub, de}) {
		t.Errorf("movie: %+v, want listed with its two external subtitles", v)
	}
	en := episodeSub
	en.Path, en.Language, en.HI = "/tv/Show/Season 01/Show - S01E02.en.srt", "en", false
	if v := videoNow(t, client, episodeSub); !v.Listed || !slices.Equal(v.Subtitles, []bazarr.Subtitle{episodeSub, en}) {
		t.Errorf("episode: %+v, want listed with the series' title and the episode's numbering", v)
	}
	// Renamed and renumbered since the library was read: the video as just
	// read replaces them; an episode's own title never becomes the title.
	lib.Movies[0].Title = "Amélie (2001)"
	lib.Series[0].Episodes[0].Season, lib.Series[0].Episodes[0].Episode = 2, 3
	renamed := connect(t, fakebazarr.New(t, fakebazarr.Config{Library: lib}), nil, bazarr.Limits{})
	if v := videoNow(t, renamed, movieSub); len(v.Subtitles) != 2 || v.Subtitles[0].Title != "Amélie (2001)" {
		t.Errorf("renamed movie: %+v, want the title as Bazarr now gives it", v)
	}
	if v := videoNow(t, renamed, episodeSub); len(v.Subtitles) != 2 || v.Subtitles[0].Title != "Show" ||
		v.Subtitles[0].Season != 2 || v.Subtitles[0].Episode != 3 || v.Subtitles[0].SeriesID != 3 {
		t.Errorf("renumbered episode: %+v, want season 2 episode 3 under the series' title", v)
	}
	// Deleted since the library was read.
	if res, err := client.Delete(context.Background(), de); err != nil || res.Outcome != bazarr.Accepted {
		t.Fatal(res, err)
	}
	if v := videoNow(t, client, movieSub); !v.Listed || !slices.Equal(v.Subtitles, []bazarr.Subtitle{movieSub}) {
		t.Errorf("movie after a delete: %+v, want only the subtitle left", v)
	}
	bare := bazarr.Subtitle{Type: bazarr.TypeMovie, MovieID: 8, Title: "Bare", Language: "en", Path: "/movies/Bare/Bare.en.srt"}
	if v := videoNow(t, client, bare); !v.Listed || len(v.Subtitles) != 0 {
		t.Errorf("movie without subtitles: %+v, want listed with none", v)
	}
	var got []string
	for _, r := range srv.Requests()[1:] {
		if r.Method == "GET" {
			got = append(got, r.Path+"?"+r.Params().Encode())
		}
	}
	want := []string{"/api/movies?radarrid%5B%5D=7", "/api/episodes?episodeid%5B%5D=41", "/api/movies?radarrid%5B%5D=7", "/api/movies?radarrid%5B%5D=8"}
	if !slices.Equal(got, want) {
		t.Errorf("reads %q, want %q", got, want)
	}
}

// A video Bazarr no longer has answers HTTP 200 with nothing in it: a
// successful read, never retried.
func TestVideoNowGone(t *testing.T) {
	clock := fakeclock.New(t)
	srv, client := changeServer(t, clock, bazarr.Limits{})
	gone := movieSub
	gone.MovieID = 999
	goneEpisode := episodeSub
	goneEpisode.EpisodeID = 999
	for _, sub := range []bazarr.Subtitle{gone, goneEpisode} {
		if v := videoNow(t, client, sub); v.Listed || len(v.Subtitles) != 0 {
			t.Errorf("%s %d: %+v, want unlisted", sub.Type, sub.Video().ID, v)
		}
	}
	if n := len(srv.Requests()); n != 3 {
		t.Errorf("%d requests, want the check and one read each", n)
	}
}

func TestVideoNowRetried(t *testing.T) {
	for name, b := range map[string]fakebazarr.Behavior{
		"503": fakebazarr.Status(503), "no data": fakebazarr.Body(`{"total": 0}`),
		"web page": fakebazarr.Reply(200, "<!doctype html><title>Bazarr</title>"),
	} {
		clock := fakeclock.New(t)
		srv, client := changeServer(t, clock, bazarr.Limits{})
		srv.Respond(func(r fakebazarr.Request) fakebazarr.Behavior {
			if r.N == 2 {
				return b
			}
			return fakebazarr.Serve
		})
		type result struct {
			v       bazarr.VideoNow
			retried bazarr.Retried
			err     error
		}
		done := make(chan result, 1)
		go func() {
			v, retried, err := client.VideoNow(context.Background(), episodeSub)
			done <- result{v, retried, err}
		}()
		clock.Advance(clock.WaitSleep())
		res := await(t, done)
		if res.err != nil || !bool(res.retried) || !res.v.Listed || len(res.v.Subtitles) != 1 {
			t.Errorf("%s: %+v, retried %v, %v; want the video after a retry", name, res.v, res.retried, res.err)
		}
	}
}
