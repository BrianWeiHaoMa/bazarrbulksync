package main_test

import (
	"encoding/json"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/bazarr"
)

// syncVar must be 1, beside BAZARRBULKSYNC_TESTBED, for TestBedSync to run:
// it has Bazarr sync the bed's subtitles, which the read checks and the
// changing checks then no longer find as placed.
const syncVar = "BAZARRBULKSYNC_TESTBED_SYNC"

// TestBedSync runs `sync` through the built executable against the test
// bed's real Bazarr while it reads Bazarr's queue and histories itself, as
// the product specification's second Done-when check and the opening of its
// third describe. It leaves the bed's subtitles synced; `go run ./testbed up`
// provisions it again. Each subtest first verifies that its subtitles have
// no sync event, so that none passes because another ran first; run alone
// or in this order on a fresh bed, each passes.
func TestBedSync(t *testing.T) {
	m := changingBed(t, syncVar)
	b := newChangeBed(t, m)
	q := watchQueue(t, m)

	t.Run("dry run", func(t *testing.T) {
		b.needProvisioned(t, paths(m.Subtitles)...)
		b.needNoSync(t, m.Subtitles...)
		state := onHost(t).stateFile(t)
		events, files := b.events(t, m.Subtitles...), b.files(t)
		var want []string
		for _, l := range b.listLines(t) {
			i := slices.IndexFunc(m.Subtitles, func(s bazarr.Subtitle) bool { return strings.HasSuffix(l, "  "+s.Path) })
			if i >= 0 && m.Subtitles[i].Forced {
				want = append(want, "would skip  "+l+"  (forced)")
			} else {
				want = append(want, "would sync  "+l)
			}
		}
		for _, s := range m.Subtitles {
			if n := len(slices.DeleteFunc(slices.Clone(want), func(l string) bool {
				_, l, _ = strings.Cut(l, "  ")
				return !strings.HasPrefix(l, s.Title+" "+videoOf(s)+"  ") ||
					!strings.HasSuffix(l, "  "+s.Path) && !strings.HasSuffix(l, "  "+s.Path+"  (forced)")
			})); n != 1 {
				t.Fatalf("list prints %d lines for %s of %s, want 1", n, s.Path, videoOf(s))
			}
		}
		r := b.run(t, 0, "sync", "--dry-run", "--state-file", state)
		b.sends(t, 0)
		lines, totals, _ := strings.Cut(r.stdout, "\n\n")
		if got := strings.Split(lines, "\n"); !slices.Equal(got, want) {
			t.Errorf("sync --dry-run printed\n%s\nwant\n%s", lines, strings.Join(want, "\n"))
		}
		if wantTotals := fmt.Sprintf("Total: %d of %d selected subtitles\nwould sync %d, skipped (forced) 1\n",
			len(want), len(want), len(want)-1); totals != wantTotals {
			t.Errorf("totals %q, want %q", totals, wantTotals)
		}
		if got := b.events(t, m.Subtitles...); !sameEvents(got, events) {
			t.Errorf("Bazarr's history changed: %v before, %v after", events, got)
		}
		b.unchanged(t, files, b.files(t))
		noFile(t, state)
		noFile(t, state+".tmp")
	})

	t.Run("six", func(t *testing.T) {
		six := b.selected(t, "--language", "en", "--no-forced")
		types := map[bazarr.VideoType]bool{}
		for _, s := range six {
			types[s.Type] = true
		}
		if len(six) != 6 || len(types) != 2 {
			t.Fatalf("the bed placed %d non-forced English subtitles, of %d types; want six, movies and episodes among them", len(six), len(types))
		}
		b.needNoSync(t, six...)
		state := onHost(t).stateFile(t)
		args := []string{"sync", "--yes", "--language", "en", "--no-forced", "--state-file", state}
		from := q.mark()

		// The first run is interrupted once two subtitles are reported
		// synced and Bazarr has accepted a third sync, so that the second
		// run starts while that sync may still be in Bazarr.
		reported, interrupted := 0, make(chan time.Time, 1)
		first := b.live(t, args, func(line string, stop func()) {
			if !strings.HasPrefix(line, "synced  ") {
				return
			}
			if reported++; reported == 2 {
				go func() {
					b.awaitSyncs(t, 3)
					interrupted <- time.Now()
					stop()
				}()
			}
		})
		ended, afterFirst := time.Now(), q.mark()
		runs := [][]*sentRequest{b.syncsOfLastRun()}
		b.sends(t, len(runs[0]))
		if first.code == 0 || !strings.Contains(first.stdout, "synced 2") || strings.Count(first.stdout, "synced  ") != 2 {
			t.Errorf("the interrupted run: code %d, stdout %q, stderr %q; want two synced lines, totals counting them, nonzero (G4)", first.code, first.stdout, first.stderr)
		}
		if len(runs[0]) != 3 {
			t.Fatalf("the interrupted run sent %d syncs, want 3: two reported, one left in Bazarr", len(runs[0]))
		}
		b.reportedSynced(t, first, runs[0][:2])
		if left := runs[0][2].query.Get("path"); strings.Contains(first.stdout, left) {
			t.Errorf("the interrupted run printed a line for %s, whose sync it left in Bazarr (G4):\n%s", left, first.stdout)
		}
		select {
		case stopped := <-interrupted:
			if took := ended.Sub(stopped); took > 3*time.Second {
				t.Errorf("the interrupted run took %v to exit after the interrupt; want it not to wait for Bazarr (G4)", took)
			}
		default:
			t.Fatalf("the run ended before it was interrupted: stdout %q", first.stdout)
		}

		second := b.live(t, args, nil)
		runs = append(runs, b.syncsOfLastRun())
		b.sends(t, len(runs[1]))
		b.reportedSynced(t, second, runs[1])
		if skipped := fmt.Sprintf("skipped (currently synced) %d", 6-len(runs[1])); !strings.Contains(second.stdout, skipped) {
			t.Errorf("the second run printed\n%s\nwant its totals to count %q", second.stdout, skipped)
		}
		if second.code != 0 || strings.Contains(second.stdout, "failed") || strings.Contains(second.stdout, "unconfirmed") {
			t.Errorf("the second run: code %d, stdout %q, stderr %q; want it to end with exit zero", second.code, second.stdout, second.stderr)
		}
		var submitted []string
		for _, run := range runs {
			for _, s := range run {
				submitted = append(submitted, s.query.Get("path"))
				b.syncRequest(t, s, nil)
			}
		}
		if !samePaths(submitted, paths(six)) {
			t.Errorf("the two runs sent syncs for %v, want one for each of %v", submitted, paths(six))
		}
		t.Logf("first run:\n%s\nsecond run:\n%s", first.stdout, second.stdout)
		most := q.mostAtOnce(t, from, runs)
		t.Logf("most syncs of each run in Bazarr at once: %v", most)
		// Otherwise the first run may have waited for it, and the second run
		// never met a sync of the run before in Bazarr (G4, G18).
		if left := runs[0][2].query.Get("path"); !q.sawActive(afterFirst, left) {
			t.Fatalf("the sync of %s the first run left had ended before that run did; the check shows nothing, run it again", left)
		}
		if slices.Max(most) > 1 {
			t.Errorf("Bazarr's queue held at once %v syncs of each run, want at most 1 (G17)", most)
		}
		events := b.events(t, six...)
		for _, s := range six {
			if n := len(events[s.Path]); n != 1 {
				t.Errorf("%s has %d sync events in Bazarr's history %v, want exactly 1", s.Path, n, events[s.Path])
			}
		}

		third := b.live(t, args, nil)
		b.sends(t, 0)
		if third.code != 0 || third.stdout != "\nTotal: 6 of 6 selected subtitles\nskipped (currently synced) 6\n" {
			t.Errorf("the third run: code %d, stdout %q, stderr %q; want all six skipped as currently synced, exit zero", third.code, third.stdout, third.stderr)
		}
		if got := b.events(t, six...); !sameEvents(got, events) {
			t.Errorf("Bazarr's history changed during the third run: %v before, %v after", events, got)
		}
		noFile(t, state)
	})

	t.Run("concurrency 2", func(t *testing.T) {
		es := b.selected(t, "--language", "es")
		if len(es) != 3 {
			t.Fatalf("the bed placed %d Spanish subtitles, want 3: two at once and a third waiting", len(es))
		}
		b.needNoSync(t, es...)
		from := q.mark()
		r := b.live(t, []string{"sync", "--yes", "--language", "es", "--concurrency", "2", "--state-file", onHost(t).stateFile(t)}, nil)
		run := b.syncsOfLastRun()
		if r.code != 0 || strings.Count(r.stdout, "synced  ") != 3 || len(run) != 3 {
			t.Errorf("code %d, stdout %q, stderr %q; want each subtitle synced by one request, exit zero", r.code, r.stdout, r.stderr)
		}
		b.sends(t, len(run))
		b.reportedSynced(t, r, run)
		for _, s := range run {
			b.syncRequest(t, s, nil)
		}
		if most := q.mostAtOnce(t, from, [][]*sentRequest{run}); most[0] != 2 {
			t.Errorf("Bazarr's queue held at most %d syncs of the run at once, want 2 at some moment and never 3 (G17)", most[0])
		}
		for p, events := range b.events(t, es...) {
			if len(events) != 1 {
				t.Errorf("%s has %d sync events in Bazarr's history %v, want exactly 1", p, len(events), events)
			}
		}
	})

	t.Run("failed", func(t *testing.T) {
		state := onHost(t).stateFile(t)
		sub, args, line := b.recordFailure(t, state)

		r := b.live(t, args, nil)
		b.sends(t, 0)
		if r.code != 0 || r.stdout != "\nTotal: 1 of 1 selected subtitle\nskipped (earlier failure) 1\n" {
			t.Errorf("the next plain run: code %d, stdout %q, stderr %q; want it skipped for its earlier failure, exit zero", r.code, r.stdout, r.stderr)
		}
		if !b.recorded(t, state, b.proxy, sub.Path) {
			t.Fatalf("after the plain run the state file holds no failure for %s, so --retry-failed would show nothing", sub.Path)
		}

		r = b.live(t, append(args, "--retry-failed"), nil)
		if syncs := b.syncsOfLastRun(); len(syncs) != 1 || syncs[0].status != 204 || syncs[0].query.Get("path") != sub.Path {
			t.Errorf("sync --retry-failed sent %v; want the sync of %s submitted again", describe(syncs), sub.Path)
		} else {
			b.syncRequest(t, syncs[0], map[string]string{"reference": "a:5"})
		}
		b.sends(t, 1)
		if r.code == 0 || !strings.HasPrefix(r.stdout, "failed  "+line) {
			t.Errorf("sync --retry-failed: code %d, stdout %q; want it failed again, nonzero", r.code, r.stdout)
		}
		if !b.recorded(t, state, b.proxy, sub.Path) {
			t.Errorf("after it failed again the state file holds no failure for %s under %s", sub.Path, b.proxy)
		}
		if events := b.events(t, sub)[sub.Path]; len(events) != 0 {
			t.Errorf("Bazarr's history holds sync events %v of %s, which the runs reported failed", events, sub.Path)
		}
	})
}

