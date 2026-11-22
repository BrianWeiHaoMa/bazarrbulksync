package main_test

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/BrianWeiHaoMa/bazarrbulksync/testbed/bed"
)

// outagesVar must be 1, beside BAZARRBULKSYNC_TESTBED, for TestBedOutages to
// run: it restarts the bed's first Bazarr and syncs the bed's subtitles,
// which the other checks then no longer find as placed.
const outagesVar = "BAZARRBULKSYNC_TESTBED_OUTAGES"

// cutLasts is how long the network between a run and Bazarr is cut.
const cutLasts = time.Minute

// TestBedOutages runs `sync` through the built executable against the test
// bed's real Bazarr while Bazarr restarts with a sync of the run pending,
// and while the network between the run and Bazarr is cut for a minute with
// a sync in Bazarr's queue, as the product specification's fourth Done-when
// check describes. It runs in real time, minutes. It leaves subtitles synced
// and Bazarr restarted; `go run ./testbed up` provisions the bed again. Each
// subtest first verifies that its subtitles have no sync event, and fails
// when a premise it needs from Bazarr was not reached; run alone or in this
// order on a fresh bed, each passes.
func TestBedOutages(t *testing.T) {
	m := changingBed(t, outagesVar)
	b := newChangeBed(t, m)
	q := watchQueue(t, m)

	t.Run("restart", func(t *testing.T) {
		six := b.selected(t, "--language", "en", "--no-forced")
		if len(six) < 4 {
			t.Fatalf("the bed placed %d non-forced English subtitles, want one synced before the restart, one restarted, and two after", len(six))
		}
		b.needProvisioned(t, paths(six)...)
		b.needNoSync(t, six...)
		state := filepath.Join(t.TempDir(), "state.json")
		args := []string{"sync", "--yes", "--language", "en", "--no-forced", "--state-file", state}
		second := startTime(t, m.OnSecond())

		// Bazarr is restarted once the run's second sync is accepted and its
		// queue shows that sync pending or running, so that a subtitle synced
		// before the restart shows that Bazarr kept its history.
		var restart restarted
		var started bool
		var unconfirmedAt time.Time
		done := make(chan struct{})
		r := b.live(t, args, func(line string, _ func()) {
			if strings.HasPrefix(line, "unconfirmed  ") && unconfirmedAt.IsZero() {
				unconfirmedAt = time.Now()
			}
			if strings.HasPrefix(line, "synced  ") && !started {
				started = true
				go func() {
					defer close(done)
					restart = b.restartDuringSync(t, q)
				}()
			}
		})
		if !started {
			t.Fatalf("the run ended before Bazarr was restarted: code %d, stdout %q, stderr %q", r.code, r.stdout, r.stderr)
		}
		<-done // no restart outlives the subtest
		if t.Failed() {
			t.FailNow()
		}
		t.Logf("Bazarr did not answer for %v after the restart began\n%s", restart.away, r.stdout)

		syncs := b.syncsOfLastRun()
		b.sends(t, len(syncs))
		var submitted []string
		for _, s := range syncs {
			submitted = append(submitted, s.query.Get("path"))
			b.syncRequest(t, s, nil)
		}
		if !samePaths(submitted, paths(six)) || len(syncs) < 2 || syncs[1].query.Get("path") != restart.path {
			t.Fatalf("the run sent syncs for %v, want one for each of %v, the second restarted (%s)", submitted, paths(six), restart.path)
		}
		sub, first := b.subtitle(t, restart.path), b.subtitle(t, submitted[0])
		events := b.events(t, sub, first)
		if len(events[sub.Path]) != 0 {
			t.Fatalf("Bazarr recorded the sync of %s, which the run sent once, though the restart came while it was pending; the check shows nothing, it is not a pass", sub.Path)
		}
		if len(events[first.Path]) != 1 {
			t.Errorf("Bazarr's history holds %d sync events of %s, synced before the restart; want it kept", len(events[first.Path]), first.Path)
		}
		lines, totals, _ := strings.Cut(r.stdout, "\n\n")
		if want := "unconfirmed  " + b.listLine(t, sub) + "  (Bazarr restarted)"; !slices.Contains(strings.Split(lines, "\n"), want) {
			t.Errorf("the run printed\n%s\nwant the line %q (G20)", lines, want)
		}
		if r.code == 0 || strings.Contains(lines, "failed  ") || strings.Count(lines, "synced  ") != len(six)-1 || strings.Count(lines, "\n") != len(six)-1 {
			t.Errorf("code %d; want the restarted subtitle unconfirmed, every other synced, one line each, nonzero (G6)", r.code)
		}
		if want := fmt.Sprintf("Total: %d of %d selected subtitles\nsynced %d, unconfirmed 1\n", len(six), len(six), len(six)-1); totals != want {
			t.Errorf("totals %q, want %q", totals, want)
		}
		b.reportedSynced(t, r, slices.Delete(slices.Clone(syncs), 1, 2))
		// The run goes on to the others only once the restarted one has its
		// outcome, after Bazarr is back (G17, G20).
		for _, s := range syncs[2:] {
			if !s.sent.After(unconfirmedAt) || !s.sent.After(restart.back) {
				t.Errorf("the sync of %s was sent at %v, before Bazarr was back (%v) or the restarted subtitle was reported (%v)",
					s.query.Get("path"), s.sent.Format(time.StampMilli), restart.back.Format(time.StampMilli), unconfirmedAt.Format(time.StampMilli))
			}
		}
		// Nothing was recorded, so no state file was made (G20, G23).
		noFile(t, state)
		noFile(t, state+".tmp")
		if got := startTime(t, m.OnSecond()); got != second {
			t.Errorf("the second Bazarr's start time went from %s to %s; want it untouched", second, got)
		}

		next := b.live(t, args, nil)
		syncs = b.syncsOfLastRun()
		b.sends(t, len(syncs))
		if len(syncs) != 1 || syncs[0].query.Get("path") != sub.Path || syncs[0].status != 204 {
			t.Errorf("the next run sent %v; want one sync of %s, accepted", describe(syncs), sub.Path)
		}
		if strings.Contains(next.stdout, "earlier failure") || !strings.Contains(next.stdout, fmt.Sprintf("skipped (currently synced) %d", len(six)-1)) {
			t.Errorf("the next run printed\n%s\nwant only the restarted subtitle submitted, no earlier failure", next.stdout)
		}
		t.Logf("next run:\n%s", next.stdout)
	})

	t.Run("cut", func(t *testing.T) {
		es := b.selected(t, "--language", "es")
		if len(es) < 2 {
			t.Fatalf("the bed placed %d Spanish subtitles, want one synced through the cut and one left to submit", len(es))
		}
		b.needProvisioned(t, paths(es)...)
		b.needNoSync(t, es...)
		state := filepath.Join(t.TempDir(), "state.json")
		before := startTime(t, m)

		printed := map[string]time.Time{}
		var from, to time.Time
		done := make(chan struct{})
		b.forget()
		go func() {
			defer close(done)
			from, to = b.cutDuringSync(t, q)
		}()
		r := b.live(t, []string{"sync", "--yes", "--language", "es", "--state-file", state}, func(line string, _ func()) {
			printed[line] = time.Now()
		})
		<-done
		if t.Failed() {
			t.FailNow()
		}
		t.Logf("cut from %v to %v (%v)\n%s", from.Format(time.StampMilli), to.Format(time.StampMilli), to.Sub(from), r.stdout)

		var during []string
		b.mu.Lock()
		for _, req := range b.requests {
			if !req.sent.Before(from) && req.sent.Before(to) {
				during = append(during, fmt.Sprintf("%s %s at +%v -> %d", req.method, req.path, req.sent.Sub(from).Round(time.Millisecond), req.status))
				if req.method != "GET" {
					t.Errorf("the run sent %s %s %v during the cut", req.method, req.path, req.query)
				}
			}
			if !req.answered.IsZero() && !req.answered.Before(from) && req.answered.Before(to) {
				t.Errorf("%s %s was answered during the cut", req.method, req.path)
			}
		}
		b.mu.Unlock()
		t.Logf("the run attempted during the cut: %v", during)

		if d := to.Sub(from); d < cutLasts || d > cutLasts+3*time.Second {
			t.Errorf("the cut lasted %v, want %v within a few seconds", d, cutLasts)
		}
		syncs := b.syncsOfLastRun()
		b.sends(t, len(syncs))
		b.reportedSynced(t, r, syncs)
		var submitted []string
		for i, s := range syncs {
			submitted = append(submitted, s.query.Get("path"))
			b.syncRequest(t, s, nil)
			if i > 0 && !s.sent.After(to) {
				t.Errorf("the sync of %s was sent at %v, during the cut or before it began", s.query.Get("path"), s.sent.Format(time.StampMilli))
			}
		}
		if !samePaths(submitted, paths(es)) || len(syncs) < 2 || !syncs[0].sent.Before(from) {
			t.Fatalf("the run sent syncs for %v, want one for each of %v, the first before the cut", submitted, paths(es))
		}
		first := b.subtitle(t, syncs[0].query.Get("path"))
		line := "synced  " + b.listLine(t, first)
		if at, ok := printed[line]; !ok || !at.After(to) {
			t.Errorf("the run printed\n%s\nwant %q after the cut ended (%v), not at %v", r.stdout, line, to.Format(time.StampMilli), at.Format(time.StampMilli))
		}
		if want := fmt.Sprintf("\nTotal: %d of %d selected subtitles\nsynced %d\n", len(es), len(es), len(es)); r.code != 0 || !strings.HasSuffix(r.stdout, want) || strings.Count(r.stdout, "synced  ") != len(es) {
			t.Errorf("code %d, stdout %q, stderr %q; want every subtitle synced, totals %q, exit zero", r.code, r.stdout, r.stderr, want)
		}
		for p, events := range b.events(t, es...) {
			if len(events) != 1 {
				t.Errorf("%s has %d sync events in Bazarr's history %v, want exactly 1", p, len(events), events)
			}
		}
		if after := startTime(t, m); after != before {
			t.Errorf("Bazarr's start time went from %s to %s during the cut; want Bazarr running throughout", before, after)
		}
		noFile(t, state)
		noFile(t, state+".tmp")
	})
}

