package fakebazarr_test

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/fakebazarr"
)

// Real Bazarr 1.6.2 answers a call it does not have, even under /api/, with
// or without a key, with its web page and HTTP 200, not with 404 or 401.
func TestUnknownCallAnswersWebPage(t *testing.T) {
	srv := fakebazarr.New(t, fakebazarr.Config{})
	for _, key := range []string{srv.APIKey(), ""} {
		req, err := http.NewRequest("GET", srv.URL+"/api/nonexistent", nil)
		if err != nil {
			t.Fatal(err)
		}
		if key != "" {
			req.Header.Set("X-API-KEY", key)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/html") {
			t.Errorf("unknown call (key given: %v) answered %d %q, want 200 and Bazarr's web page",
				key != "", resp.StatusCode, resp.Header.Get("Content-Type"))
		}
	}
}

// send makes one request to the fake with the key and returns the status
// and body.
func send(t *testing.T, srv *fakebazarr.Server, method, call, query, key string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(method, srv.URL+"/api/"+call+"?"+query, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-API-KEY", key)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

// The answers below are those the test bed's Bazarr 1.6.2 gave to the same
// requests.
func TestChangesAnswerAsBazarr(t *testing.T) {
	srv := fakebazarr.New(t, fakebazarr.Config{Library: fakebazarr.Library{
		Movies: []fakebazarr.Movie{{RadarrID: 2, Title: "Sintel", Subtitles: []fakebazarr.Subtitle{
			{Path: "/m/Sintel.de.srt", Language: "de"}, {Path: "/m/Sintel.es.srt", Language: "es"},
			{Path: "/m/Sintel.en.ass", Language: "en"}, {Embedded: true, Language: "en"},
		}}},
		Series: []fakebazarr.Series{{SonarrSeriesID: 1, Title: "Fawlty Towers", Episodes: []fakebazarr.Episode{
			{SonarrEpisodeID: 31, Season: 1, Episode: 1, Subtitles: []fakebazarr.Subtitle{{Path: "/tv/S01E01.fr.srt", Language: "fr"}}},
		}}},
	}})
	key := srv.APIKey()
	fix := "action=remove_tags&forced=False&hi=False&"
	for _, tc := range []struct {
		method, call, query string
		code                int
		body                string // a prefix of the body
	}{
		{"PATCH", "subtitles", fix + "language=de&path=%2Fm%2FSintel.de.srt&type=movie&id=2", 204, ""},
		{"PATCH", "subtitles", fix + "language=fr&path=%2Ftv%2FS01E01.fr.srt&type=episode&id=31", 204, ""},
		// Any word is taken for a modification's name.
		{"PATCH", "subtitles", "action=Remove_Tags&forced=False&hi=False&language=de&path=%2Fm%2FSintel.de.srt&type=movie&id=2", 204, ""},
		// An episode's subtitle sent with the series ID.
		{"PATCH", "subtitles", fix + "language=fr&path=%2Ftv%2FS01E01.fr.srt&type=episode&id=1", 404, "\"Episode not found\"\n"},
		{"PATCH", "subtitles", fix + "language=de&path=%2Fm%2FSintel.de.srt&type=movie&id=999", 404, "\"Movie not found\"\n"},
		{"PATCH", "subtitles", fix + "language=de&path=%2Fm%2Fnope.srt&type=movie&id=2", 500, "\"Subtitles file not found. Path mapping issue?\"\n"},
		{"PATCH", "subtitles", fix + "language=en&path=%2Fm%2FSintel.en.ass&type=movie&id=2", 409, "\"Unable to take action on subtitles file. Check logs.\"\n"},
		{"PATCH", "subtitles", "forced=False&hi=False&language=de&path=%2Fm%2FSintel.de.srt&type=movie&id=2", 400, `{"errors": {"action": "Action from [\"sync\", \"translate\", \"extract\" or mods name] Missing required parameter`},
		{"DELETE", "subtitles", "action=remove_tags", 405, `{"message": "The method is not allowed for the requested URL."}`},
		{"DELETE", "movies/subtitles", "radarrid=2&language=en&forced=False&hi=False", 400, `{"errors": {"path": "Path of the subtitles file Missing required parameter`},
		{"DELETE", "movies/subtitles", "radarrid=999&language=en&forced=False&hi=False&path=%2Fx.srt", 404, "\"Movie not found\"\n"},
		// A wrong series ID, a subtitle of another video.
		{"DELETE", "episodes/subtitles", "seriesid=31&episodeid=31&language=fr&forced=False&hi=False&path=%2Ftv%2FS01E01.fr.srt", 500, "\"Subtitles file not found or permission issue.\"\n"},
		{"DELETE", "movies/subtitles", "radarrid=2&language=fr&forced=False&hi=False&path=%2Ftv%2FS01E01.fr.srt", 500, "\"Subtitles file not found or permission issue.\"\n"},
		{"DELETE", "movies/subtitles", "radarrid=2&language=es&forced=False&hi=False&path=%2Fm%2FSintel.es.srt", 204, ""},
		{"DELETE", "episodes/subtitles", "seriesid=1&episodeid=31&language=fr&forced=False&hi=False&path=%2Ftv%2FS01E01.fr.srt", 204, ""},
		// Deleted, it is gone.
		{"DELETE", "movies/subtitles", "radarrid=2&language=es&forced=False&hi=False&path=%2Fm%2FSintel.es.srt", 500, "\"Subtitles file not found or permission issue.\"\n"},
	} {
		code, body := send(t, srv, tc.method, tc.call, tc.query, key)
		if code != tc.code || !strings.HasPrefix(body, tc.body) || (tc.code == 204 && body != "") {
			t.Errorf("%s %s?%s: %d %q, want %d %q", tc.method, tc.call, tc.query, code, body, tc.code, tc.body)
		}
	}
	if code, _ := send(t, srv, "PATCH", "subtitles", fix+"language=de&path=%2Fm%2FSintel.de.srt&type=movie&id=2", "wrong"); code != 401 {
		t.Errorf("a change with a wrong key: %d, want 401", code)
	}
	_, movies := send(t, srv, "GET", "movies", "", key)
	_, episodes := send(t, srv, "GET", "episodes", "seriesid[]=1", key)
	if strings.Contains(movies, "Sintel.es.srt") || strings.Contains(episodes, "S01E01.fr.srt") || !strings.Contains(movies, "Sintel.de.srt") {
		t.Errorf("after the deletes: movies %s episodes %s; want the deleted subtitles gone, the others listed", movies, episodes)
	}
}

// The queue and the histories answer in the shapes the test bed's Bazarr
// 1.6.2 gave: an ended job stays listed, history entries carry no
// identifier, and both calls need the key.
func TestSyncAnswersAsBazarr(t *testing.T) {
	srv := fakebazarr.New(t, fakebazarr.Config{Library: fakebazarr.Library{
		Movies: []fakebazarr.Movie{{RadarrID: 2, Title: "Sintel", Subtitles: []fakebazarr.Subtitle{
			{Path: "/m/Sintel.de.srt", Language: "de"}, {Path: "/m/Sintel.en.ass", Language: "en"}}}},
	}})
	key := srv.APIKey()
	sync := "action=sync&forced=False&hi=False&language=de&path=%2Fm%2FSintel.de.srt&type=movie&id=2"
	if code, body := send(t, srv, "PATCH", "subtitles", sync, key); code != 204 || body != "" {
		t.Errorf("sync: %d %q, want 204 and no body", code, body)
	}
	id, ok := srv.SyncJob("/m/Sintel.de.srt")
	if !ok {
		t.Fatal("no job queued")
	}
	srv.AddJob("Synced movies with Radarr", fakebazarr.JobCompleted)
	srv.SetJob(id, fakebazarr.JobRunning)
	srv.SyncRan(id, "11/22/26 09:17:04")
	srv.SetJob(id, fakebazarr.JobCompleted)
	_, jobs := send(t, srv, "GET", "system/jobs", "", key)
	for _, want := range []string{`{"data":[{`, `"job_name":"Synced movies with Radarr"`, `"job_name":"Synced /m/Sintel.de.srt"`, `"status":"completed"`} {
		if !strings.Contains(jobs, want) {
			t.Errorf("system/jobs %s: want it to hold %s", jobs, want)
		}
	}
	_, history := send(t, srv, "GET", "movies/history", "radarrid=2", key)
	for _, want := range []string{`"action":5`, `"parsed_timestamp":"11/22/26 09:17:04"`, `"subtitles_path":"/m/Sintel.de.srt"`, `"radarrId":2`} {
		if !strings.Contains(history, want) {
			t.Errorf("movies/history %s: want it to hold %s", history, want)
		}
	}
	if strings.Contains(history, `"id":`) {
		t.Errorf("movies/history %s: Bazarr gives entries no identifier", history)
	}
	// Bazarr 1.6.0 repeats each entry once per external subtitle (S4).
	srv.RepeatEvents(true)
	if _, repeated := send(t, srv, "GET", "movies/history", "radarrid=2", key); strings.Count(repeated, `"action":5`) != 2 {
		t.Errorf("movies/history with repeats: %s, want the event twice", repeated)
	}
	if _, other := send(t, srv, "GET", "movies/history", "radarrid=3", key); !strings.HasPrefix(other, `{"data":[],`) {
		t.Errorf("history of a movie without events: %s, want no entries", other)
	}
	for _, call := range []string{"system/jobs", "movies/history", "episodes/history"} {
		if code, _ := send(t, srv, "GET", call, "", "wrong"); code != 401 {
			t.Errorf("%s with a wrong key: %d, want 401", call, code)
		}
	}
}