// recordFailure makes the failure of the third Done-when check on the one
// German movie subtitle, which needs no sync event yet: `sync` with args,
// its state file at state, against an audio track the video lacks, which
// Bazarr accepts and runs without a sync event. It requires the subtitle
// reported failed and recorded under the address of b's proxy, and returns
// the subtitle, args, and the subtitle's list line.
func (b *changeBed) recordFailure(t *testing.T, state string) (sub bazarr.Subtitle, args []string, line string) {
	t.Helper()
	return b.recordFailureAt(t, "de", state, "--state-file", state)
}

// recordFailureAt makes the failure as recordFailure does, on the one movie
// subtitle in language lang, the run given stateArgs, which leave its state
// file at state.
func (b *changeBed) recordFailureAt(t *testing.T, lang, state string, stateArgs ...string) (sub bazarr.Subtitle, args []string, line string) {
	t.Helper()
	movie := b.selected(t, "--language", lang, "--movies")
	if len(movie) != 1 {
		t.Fatalf("the bed placed %d movie subtitles in %s, want 1", len(movie), lang)
	}
	sub = movie[0]
	b.needNoSync(t, sub)
	// The video has one audio track.
	args = append([]string{"sync", "--yes", "--language", lang, "--movies", "--reference", "a:5"}, stateArgs...)
	line = b.listLine(t, sub)

	r := b.live(t, args, nil)
	syncs := b.syncsOfLastRun()
	b.sends(t, len(syncs))
	if len(syncs) != 1 || syncs[0].status != 204 || syncs[0].query.Get("path") != sub.Path {
		t.Fatalf("the run sent %v; want one sync of %s, accepted with 204", describe(syncs), sub.Path)
	}
	b.syncRequest(t, syncs[0], map[string]string{"reference": "a:5"})
	if r.code == 0 || !strings.HasPrefix(r.stdout, "failed  "+line) || strings.Contains(r.stdout, "synced") {
		t.Errorf("code %d, stdout %q, stderr %q; want %s reported failed, nonzero", r.code, r.stdout, r.stderr, sub.Path)
	}
	if !b.recorded(t, state, b.proxy, sub.Path) {
		t.Fatalf("the state file holds no failure for %s under %s", sub.Path, b.proxy)
	}
	return sub, args, line
}