// restarted is what restartDuringSync saw.
type restarted struct {
	path string        // the subtitle whose sync the restart took
	away time.Duration // how long Bazarr did not answer
	back time.Time     // when Bazarr first answered again
}

// restartDuringSync waits for the running run's second sync to be accepted
// and for Bazarr's queue, read directly, to show it pending or running; it
// then restarts Bazarr and waits until Bazarr answers with a new start time,
// G3's five minutes at most, failing the check otherwise.
func (b *changeBed) restartDuringSync(t *testing.T, q *queueWatch) restarted {
	b.awaitSyncs(t, 2)
	syncs := b.syncsOfLastRun()
	if len(syncs) < 2 || syncs[1].status != 204 {
		t.Errorf("the run's second sync was not accepted: %v", describe(syncs))
		return restarted{}
	}
	path := syncs[1].query.Get("path")
	before, err := startTimeOf(b.Manifest)
	if err != nil {
		t.Errorf("reading Bazarr's start time: %v", err)
		return restarted{}
	}
	if !q.shows(path) {
		t.Errorf("Bazarr's queue did not show the sync of %s pending or running after it was accepted; no restart caught it", path)
		return restarted{}
	}
	began := time.Now()
	if err := b.Restart(); err != nil {
		t.Errorf("restarting Bazarr: %v", err)
		return restarted{}
	}
	for deadline := began.Add(5 * time.Minute); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		if after, err := startTimeOf(b.Manifest); err == nil && after != before {
			back := time.Now()
			return restarted{path: path, back: back, away: back.Sub(began)}
		}
	}
	t.Errorf("Bazarr did not answer with a new start time within 5 minutes of the restart")
	return restarted{}
}

