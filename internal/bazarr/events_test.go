package bazarr_test

import (
	"context"
	"slices"
	"testing"

	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/bazarr"
	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/fakebazarr"
	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/fakeclock"
)

// twoPart is a multi-episode file's subtitle: Bazarr indexes it for
// episodes 41 and 42.
const twoPart = "/tv/Show/Season 01/Show - S01E01-E02.en.srt"

func eventsServer(t *testing.T) (*fakebazarr.Server, *bazarr.Client) {
	t.Helper()
	lib := changeLibrary()
	lib.Series[0].Episodes[0].Subtitles = append(lib.Series[0].Episodes[0].Subtitles, fakebazarr.Subtitle{Path: twoPart, Language: "en"})
	lib.Series[0].Episodes = append(lib.Series[0].Episodes, fakebazarr.Episode{SonarrEpisodeID: 42, Season: 1, Episode: 3,
		Subtitles: []fakebazarr.Subtitle{{Path: twoPart, Language: "en"}}})
	srv := fakebazarr.New(t, fakebazarr.Config{Library: lib})
	return srv, connect(t, srv, nil, bazarr.Limits{})
}

var (
	twoPartSub = bazarr.Subtitle{Type: bazarr.TypeEpisode, SeriesID: 3, EpisodeID: 41, Title: "Show", Season: 1, Episode: 2,
		Language: "en", Path: twoPart}
	secondPart = bazarr.Video{Type: bazarr.TypeEpisode, ID: 42}
)

func events(t *testing.T, client *bazarr.Client, sub bazarr.Subtitle, others ...bazarr.Video) bazarr.Events {
	t.Helper()
	es, retried, err := client.Events(context.Background(), sub, others)
	if err != nil || retried {
		t.Fatalf("events of %s: retried %v, %v", sub.Path, retried, err)
	}
	return es
}

func identities(es bazarr.Events) []string {
	if es == nil {
		return []string{}
	}
	return es.Identities()
}

// The events of a subtitle are those at its own path, byte for byte, of
// kinds 1 to 6.
func TestEventsOfItsPath(t *testing.T) {
	srv, client := eventsServer(t)
	de := movieSub
	de.Path, de.Language, de.Forced = "/movies/Amélie/Amélie.de.srt", "de", false
	for _, e := range []fakebazarr.Event{
		{Action: 1, Time: "01/01/27 10:00:00", Path: movieSub.Path},
		{Action: 5, Time: "01/01/27 10:05:00", Path: de.Path},       // another language's sync
		{Action: 5, Time: "01/01/27 10:06:00", Path: oddPath + " "}, // another path, nearly the same
		{Action: 5, Time: "01/01/27 10:07:00", Path: ""},            // no path
	} {
		srv.AddMovieEvent(7, e)
	}
	if got := identities(events(t, client, movieSub)); !slices.Equal(got, []string{"1 01/01/27 10:00:00"}) {
		t.Errorf("events of %s: %q, want only its download", movieSub.Path, got)
	}
	if es := events(t, client, de); !slices.Equal(es.Identities(), []string{"5 01/01/27 10:05:00"}) || !es.CurrentlySynced() {
		t.Errorf("events of %s: %q, want only its sync, currently synced", de.Path, es.Identities())
	}
	// Another action Bazarr records (as for a deletion) newer than a sync is
	// no event of the subtitle.
	srv.AddMovieEvent(7, fakebazarr.Event{Action: 0, Time: "01/01/27 11:00:00", Path: de.Path})
	srv.AddMovieEvent(7, fakebazarr.Event{Action: 7, Time: "01/01/27 11:01:00", Path: de.Path})
	if es := events(t, client, de); !slices.Equal(es.Identities(), []string{"5 01/01/27 10:05:00"}) || !es.CurrentlySynced() {
		t.Errorf("events of %s after other actions: %q, want only its sync, currently synced", de.Path, es.Identities())
	}
}