// selected returns the subtitles `list` selects with args, as the bed placed
// them.
func (b *changeBed) selected(t *testing.T, args ...string) []bazarr.Subtitle {
	t.Helper()
	var out []bazarr.Subtitle
	for _, obj := range listJSON(t, b.Manifest, args...) {
		s, ok := placedAs(b.Manifest, obj)
		if !ok {
			t.Fatalf("list --json printed %+v, which the bed did not place", obj)
		}
		out = append(out, s)
	}
	return out
}

// needNoSync stops the subtest unless Bazarr's history holds no sync event
// for any of subs.
func (b *changeBed) needNoSync(t *testing.T, subs ...bazarr.Subtitle) {
	t.Helper()
	for p, events := range b.events(t, subs...) {
		if len(events) > 0 {
			t.Fatalf("Bazarr's history holds a sync of %s; run `go run ./testbed up` before these checks", p)
		}
	}
}

// historyEvent is an entry of a video's history as Bazarr answers it.
type historyEvent struct {
	Action int    `json:"action"`
	Path   string `json:"subtitles_path"`
	Time   string `json:"parsed_timestamp"`
}

// events returns, for each of subs, the times of the sync events for its
// path in the histories of every video the bed placed a subtitle at that
// path for (a file holding two episodes is in both episodes' histories),
// read from Bazarr directly.
func (b *changeBed) events(t *testing.T, subs ...bazarr.Subtitle) map[string][]string {
	t.Helper()
	out := map[string][]string{}
	for _, s := range subs {
		out[s.Path] = []string{}
		for _, e := range b.history(t, s.Path) {
			if e.Action == 5 {
				out[s.Path] = append(out[s.Path], e.Time)
			}
		}
	}
	return out
}

