package bazarr_test

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/bazarr"
	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/fakebazarr"
	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/fakeclock"
)

// oddPath holds every character a path could lose on its way to Bazarr.
const oddPath = `/movies/Amélie & Co #1 + 100% \ back\slash/Amélie.en.srt`

var (
	movieSub = bazarr.Subtitle{Type: bazarr.TypeMovie, MovieID: 7, Title: "Amélie",
		Language: "en", Forced: true, Path: oddPath}
	episodeSub = bazarr.Subtitle{Type: bazarr.TypeEpisode, SeriesID: 3, EpisodeID: 41, Title: "Show",
		Season: 1, Episode: 2, Language: "fr", HI: true, Path: "/tv/Show/Season 01/Show - S01E02.fr.hi.srt"}
)

// changeLibrary is the fake's library holding movieSub and episodeSub.
func changeLibrary() fakebazarr.Library {
	return fakebazarr.Library{
		Movies: []fakebazarr.Movie{{RadarrID: 7, Title: "Amélie", Subtitles: []fakebazarr.Subtitle{
			{Path: oddPath, Language: "en", Forced: true},
			{Path: "/movies/Amélie/Amélie.de.srt", Language: "de"},
		}}},
		Series: []fakebazarr.Series{{SonarrSeriesID: 3, Title: "Show", Episodes: []fakebazarr.Episode{
			{SonarrEpisodeID: 41, Season: 1, Episode: 2, Subtitles: []fakebazarr.Subtitle{
				{Path: episodeSub.Path, Language: "fr", HI: true},
			}},
		}}},
	}
}

type changeOutput struct {
	res bazarr.ChangeResult
	err error
}

// inBackground runs a change and hands back what it returned.
func inBackground(change func() (bazarr.ChangeResult, error)) <-chan changeOutput {
	done := make(chan changeOutput, 1)
	go func() {
		res, err := change()
		done <- changeOutput{res, err}
	}()
	return done
}

// changesReceived returns the requests the fake received that are not reads.
func changesReceived(srv *fakebazarr.Server) []fakebazarr.Request {
	var out []fakebazarr.Request
	for _, r := range srv.Requests() {
		if r.Method != http.MethodGet {
			out = append(out, r)
		}
	}
	return out
}

// changeServer starts a fake holding changeLibrary and a client checked
// against it on clock.
func changeServer(t *testing.T, clock bazarr.Clock, limits bazarr.Limits) (*fakebazarr.Server, *bazarr.Client) {
	t.Helper()
	srv := fakebazarr.New(t, fakebazarr.Config{Library: changeLibrary()})
	return srv, connect(t, srv, clock, limits)
}

// onChanges answers every change with b and every read as Bazarr would.
func onChanges(b fakebazarr.Behavior) func(fakebazarr.Request) fakebazarr.Behavior {
	return func(r fakebazarr.Request) fakebazarr.Behavior {
		if r.Method != http.MethodGet {
			return b
		}
		return fakebazarr.Serve
	}
}

func TestFixParameters(t *testing.T) {
	srv, client := changeServer(t, nil, bazarr.Limits{})
	for _, tc := range []struct {
		sub  bazarr.Subtitle
		want url.Values
	}{
		{movieSub, url.Values{"action": {"remove_tags"}, "language": {"en"}, "path": {oddPath},
			"type": {"movie"}, "id": {"7"}, "forced": {"True"}, "hi": {"False"}}},
		// The episode's own ID, never the series ID `list` prints.
		{episodeSub, url.Values{"action": {"remove_tags"}, "language": {"fr"}, "path": {episodeSub.Path},
			"type": {"episode"}, "id": {"41"}, "forced": {"False"}, "hi": {"True"}}},
	} {
		res, err := client.Fix(context.Background(), tc.sub, bazarr.FixRemoveTags)
		if err != nil || res.Outcome != bazarr.Accepted {
			t.Errorf("%s: %+v, %v; want accepted", tc.sub.Path, res, err)
		}
		changes := changesReceived(srv)
		r := changes[len(changes)-1]
		if r.Method != http.MethodPatch || r.Path != "/api/subtitles" || r.Body != "" {
			t.Errorf("%s: %s %s with body %q, want PATCH /api/subtitles with the parameters in the query",
				tc.sub.Path, r.Method, r.Path, r.Body)
		}
		if got := r.Params(); fmt.Sprint(got) != fmt.Sprint(tc.want) {
			t.Errorf("%s: parameters %v, want %v", tc.sub.Path, got, tc.want)
		}
	}
}