func TestEventsCurrentlySynced(t *testing.T) {
	for _, tc := range []struct {
		name    string
		actions []int // oldest first
		want    bool
	}{
		{"no event", nil, false},
		{"download, then sync", []int{1, 5}, true},
		{"sync, then upload", []int{5, 4}, false},
		{"download, sync, upgrade", []int{1, 5, 3}, false},
		{"translation, manual download, sync", []int{6, 2, 5}, true},
		{"sync, then translation", []int{5, 6}, false},
	} {
		srv, client := eventsServer(t)
		for i, a := range tc.actions {
			srv.AddMovieEvent(7, fakebazarr.Event{Action: a, Time: "03/04/27 10:00:0" + string(rune('0'+i)), Path: movieSub.Path})
		}
		if got := events(t, client, movieSub).CurrentlySynced(); got != tc.want {
			t.Errorf("%s: currently synced %v, want %v", tc.name, got, tc.want)
		}
	}
}

// S4: Bazarr 1.6.0 repeats each event once per external subtitle of the
// video; the events are the same.
func TestEventsRepeated(t *testing.T) {
	srv, client := eventsServer(t)
	for _, e := range []fakebazarr.Event{
		{Action: 1, Time: "05/06/27 08:00:00", Path: movieSub.Path},
		{Action: 5, Time: "05/06/27 08:00:00", Path: movieSub.Path},
		{Action: 5, Time: "05/06/27 08:10:00", Path: movieSub.Path},
	} {
		srv.AddMovieEvent(7, e)
	}
	want := events(t, client, movieSub).Identities()
	srv.RepeatEvents(true)
	if got := events(t, client, movieSub).Identities(); !slices.Equal(got, want) || len(got) != 3 {
		t.Errorf("events with each entry repeated: %q, want %q", got, want)
	}
	if want[0] != "5 05/06/27 08:10:00" {
		t.Errorf("newest identity %q, want \"5 05/06/27 08:10:00\"", want[0])
	}
}

// Across histories the later time is the newer, as a time and never as
// text, and of two events in the same second a sync is not the newer.
func TestEventsAcrossHistories(t *testing.T) {
	for _, tc := range []struct {
		name      string
		own, more []fakebazarr.Event // episode 41's and 42's, oldest first
		want      []string
		synced    bool
	}{
		{"across a year's end",
			[]fakebazarr.Event{{Action: 1, Time: "12/31/26 23:59:59", Path: twoPart}},
			[]fakebazarr.Event{{Action: 5, Time: "01/01/27 00:00:01", Path: twoPart}},
			[]string{"5 01/01/27 00:00:01", "1 12/31/26 23:59:59"}, true},
		{"a download in the other history after the sync",
			[]fakebazarr.Event{{Action: 5, Time: "01/01/27 00:00:01", Path: twoPart}},
			[]fakebazarr.Event{{Action: 3, Time: "02/01/27 00:00:00", Path: twoPart}},
			[]string{"3 02/01/27 00:00:00", "5 01/01/27 00:00:01"}, false},
		{"same second, sync in its own history",
			[]fakebazarr.Event{{Action: 5, Time: "03/04/27 10:00:00", Path: twoPart}},
			[]fakebazarr.Event{{Action: 1, Time: "03/04/27 10:00:00", Path: twoPart}},
			[]string{"1 03/04/27 10:00:00", "5 03/04/27 10:00:00"}, false},
		{"same second, sync in the other history",
			[]fakebazarr.Event{{Action: 4, Time: "03/04/27 10:00:00", Path: twoPart}},
			[]fakebazarr.Event{{Action: 5, Time: "03/04/27 10:00:00", Path: twoPart}},
			[]string{"4 03/04/27 10:00:00", "5 03/04/27 10:00:00"}, false},
		{"one event in both histories",
			[]fakebazarr.Event{{Action: 1, Time: "01/02/27 09:00:00", Path: twoPart}, {Action: 5, Time: "01/02/27 09:30:00", Path: twoPart}},
			[]fakebazarr.Event{{Action: 5, Time: "01/02/27 09:30:00", Path: twoPart}},
			[]string{"5 01/02/27 09:30:00", "1 01/02/27 09:00:00"}, true},
		{"the other history's events at other paths",
			[]fakebazarr.Event{{Action: 5, Time: "01/02/27 09:30:00", Path: twoPart}},
			[]fakebazarr.Event{{Action: 1, Time: "01/02/27 10:00:00", Path: "/tv/Show/Season 01/Show - S01E03.en.srt"}},
			[]string{"5 01/02/27 09:30:00"}, true},
	} {
		for _, swap := range []bool{false, true} {
			srv, client := eventsServer(t)
			own, more := tc.own, tc.more
			if swap { // the order of the histories decides nothing
				own, more = more, own
			}
			for _, e := range own {
				srv.AddEpisodeEvent(41, e)
			}
			for _, e := range more {
				srv.AddEpisodeEvent(42, e)
			}
			es := events(t, client, twoPartSub, secondPart)
			if !slices.Equal(identities(es), tc.want) || es.CurrentlySynced() != tc.synced {
				t.Errorf("%s (swapped %v): %q, currently synced %v; want %q, %v", tc.name, swap, identities(es), es.CurrentlySynced(), tc.want, tc.synced)
			}
		}
	}
	// Within one history, Bazarr's order stands.
	srv, client := eventsServer(t)
	srv.AddEpisodeEvent(41, fakebazarr.Event{Action: 1, Time: "03/04/27 10:00:00", Path: twoPart})
	srv.AddEpisodeEvent(41, fakebazarr.Event{Action: 5, Time: "03/04/27 10:00:00", Path: twoPart})
	if es := events(t, client, twoPartSub); !es.CurrentlySynced() {
		t.Errorf("one history listing the sync first: %q, want it currently synced", es.Identities())
	}
}