// history returns the events for path in the histories of every video the
// bed placed a subtitle at path for, read from Bazarr directly.
func (b *changeBed) history(t *testing.T, path string) []historyEvent {
	t.Helper()
	var out []historyEvent
	for _, s := range placedAt(b.Manifest, path) {
		call := fmt.Sprintf("movies/history?radarrid=%d", s.MovieID)
		if s.Type == bazarr.TypeEpisode {
			call = fmt.Sprintf("episodes/history?episodeid=%d", s.EpisodeID)
		}
		var answer struct{ Data []historyEvent }
		if err := b.Get(call, &answer); err != nil {
			t.Fatal(err)
		}
		for _, e := range answer.Data {
			if e.Path == path {
				out = append(out, e)
			}
		}
	}
	return out
}

func sameEvents(a, b map[string][]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if w, ok := b[k]; !ok || !slices.Equal(v, w) {
			return false
		}
	}
	return true
}

// recorded reports whether the state file at path, where the product runs,
// holds a failure of subtitle under address.
func (b *changeBed) recorded(t *testing.T, path, address, subtitle string) bool {
	t.Helper()
	_, ok := b.record(t, path, address, subtitle)
	return ok
}

// record returns the event identities of the failure of subtitle the state
// file at path holds under address, and whether it holds one.
func (b *changeBed) record(t *testing.T, path, address, subtitle string) ([]string, bool) {
	t.Helper()
	data, err := onHost(t).readFile(path)
	if err != nil {
		t.Errorf("the state file: %v", err)
		return nil, false
	}
	var state struct {
		Servers map[string]map[string][]string `json:"servers"`
	}
	if err := json.Unmarshal(data, &state); err != nil {
		t.Errorf("the state file: %v", err)
		return nil, false
	}
	if strings.Contains(string(data), b.APIKey) {
		t.Fatalf("the state file holds the API key (R2)")
	}
	identities, ok := state.Servers[address][subtitle]
	return identities, ok
}

