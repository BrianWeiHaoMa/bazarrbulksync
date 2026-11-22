package main_test

import (
	"bytes"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/bazarr"
)

// pathsVar must be 1, beside BAZARRBULKSYNC_TESTBED, for TestBedPaths to run:
// it syncs and uploads the bed's subtitles, which the other checks then no
// longer find as placed.
const pathsVar = "BAZARRBULKSYNC_TESTBED_PATHS"

// TestBedPaths runs `sync` through the built executable against the test
// bed's two real Bazarrs, on what can share one subtitle path: the two
// episodes of one file, a subtitle uploaded through Bazarr over a recorded
// failure, and a second Bazarr server showing the same library, as the
// client's events of a subtitle and the end of the product specification's
// third Done-when check describe. It leaves subtitles synced, uploaded, and
// failed in Bazarr's histories; `go run ./testbed up` provisions the bed
// again. Each subtest first verifies that what it needs is as the bed placed
// it; run alone or in this order on a fresh bed, each passes.
func TestBedPaths(t *testing.T) {
	m := changingBed(t, pathsVar)
	b := newChangeBed(t, m)

	t.Run("two episodes", func(t *testing.T) {
		i := slices.IndexFunc(m.Subtitles, func(s bazarr.Subtitle) bool { return len(placedAt(m, s.Path)) > 1 })
		if i < 0 {
			t.Fatalf("the bed placed no file holding two episodes")
		}
		shared, path := placedAt(m, m.Subtitles[i].Path), m.Subtitles[i].Path
		b.needProvisioned(t, path)
		b.needNoSync(t, shared[0])
		args := []string{"--title", shared[0].Title, "--season", strconv.Itoa(shared[0].Season), "--language", shared[0].Language}
		if got := b.selected(t, args...); len(got) != 2 || !slices.Contains(got, shared[0]) || !slices.Contains(got, shared[1]) {
			t.Fatalf("%q selects %+v, want the file's subtitle under each of its two episodes %+v", args, got, shared)
		}
		lines := b.listLines(t, args...)
		if len(lines) != 2 {
			t.Fatalf("list printed %q, want the file's subtitle under each of its two episodes", lines)
		}
		for j, l := range lines {
			if !strings.HasPrefix(l, shared[0].Title+" "+videoOf(shared[j])+"  ") || !strings.HasSuffix(l, "  "+path) {
				t.Errorf("list printed %q, want %s of %s", lines, path, videoOf(shared[j]))
			}
		}
		state := onHost(t).stateFile(t)
		run := append([]string{"sync", "--yes", "--state-file", state}, args...)

		r := b.live(t, run, nil)
		syncs := b.syncsOfLastRun()
		b.sends(t, len(syncs))
		if len(syncs) != 1 || syncs[0].status != 204 || syncs[0].query.Get("path") != path {
			t.Fatalf("the run sent %v; want one sync of %s, accepted with 204", describe(syncs), path)
		}
		b.syncRequest(t, syncs[0], nil)
		var synced []string
		for _, l := range strings.Split(r.stdout, "\n") {
			if strings.HasPrefix(l, "synced  ") {
				synced = append(synced, l)
			}
		}
		if r.code != 0 || len(synced) != 1 || !strings.HasSuffix(r.stdout, "\nsynced 1, skipped (currently synced) 1\n") {
			t.Errorf("code %d, stdout %q, stderr %q; want one listing synced, the other counted currently synced, exit zero", r.code, r.stdout, r.stderr)
		}
		events := b.events(t, shared[0])[path]
		if len(events) != 1 {
			t.Errorf("the histories of both episodes hold %d sync events for %s %v, want exactly 1", len(events), path, events)
		}
		for _, s := range shared {
			t.Logf("Bazarr %s: the history of %s holds %v", m.BazarrImage, videoOf(s), b.ownHistory(t, s))
		}

		r = b.live(t, run, nil)
		b.sends(t, 0)
		if r.code != 0 || r.stdout != "\nTotal: 2 of 2 selected subtitles\nskipped (currently synced) 2\n" {
			t.Errorf("the second run: code %d, stdout %q, stderr %q; want both listings skipped as currently synced", r.code, r.stdout, r.stderr)
		}
		noFile(t, state)
	})

	t.Run("upload", func(t *testing.T) {
		state := onHost(t).stateFile(t)
		sub, args, line := b.recordFailure(t, state)
		old, _ := b.record(t, state, b.proxy, sub.Path)
		data, err := b.ReadFile(sub.Path)
		if err != nil {
			t.Fatal(err)
		}
		uploaded := fmt.Sprintf("%d %s", bazarr.EventUpload, b.upload(t, sub, data).Time)
		b.needShown(t, sub)
		if slices.Contains(old, uploaded) {
			t.Fatalf("the record %v already holds the upload's identity %q", old, uploaded)
		}
		if !b.recorded(t, state, b.proxy, sub.Path) {
			t.Fatalf("after the upload the state file holds no failure for %s under %s, so the next run shows nothing", sub.Path, b.proxy)
		}

		r := b.live(t, args, nil)
		syncs := b.syncsOfLastRun()
		b.sends(t, len(syncs))
		if len(syncs) != 1 || syncs[0].status != 204 || syncs[0].query.Get("path") != sub.Path {
			t.Fatalf("the plain run after the upload sent %v; want the sync of %s submitted again (G21)", describe(syncs), sub.Path)
		}
		b.syncRequest(t, syncs[0], map[string]string{"reference": "a:5"})
		// What Bazarr makes of the sync is Bazarr's; the record follows it.
		now, held := b.record(t, state, b.proxy, sub.Path)
		t.Logf("the record before the upload %v, after the plain run %v; the run printed %q", old, now, r.stdout)
		switch {
		case strings.HasPrefix(r.stdout, "synced  "+line):
			if held || r.code != 0 {
				t.Errorf("%s was synced, the run exited %d, and the state file still holds its failure: %v", sub.Path, r.code, held)
			}
		case strings.HasPrefix(r.stdout, "failed  "+line):
			if !slices.Contains(now, uploaded) || r.code == 0 {
				t.Errorf("%s failed again, the run exited %d, and its record holds %v, want the upload's identity %q in it", sub.Path, r.code, now, uploaded)
			}
		default:
			t.Errorf("the plain run after the upload: code %d, stdout %q, stderr %q; want %s synced or failed", r.code, r.stdout, r.stderr, sub.Path)
		}
	})

	t.Run("second server", func(t *testing.T) {
		second := newChangeBed(t, m.OnSecond())
		state := onHost(t).stateFile(t)
		sub, args, _ := b.recordFailure(t, state)
		second.needShown(t, sub)
		second.needNoSync(t, sub)
		if !b.recorded(t, state, b.proxy, sub.Path) {
			t.Fatalf("the state file holds no failure for %s under %s, so the run against the second server shows nothing", sub.Path, b.proxy)
		}

		r := second.live(t, args, nil)
		syncs := second.syncsOfLastRun()
		second.sends(t, len(syncs))
		if len(syncs) != 1 || syncs[0].status != 204 || syncs[0].query.Get("path") != sub.Path {
			t.Errorf("against the second server the run sent %v; want the sync of %s submitted (G21: a failure is recorded under the Bazarr address)", describe(syncs), sub.Path)
		} else {
			second.syncRequest(t, syncs[0], map[string]string{"reference": "a:5"})
		}
		t.Logf("against the second server: code %d, stdout %q", r.code, r.stdout)
		// What the second server makes of the sync is its own; a failure it
		// ran is recorded in the same file under its address (G21).
		switch held := b.recorded(t, state, second.proxy, sub.Path); {
		case strings.HasPrefix(r.stdout, "failed  "):
			if !held || r.code == 0 {
				t.Errorf("against the second server %s failed, the run exited %d, and the state file holds a failure under %s: %v; want one, nonzero", sub.Path, r.code, second.proxy, held)
			}
		case strings.HasPrefix(r.stdout, "synced  "):
			if held || r.code != 0 {
				t.Errorf("against the second server %s was synced, the run exited %d, and the state file holds a failure under %s: %v", sub.Path, r.code, second.proxy, held)
			}
		default:
			t.Errorf("against the second server: code %d, stdout %q, stderr %q; want %s synced or failed", r.code, r.stdout, r.stderr, sub.Path)
		}
		if !b.recorded(t, state, b.proxy, sub.Path) {
			t.Fatalf("after the run against the second server the state file holds no failure for %s under %s", sub.Path, b.proxy)
		}

		r = b.live(t, args, nil)
		b.sends(t, 0)
		if r.code != 0 || r.stdout != "\nTotal: 1 of 1 selected subtitle\nskipped (earlier failure) 1\n" {
			t.Errorf("against the first server again: code %d, stdout %q, stderr %q; want it skipped for its earlier failure, exit zero", r.code, r.stdout, r.stderr)
		}
	})
}