// A movie's history is read by radarrid and an episode's by episodeid,
// whole: without paging parameters, which Bazarr then does not apply.
func TestEventsReads(t *testing.T) {
	srv, client := eventsServer(t)
	for i := range 300 {
		srv.AddEpisodeEvent(42, fakebazarr.Event{Action: 1 + i%6, Time: "06/07/27 10:" + twoDigits(i/60) + ":" + twoDigits(i%60), Path: twoPart})
	}
	events(t, client, movieSub)
	es := events(t, client, twoPartSub, secondPart, twoPartSub.Video(), secondPart)
	if len(es) != 300 {
		t.Errorf("%d events, want all 300", len(es))
	}
	var got []string
	for _, r := range srv.Requests()[1:] {
		got = append(got, r.Path+"?"+r.RawQuery)
	}
	want := []string{"/api/movies/history?radarrid=7", "/api/episodes/history?episodeid=41", "/api/episodes/history?episodeid=42"}
	if !slices.Equal(got, want) {
		t.Errorf("reads %q, want %q", got, want)
	}
}

func twoDigits(n int) string { return string(rune('0'+n/10)) + string(rune('0'+n%10)) }

// Each history read is a read under G3 and says when it needed a retry; a
// history that is not Bazarr's is a failed read.
func TestEventsRetried(t *testing.T) {
	for name, b := range map[string]fakebazarr.Behavior{
		"503": fakebazarr.Status(503), "web page": fakebazarr.Reply(200, "<!doctype html><title>Bazarr</title>"),
		"no data":              fakebazarr.Body(`{"total": 0}`),
		"event without time":   fakebazarr.Body(`{"data": [{"action": 5, "subtitles_path": "/x.srt"}]}`),
		"unparsable time":      fakebazarr.Body(`{"data": [{"action": 5, "parsed_timestamp": "22.11.2026 09:17", "subtitles_path": "/x.srt"}]}`),
		"entry without action": fakebazarr.Body(`{"data": [{"parsed_timestamp": "11/22/26 09:17:04", "subtitles_path": "/x.srt"}]}`),
	} {
		clock := fakeclock.New(t)
		srv := fakebazarr.New(t, fakebazarr.Config{Library: changeLibrary()})
		client := connect(t, srv, clock, bazarr.Limits{})
		srv.AddEpisodeEvent(41, fakebazarr.Event{Action: 5, Time: "11/22/26 09:17:04", Path: episodeSub.Path})
		srv.Respond(func(r fakebazarr.Request) fakebazarr.Behavior {
			if r.N == 3 { // the second history read
				return b
			}
			return fakebazarr.Serve
		})
		type result struct {
			es      bazarr.Events
			retried bazarr.Retried
			err     error
		}
		done := make(chan result, 1)
		go func() {
			es, retried, err := client.Events(context.Background(), episodeSub, []bazarr.Video{secondPart})
			done <- result{es, retried, err}
		}()
		clock.Advance(clock.WaitSleep())
		res := await(t, done)
		if res.err != nil || !bool(res.retried) || !slices.Equal(res.es.Identities(), []string{"5 11/22/26 09:17:04"}) {
			t.Errorf("%s: %q, retried %v, %v; want the event after a retry", name, identities(res.es), res.retried, res.err)
		}
	}
}