// noFile reports a file at path where the product runs.
func noFile(t *testing.T, path string) {
	t.Helper()
	if there, err := onHost(t).exists(path); there || err != nil {
		t.Errorf("%s: there %v, %v; want no file there", path, there, err)
	}
}

// asReported checks Bazarr, read directly as soon as a run prints line, for
// what a `synced` or `failed` line says: Bazarr no longer shows the sync
// queued or running, and its history holds a sync event of the subtitle
// for `synced` and none for `failed` (G19).
func (b *changeBed) asReported(t *testing.T, line string) {
	t.Helper()
	word, _, _ := strings.Cut(line, "  ")
	if word != "synced" && word != "failed" {
		return
	}
	i := slices.IndexFunc(b.Subtitles, func(s bazarr.Subtitle) bool {
		return strings.HasSuffix(line, "  "+s.Path) || strings.Contains(line, "  "+s.Path+"  (")
	})
	if i < 0 {
		t.Errorf("the line %q names no subtitle the bed placed", line)
		return
	}
	s := b.Subtitles[i]
	var answer struct{ Data []queuedJob }
	if err := b.Get("system/jobs", &answer); err != nil {
		t.Errorf("reading Bazarr's queue: %v", err)
		return
	}
	for _, j := range answer.Data {
		if (j.Name == "Syncing "+s.Path || j.Name == "Synced "+s.Path) && (j.Status == "pending" || j.Status == "running") {
			t.Errorf("%q printed while Bazarr shows the sync %s", line, j.Status)
		}
	}
	if n := len(b.events(t, s)[s.Path]); (word == "synced") != (n > 0) {
		t.Errorf("%q printed while Bazarr's history holds %d sync events of it", line, n)
	}
}

// reportedSynced checks that r printed a `synced` line for the subtitle of
// each of syncs, and no other.
func (b *changeBed) reportedSynced(t *testing.T, r result, syncs []*sentRequest) {
	t.Helper()
	var want, got []string
	for _, s := range syncs {
		want = append(want, s.query.Get("path"))
	}
	for _, l := range strings.Split(r.stdout, "\n") {
		if strings.HasPrefix(l, "synced  ") {
			for _, s := range b.Subtitles {
				if strings.HasSuffix(l, "  "+s.Path) {
					got = append(got, s.Path)
					break
				}
			}
		}
	}
	if !samePaths(got, want) {
		t.Errorf("the run printed synced lines for %v, want one for each subtitle it submitted, %v:\n%s", got, want, r.stdout)
	}
}

// syncRequest checks that req holds exactly the parameters a sync of the
// placed subtitle at its path takes, with the sync options of extra and no
// others (G16, the technical specification's table).
func (b *changeBed) syncRequest(t *testing.T, req *sentRequest, extra map[string]string) {
	t.Helper()
	// Of a file holding two episodes, the subtitle of the episode sent.
	at := placedAt(b.Manifest, req.query.Get("path"))
	i := slices.IndexFunc(at, func(s bazarr.Subtitle) bool {
		return req.query.Get("id") == strconv.Itoa(max(s.MovieID, s.EpisodeID))
	})
	if i < 0 {
		t.Errorf("a sync of %v, which is none of the subtitles the bed placed", req.query)
		return
	}
	s := at[i]
	id := max(s.MovieID, s.EpisodeID)
	title := map[bool]string{true: "True", false: "False"}
	want := url.Values{"action": {"sync"}, "language": {s.Language}, "path": {s.Path}, "type": {string(s.Type)},
		"id": {strconv.Itoa(id)}, "forced": {title[s.Forced]}, "hi": {title[s.HI]}}
	for k, v := range extra {
		want.Set(k, v)
	}
	if req.query.Encode() != want.Encode() {
		t.Errorf("the sync of %s carried %v, want %v", s.Path, req.query, want)
	}
}