// cutDuringSync waits for the running run's first sync to be accepted, cuts
// the network, and fails the check unless Bazarr's queue, read directly once
// the cut began, shows that sync pending or running; it ends the cut after
// cutLasts and returns when the cut began and ended.
func (b *changeBed) cutDuringSync(t *testing.T, q *queueWatch) (from, to time.Time) {
	b.awaitSyncs(t, 1)
	syncs := b.syncsOfLastRun()
	if len(syncs) != 1 || syncs[0].status != 204 {
		t.Errorf("the run's first sync was not accepted alone: %v", describe(syncs))
		return
	}
	from = b.startCut()
	defer func() { to = b.endCut() }()
	if path := syncs[0].query.Get("path"); !q.shows(path) {
		t.Errorf("Bazarr's queue did not show the sync of %s pending or running once the cut began; the cut caught no sync", path)
		return
	}
	time.Sleep(time.Until(from.Add(cutLasts)))
	return
}

// startTime reads Bazarr's start time directly.
func startTime(t *testing.T, m bed.Manifest) string {
	t.Helper()
	s, err := startTimeOf(m)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// startTimeOf reads Bazarr's start time directly, as Bazarr's own JSON.
func startTimeOf(m bed.Manifest) (string, error) {
	var status struct {
		Data struct {
			StartTime json.RawMessage `json:"start_time"`
		} `json:"data"`
	}
	err := m.Get("system/status", &status)
	return string(status.Data.StartTime), err
}