func TestFixActions(t *testing.T) {
	srv, client := changeServer(t, nil, bazarr.Limits{})
	var words []string
	for _, fix := range bazarr.Fixes {
		if res, err := client.Fix(context.Background(), movieSub, fix); err != nil || res.Outcome != bazarr.Accepted {
			t.Errorf("fix %d: %+v, %v; want accepted", fix, res, err)
		}
		changes := changesReceived(srv)
		words = append(words, changes[len(changes)-1].Params().Get("action"))
	}
	// The technical specification's words, in F10's order.
	want := []string{"remove_HI", "remove_tags", "OCR_fixes", "common", "fix_uppercase", "emoji"}
	if !slices.Equal(words, want) {
		t.Errorf("action words %q, want %q", words, want)
	}
	// R3: nothing outside the six can be sent.
	before := len(srv.Requests())
	for _, fix := range []bazarr.Fix{0, bazarr.Fix(len(bazarr.Fixes) + 1)} {
		if res, err := client.Fix(context.Background(), movieSub, fix); err == nil || res.Outcome != bazarr.NotSent {
			t.Errorf("fix %d: %+v, %v; want an error and nothing sent", fix, res, err)
		}
	}
	if n := len(srv.Requests()) - before; n != 0 {
		t.Errorf("%d requests for fixes outside the six, want none", n)
	}
}