// ownHistory returns the events of s's own video's history for its path,
// each as "<action> <parsed_timestamp>", read from Bazarr directly.
func (b *changeBed) ownHistory(t *testing.T, s bazarr.Subtitle) []string {
	t.Helper()
	call := fmt.Sprintf("movies/history?radarrid=%d", s.MovieID)
	if s.Type == bazarr.TypeEpisode {
		call = fmt.Sprintf("episodes/history?episodeid=%d", s.EpisodeID)
	}
	var answer struct{ Data []historyEvent }
	if err := b.Get(call, &answer); err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range answer.Data {
		if e.Path == s.Path {
			out = append(out, fmt.Sprintf("%d %s", e.Action, e.Time))
		}
	}
	return out
}

// upload uploads data through Bazarr's own upload call as the subtitle of
// the movie sub (a movie's: the failure the checks make is one), with its
// language and flags, so that Bazarr stores it at sub's path, and waits until
// Bazarr's history shows the upload event for that path, which it returns.
func (b *changeBed) upload(t *testing.T, sub bazarr.Subtitle, data []byte) historyEvent {
	t.Helper()
	if sub.Type != bazarr.TypeMovie {
		t.Fatalf("upload takes a movie's subtitle, not %+v", sub)
	}
	before := map[historyEvent]bool{}
	for _, e := range b.history(t, sub.Path) {
		before[e] = true
	}
	// Bazarr 1.6.2's upload fails, after answering 204, for a video without
	// a language profile (subtitles/upload.py reads the profile's
	// originalFormat), so the movie gets one of its language first.
	profiles := fmt.Sprintf(`[{"profileId":1,"name":"upload","cutoff":null,"items":[{"id":1,"language":%q,`+
		`"audio_exclude":"False","audio_only_include":"False","hi":"False","forced":"False"}],`+
		`"mustContain":[],"mustNotContain":[],"originalFormat":false,"tag":null}]`, sub.Language)
	b.post(t, "system/settings", url.Values{"languages-profiles": {profiles}}, nil)
	b.post(t, "movies", url.Values{"radarrid": {strconv.Itoa(sub.MovieID)}, "profileid": {"1"}}, nil)
	flag := map[bool]string{true: "true", false: "false"}
	b.post(t, "movies/subtitles", url.Values{"radarrid": {strconv.Itoa(sub.MovieID)}, "language": {sub.Language},
		"forced": {flag[sub.Forced]}, "hi": {flag[sub.HI]}}, data)
	for deadline := time.Now().Add(time.Minute); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		for _, e := range b.history(t, sub.Path) {
			if e.Action == int(bazarr.EventUpload) && !before[e] {
				return e
			}
		}
	}
	t.Fatalf("within a minute of the upload Bazarr's history shows no upload event for %s", sub.Path)
	return historyEvent{}
}

// post sends form to one of Bazarr's API calls directly, not through the
// proxy, as a multipart form carrying file as its file when file is not
// nil, and requires success.
func (b *changeBed) post(t *testing.T, call string, form url.Values, file []byte) {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	for k, vs := range form {
		for _, v := range vs {
			if err := w.WriteField(k, v); err != nil {
				t.Fatal(err)
			}
		}
	}
	if file != nil {
		part, err := w.CreateFormFile("file", "upload.srt")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write(file); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest("POST", b.URL+"/api/"+call, &body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("X-API-KEY", b.APIKey)
	resp, err := directClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		t.Fatalf("POST %s: HTTP %d", call, resp.StatusCode)
	}
}

// directClient follows no redirect, which would carry the key elsewhere.
var directClient = &http.Client{Timeout: 30 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
