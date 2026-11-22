package bed_test

import (
	"reflect"
	"testing"

	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/bazarr"
	"github.com/BrianWeiHaoMa/bazarrbulksync/testbed/bed"
)

// Compare is what the bed's bring-up waits on and what the check against the
// bed asserts: a placed subtitle counts as shown only with every attribute.
func TestCompare(t *testing.T) {
	en := bazarr.Subtitle{Type: bazarr.TypeMovie, MovieID: 2, Title: "Sintel", Language: "en", Path: "/m/S.en.srt"}
	hi := en
	hi.HI, hi.Path = true, "/m/S.en.hi.srt"
	ep := bazarr.Subtitle{Type: bazarr.TypeEpisode, SeriesID: 1, EpisodeID: 31, Title: "Show", Season: 1, Episode: 1,
		Language: "fr", Path: "/tv/S01E01.fr.srt"}
	m := bed.Manifest{Subtitles: []bazarr.Subtitle{en, hi, ep}}
	library := func(movieSubs, episodeSubs []bazarr.Subtitle) bazarr.Library {
		return bazarr.Library{
			Movies: []bazarr.Movie{{ID: 2, Title: "Sintel", Subtitles: movieSubs}},
			Series: []bazarr.Series{{ID: 1, Title: "Show", Episodes: []bazarr.Episode{{ID: 31, Season: 1, Number: 1, Subtitles: episodeSubs}}}},
		}
	}

	if missing, unexpected := m.Compare(library([]bazarr.Subtitle{hi, en}, []bazarr.Subtitle{ep})); missing != nil || unexpected != nil {
		t.Errorf("all shown: missing %v, unexpected %v; want none", missing, unexpected)
	}

	notIndexed := library(nil, nil)
	if missing, _ := m.Compare(notIndexed); !reflect.DeepEqual(missing, []bazarr.Subtitle{hi, en, ep}) {
		t.Errorf("nothing indexed: missing %v, want every placed subtitle by path", missing)
	}

	hiAsPlain := hi
	hiAsPlain.HI = false
	missing, unexpected := m.Compare(library([]bazarr.Subtitle{en, hiAsPlain}, []bazarr.Subtitle{ep}))
	if !reflect.DeepEqual(missing, []bazarr.Subtitle{hi}) || !reflect.DeepEqual(unexpected, []bazarr.Subtitle{hiAsPlain}) {
		t.Errorf("HI flag lost: missing %v, unexpected %v; want the HI subtitle missing and its flagless copy unexpected",
			missing, unexpected)
	}
}

// The check against the bed is switched on by its own variable alone: the
// product's BAZARR_URL and BAZARR_API_KEY, exported in a user's shell, never
// send `go test ./...` to a live server.
func TestLoadIgnoresProductVariables(t *testing.T) {
	t.Setenv(bed.EnvVar, "")
	t.Setenv("BAZARR_URL", "http://127.0.0.1:1")
	t.Setenv("BAZARR_API_KEY", "0123456789abcdef")
	if m, ok, err := bed.Load(); ok || err != nil || m.URL != "" {
		t.Errorf("Load() = %+v, %v, %v; want no bed", m, ok, err)
	}
}

// A library missing a movie, or listing the movies out of Bazarr's order,
// differs from the bed's even with every subtitle shown.
func TestDifferencesMovies(t *testing.T) {
	m := bed.Manifest{Movies: []bed.Video{{ID: 3, Title: "B"}, {ID: 1, Title: "T"}}}
	same := bazarr.Library{Movies: []bazarr.Movie{{ID: 3, Title: "B"}, {ID: 1, Title: "T"}}}
	if d := m.Differences(same); d != nil {
		t.Errorf("same library: %v", d)
	}
	for _, lib := range []bazarr.Library{
		{Movies: []bazarr.Movie{{ID: 3, Title: "B"}}},
		{Movies: []bazarr.Movie{{ID: 1, Title: "T"}, {ID: 3, Title: "B"}}},
	} {
		if d := m.Differences(lib); len(d) != 1 {
			t.Errorf("movies %v: differences %v, want one", lib.Movies, d)
		}
	}
}