func TestDeleteParameters(t *testing.T) {
	srv, client := changeServer(t, nil, bazarr.Limits{})
	for _, tc := range []struct {
		sub  bazarr.Subtitle
		path string
		want url.Values
	}{
		{movieSub, "/api/movies/subtitles", url.Values{"radarrid": {"7"}, "language": {"en"},
			"forced": {"True"}, "hi": {"False"}, "path": {oddPath}}},
		{episodeSub, "/api/episodes/subtitles", url.Values{"seriesid": {"3"}, "episodeid": {"41"},
			"language": {"fr"}, "forced": {"False"}, "hi": {"True"}, "path": {episodeSub.Path}}},
	} {
		res, err := client.Delete(context.Background(), tc.sub)
		if err != nil || res.Outcome != bazarr.Accepted {
			t.Errorf("%s: %+v, %v; want accepted", tc.sub.Path, res, err)
		}
		changes := changesReceived(srv)
		r := changes[len(changes)-1]
		if r.Method != http.MethodDelete || r.Path != tc.path || r.Body != "" {
			t.Errorf("%s: %s %s with body %q, want DELETE %s with the parameters in the query",
				tc.sub.Path, r.Method, r.Path, r.Body, tc.path)
		}
		if got := r.Params(); fmt.Sprint(got) != fmt.Sprint(tc.want) {
			t.Errorf("%s: parameters %v, want %v", tc.sub.Path, got, tc.want)
		}
	}
	// Bazarr lists a deleted subtitle no more; the other stays.
	lib, _, err := client.Library(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if subs := lib.Subtitles(); len(subs) != 1 || subs[0].Path != "/movies/Amélie/Amélie.de.srt" {
		t.Errorf("library after both deletes: %+v, want only the German subtitle", subs)
	}
	// Deleting it again is refused, as Bazarr refuses a subtitle it does not have.
	if res, err := client.Delete(context.Background(), movieSub); err != nil || res.Outcome != bazarr.Refused {
		t.Errorf("second delete: %+v, %v; want refused", res, err)
	}
}

func TestChangeBasePathAndKey(t *testing.T) {
	srv := fakebazarr.New(t, fakebazarr.Config{BasePath: "/bazarr", Library: changeLibrary()})
	client := connect(t, srv, nil, bazarr.Limits{})
	if res, err := client.Fix(context.Background(), episodeSub, bazarr.FixCommon); err != nil || res.Outcome != bazarr.Accepted {
		t.Errorf("fix: %+v, %v; want accepted", res, err)
	}
	if res, err := client.Delete(context.Background(), episodeSub); err != nil || res.Outcome != bazarr.Accepted {
		t.Errorf("delete: %+v, %v; want accepted", res, err)
	}
	changes := changesReceived(srv)
	if len(changes) != 2 {
		t.Fatalf("%d changes received, want 2", len(changes))
	}
	for _, r := range changes {
		if !strings.HasPrefix(r.Path, "/bazarr/api/") {
			t.Errorf("%s %s: want it under /bazarr/api/ (F4)", r.Method, r.Path)
		}
		if r.Header.Get("X-API-KEY") != srv.APIKey() || strings.Contains(r.Path+"?"+r.RawQuery+r.Body, srv.APIKey()) {
			t.Errorf("%s %s: the key must travel in X-API-KEY only (R2)", r.Method, r.Path)
		}
	}
}

func TestChangeRefused(t *testing.T) {
	srv, client := changeServer(t, nil, bazarr.Limits{})
	key := srv.APIKey()
	page := "<!doctype html>\n<html lang=\"en\">\n<head><title>Bazarr</title></head>\n<body></body>\n</html>\n"
	for _, tc := range []struct {
		name     string
		reply    fakebazarr.Behavior
		want     string // the reason, exactly
		contains string // or a part of it
	}{
		// What Bazarr answers for a call it does not have.
		{name: "web page with 200", reply: fakebazarr.Reply(200, page), contains: "<title>Bazarr</title>"},
		{name: "201", reply: fakebazarr.Reply(201, `"created"`), want: "created"},
		{name: "Bazarr's 404", reply: fakebazarr.Reply(404, "\"Episode not found\"\n"), want: "Episode not found"},
		{name: "401", reply: fakebazarr.Reply(401, `{"message": "Unauthorized"}`), want: `{"message": "Unauthorized"}`},
		{name: "400 JSON object", reply: fakebazarr.Reply(400, "{\"errors\": {\"path\": \"x\"},\n \"message\": \"Input payload validation failed\"}\n"),
			want: `{"errors": {"path": "x"}, "message": "Input payload validation failed"}`},
		{name: "several lines", reply: fakebazarr.Reply(500, "line one\r\nline two\n\n\tthree four"), want: "line one line two three four"},
		{name: "control characters", reply: fakebazarr.Reply(409, "\x1b[31mred\x07 \x1b]0;title\x07end\x00"), contains: "red"},
		{name: "empty body", reply: fakebazarr.Reply(500, ""), contains: "500"},
		{name: "echoed key", reply: fakebazarr.Reply(400, `"bad key `+key+` given"`), contains: "bad key"},
		{name: "long body", reply: fakebazarr.Reply(500, strings.Repeat("é", 5000)), contains: "é"},
		{name: "body of 100 KiB", reply: fakebazarr.Reply(500, strings.Repeat("a", 100<<10)), contains: "aaa"},
	} {
		srv.Respond(onChanges(tc.reply))
		before := len(changesReceived(srv))
		res, err := client.Delete(context.Background(), movieSub)
		if err != nil || res.Outcome != bazarr.Refused {
			t.Errorf("%s: %+v, %v; want refused", tc.name, res, err)
			continue
		}
		if n := len(changesReceived(srv)) - before; n != 1 {
			t.Errorf("%s: sent %d times, want once", tc.name, n)
		}
		r := res.Reason
		switch {
		case tc.want != "" && r != tc.want:
			t.Errorf("%s: reason %q, want %q", tc.name, r, tc.want)
		case !strings.Contains(r, tc.contains):
			t.Errorf("%s: reason %q, want it to hold %q", tc.name, r, tc.contains)
		case strings.ContainsFunc(r, func(c rune) bool { return c < 0x20 || c == 0x7f || c == ' ' }):
			t.Errorf("%s: reason %q holds a control character or a line break", tc.name, r)
		case strings.Contains(r, key):
			t.Errorf("%s: reason %q holds the API key (R2)", tc.name, r)
		case len(r) > 400:
			t.Errorf("%s: reason of %d bytes, want one a result line can carry", tc.name, len(r))
		}
	}
	// The fake's own refusal, as Bazarr 1.6.2 answers a path it does not have.
	srv.Respond(nil)
	gone := movieSub
	gone.Path = "/movies/Amélie/gone.en.srt"
	res, err := client.Fix(context.Background(), gone, bazarr.FixEmoji)
	if err != nil || res.Outcome != bazarr.Refused || res.Reason != "Subtitles file not found. Path mapping issue?" {
		t.Errorf("fix of a path Bazarr lacks: %+v, %v; want refused with Bazarr's reason", res, err)
	}
}

func TestChangeLimit(t *testing.T) {
	if bazarr.DefaultLimits.Change != 60*time.Second {
		t.Errorf("DefaultLimits.Change = %v, want 60s until measured (T1)", bazarr.DefaultLimits.Change)
	}
	for _, tc := range []struct {
		limits bazarr.Limits
		want   time.Duration
	}{
		{bazarr.Limits{}, 60 * time.Second},
		{bazarr.Limits{Read: 7 * time.Second, Change: 9 * time.Second}, 9 * time.Second},
		{bazarr.Limits{Change: 6 * time.Minute}, 5 * time.Minute},
	} {
		clock := fakeclock.New(t)
		srv, client := changeServer(t, clock, tc.limits)
		srv.Respond(onChanges(fakebazarr.Hold))
		done := inBackground(func() (bazarr.ChangeResult, error) {
			return client.Fix(context.Background(), movieSub, bazarr.FixCommon)
		})
		srv.WaitHeld()
		if d := clock.WaitTimer(); d != tc.want {
			t.Errorf("limits %+v: the change carries %v, want %v", tc.limits, d, tc.want)
		}
		clock.Advance(time.Hour)
		await(t, done)
	}
}
