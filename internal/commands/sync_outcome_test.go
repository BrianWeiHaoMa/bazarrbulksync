package commands_test

import (
	"context"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/fakebazarr"
	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/fakeclock"
)

// TestSyncEnding: a sync has ended only when two queue reads, the later
// sent at least five seconds after the earlier was answered, show no sync
// job of it; a job shown in neither queue for one read is still followed
// (G19, G20, S2).
func TestSyncEnding(t *testing.T) {
	srv := fakebazarr.New(t, fakebazarr.Config{Library: syncLibrary()})
	s := startSync(t, srv, "--movie-id", "7", "--language", "es")
	s.until("submitted", func() bool { return len(syncsSent(srv)) == 1 }, nil)
	id, _ := srv.SyncJob(sintelEs)
	srv.SetJob(id, fakebazarr.JobMoving)
	s.step()
	s.idle()
	srv.SetJob(id, fakebazarr.JobRunning)
	for range 3 {
		s.step()
		s.idle()
		if lines := resultLines(s.stdout.String()); len(lines) != 0 {
			t.Fatalf("an outcome while the sync still runs: %q", lines)
		}
	}
	srv.SyncRan(id, "11/22/26 08:15:02")
	srv.SetJob(id, fakebazarr.JobCompleted)
	if o := s.finish(nil); outcomeOf(t, o.stdout, sintelEs) != "synced" || o.code != 0 {
		t.Errorf("exit %d\n%s", o.code, o.stdout)
	}
}

// TestSyncNewEvent: `synced` needs a sync event the history did not show
// just before the submission; with --resync on a currently synced
// subtitle, a sync that writes none is `failed`. The events are read after
// the queue shows the sync gone (G19, R4).
func TestSyncNewEvent(t *testing.T) {
	for _, wrote := range []bool{true, false} {
		srv := fakebazarr.New(t, fakebazarr.Config{Library: syncLibrary()})
		srv.AddMovieEvent(7, fakebazarr.Event{Action: 5, Time: "11/21/26 10:00:00", Path: sintelEs})
		s := startSync(t, srv, "--movie-id", "7", "--language", "es", "--resync")
		s.until("submitted", func() bool { return len(syncsSent(srv)) == 1 }, nil)
		id, _ := srv.SyncJob(sintelEs)
		srv.SetJob(id, fakebazarr.JobRunning)
		if wrote {
			srv.SyncRan(id, "11/22/26 08:15:02")
		} else {
			srv.SyncRan(id, "")
		}
		for range 3 { // the job, renamed, is still running
			s.step()
			s.idle()
			if lines := resultLines(s.stdout.String()); len(lines) != 0 {
				t.Fatalf("an outcome while the job is queued: %q", lines)
			}
		}
		submitted := len(srv.Requests())
		srv.SetJob(id, fakebazarr.JobCompleted)
		o := s.finish(nil)
		want, code := "synced", 0
		if !wrote {
			want, code = "failed", 1
		}
		if got := outcomeOf(t, o.stdout, sintelEs); got != want || o.code != code {
			t.Errorf("wrote %v: %s, exit %d, want %s\n%s", wrote, got, o.code, want, o.stdout)
		}
		after := calls(srv)[submitted:]
		events := slices.Index(after, "movies/history")
		if events < 2 || after[events-1] != "system/jobs" || after[events-2] != "system/jobs" {
			t.Errorf("wrote %v: reads after the job ended %q; the events come after two queue reads", wrote, after)
		}
	}
}

// TestSyncRefused: a refused submission is `failed` at once with Bazarr's
// reason, frees its slot, and the run goes on; a subtitle deleted after the
// library read is submitted and Bazarr's refusal decides it (G19, G14).
func TestSyncRefused(t *testing.T) {
	srv := fakebazarr.New(t, fakebazarr.Config{Library: syncLibrary()})
	onFirstVideoRead(srv, func() {
		lib := syncLibrary()
		lib.Movies[0].Subtitles = slices.Delete(lib.Movies[0].Subtitles, 1, 2) // sintelEnHI
		srv.SetLibrary(lib)
	})
	s := startSync(t, srv, sintelThree...)
	ran := map[string]bool{}
	o := s.finish(func() { runSubmitted(t, srv, ran, "11/22/26 08:15:02") })
	line := lineOf(t, o.stdout, sintelEnHI)
	if !strings.HasPrefix(line, "failed  ") || !strings.HasSuffix(line, "(Subtitles file not found. Path mapping issue?)") {
		t.Errorf("a refused sync: %q", line)
	}
	if got := syncsSent(srv); !slices.Equal(got, []string{sintelEnHI, sintelEn, sintelEs}) || o.code == 0 {
		t.Errorf("syncs sent %q, exit %d", got, o.code)
	}
	for _, p := range []string{sintelEn, sintelEs} {
		if w := outcomeOf(t, o.stdout, p); w != "synced" {
			t.Errorf("%s %s after a refusal", p, w)
		}
	}
	noStateFile(t, s.state) // a refused sync is never recorded (G21)
}

// TestSyncUnanswered: a submission Bazarr never answers keeps its slot
// while Bazarr shows its sync; it is `synced` when the sync writes its
// event and `unconfirmed`, never `failed`, when it writes none (G17, G20).
func TestSyncUnanswered(t *testing.T) {
	for _, wrote := range []bool{true, false} {
		srv := fakebazarr.New(t, fakebazarr.Config{Library: syncLibrary()})
		held := make(chan struct{})
		srv.Respond(func(r fakebazarr.Request) fakebazarr.Behavior {
			if r.Method == http.MethodPatch && len(syncsSent(srv)) == 1 {
				close(held)
				return fakebazarr.Applied(fakebazarr.Hold)
			}
			return fakebazarr.Serve
		})
		s := startSync(t, srv, "--movie-id", "7", "--language", "es", "--language", "en", "--no-hi")
		s.untilHeld(held)
		srv.WaitHeld()
		s.clock.Advance(s.clock.WaitTimer()) // the request's limit passes
		id, _ := srv.SyncJob(sintelEn)
		srv.SetJob(id, fakebazarr.JobRunning)
		for range 4 {
			s.step()
			s.idle()
			if n := len(syncsSent(srv)); n != 1 {
				t.Fatalf("wrote %v: %d syncs sent while Bazarr shows the unanswered one", wrote, n)
			}
		}
		if wrote {
			srv.SyncRan(id, "11/22/26 08:15:02")
		} else {
			srv.SyncRan(id, "")
		}
		srv.SetJob(id, fakebazarr.JobCompleted)
		ran := map[string]bool{sintelEn: true}
		o := s.finish(func() { runSubmitted(t, srv, ran, "11/22/26 08:16:02") })
		want := "synced"
		if !wrote {
			want = "unconfirmed"
		}
		if got := outcomeOf(t, o.stdout, sintelEn); got != want {
			t.Errorf("wrote %v: %s, want %s\n%s", wrote, got, want, o.stdout)
		}
		if outcomeOf(t, o.stdout, sintelEs) != "synced" || (o.code == 0) == !wrote {
			t.Errorf("wrote %v: exit %d\n%s", wrote, o.code, o.stdout)
		}
	}
}

// TestSyncRestart: a restart of Bazarr empties its queue; the sync it held
// is `unconfirmed`, never `failed`, the run goes on and exits nonzero; a
// sync whose new event is there after the restart is still `synced` (G20).
func TestSyncRestart(t *testing.T) {
	for _, wrote := range []bool{true, false} {
		srv := fakebazarr.New(t, fakebazarr.Config{Library: syncLibrary()})
		s := startSync(t, srv, "--movie-id", "7", "--language", "es", "--language", "en", "--no-hi")
		s.until("submitted", func() bool { return len(syncsSent(srv)) == 1 }, nil)
		if wrote {
			id, _ := srv.SyncJob(sintelEn)
			srv.SetJob(id, fakebazarr.JobRunning)
			srv.SyncRan(id, "11/22/26 08:15:02")
		}
		srv.Restart("1763800000.5")
		ran := map[string]bool{sintelEn: true}
		o := s.finish(func() { runSubmitted(t, srv, ran, "11/22/26 08:16:02") })
		want, code := "unconfirmed", 1
		if wrote {
			want, code = "synced", 0
		}
		if got := outcomeOf(t, o.stdout, sintelEn); got != want || o.code != code {
			t.Errorf("wrote %v: %s, exit %d, want %s\n%s", wrote, got, o.code, want, o.stdout)
		}
		if outcomeOf(t, o.stdout, sintelEs) != "synced" {
			t.Errorf("wrote %v: the next subtitle after a restart:\n%s", wrote, o.stdout)
		}
		noStateFile(t, s.state) // an unconfirmed sync is never recorded (G20)
	}
}

// TestSyncOutage: while Bazarr answers no read, nothing is submitted, and
// the run goes on following the sync Bazarr still shows once it answers
// within five minutes; past them, the run names Bazarr unreachable and
// ends with the totals, giving the followed sync no line (G20, G3, G4).
func TestSyncOutage(t *testing.T) {
	for _, minutes := range []time.Duration{1, 6} {
		srv := fakebazarr.New(t, fakebazarr.Config{Library: syncLibrary()})
		var down atomic.Bool
		srv.Respond(func(r fakebazarr.Request) fakebazarr.Behavior {
			if down.Load() && r.Method == http.MethodGet {
				return fakebazarr.Status(http.StatusServiceUnavailable)
			}
			return fakebazarr.Serve
		})
		s := startSync(t, srv, "--movie-id", "7", "--language", "es", "--language", "en", "--no-hi")
		s.until("submitted", func() bool { return len(syncsSent(srv)) == 1 }, nil)
		id, _ := srv.SyncJob(sintelEn)
		srv.SetJob(id, fakebazarr.JobRunning)
		down.Store(true)
		from := s.clock.Now()
		if minutes == 6 {
			o := s.finish(nil)
			if o.code == 0 || !strings.Contains(o.stderr, "unreachable") || !strings.Contains(o.stdout, "Total: 0 of 2") ||
				len(resultLines(o.stdout)) != 0 || len(syncsSent(srv)) != 1 {
				t.Errorf("past five minutes: exit %d\n%s%s", o.code, o.stdout, o.stderr)
			}
			if s.clock.Now().Sub(from) < 5*time.Minute {
				t.Errorf("gave up after %v", s.clock.Now().Sub(from))
			}
			continue
		}
		s.until("a minute passed", func() bool { return s.clock.Now().Sub(from) >= time.Minute }, func() {
			if len(syncsSent(srv)) != 1 {
				t.Fatalf("a sync sent while Bazarr answers nothing")
			}
		})
		// Bazarr answers again, still running the sync: the run goes on
		// waiting for it.
		down.Store(false)
		for range 4 {
			s.step()
			s.idle()
			if lines, n := resultLines(s.stdout.String()), len(syncsSent(srv)); len(lines) != 0 || n != 1 {
				t.Fatalf("after the outage, with the sync still running: lines %q, %d syncs sent", lines, n)
			}
		}
		srv.SyncRan(id, "11/22/26 08:15:02")
		srv.SetJob(id, fakebazarr.JobCompleted)
		ran := map[string]bool{sintelEn: true}
		o := s.finish(func() { runSubmitted(t, srv, ran, "11/22/26 08:16:02") })
		if outcomeOf(t, o.stdout, sintelEn) != "synced" || outcomeOf(t, o.stdout, sintelEs) != "synced" || o.code != 0 {
			t.Errorf("after a minute's outage: exit %d\n%s%s", o.code, o.stdout, o.stderr)
		}
	}
}

// TestSyncStop: a stop ends the run with the totals for the subtitles
// already reported; a sync submitted, or being submitted, gets no line and
// no count, and nothing is sent after the stop (G4).
func TestSyncStop(t *testing.T) {
	for _, during := range []string{"a pending sync", "the submission"} {
		srv := fakebazarr.New(t, fakebazarr.Config{Library: syncLibrary()})
		held := make(chan struct{})
		srv.Respond(func(r fakebazarr.Request) fakebazarr.Behavior {
			if r.Method == http.MethodPatch && len(syncsSent(srv)) == 2 && during == "the submission" {
				close(held)
				return fakebazarr.Applied(fakebazarr.Hold)
			}
			return fakebazarr.Serve
		})
		s := startSync(t, srv, sintelThree...)
		s.until("submitted", func() bool { return len(syncsSent(srv)) == 1 }, nil)
		runJob(t, srv, sintelEnHI, "11/22/26 08:15:02")
		if during == "the submission" {
			s.untilHeld(held)
			srv.WaitHeld()
		} else {
			s.until("the second is submitted", func() bool { return len(syncsSent(srv)) == 2 }, nil)
		}
		sent := len(srv.Requests())
		s.stop()
		var o outcome
		select {
		case <-s.ended:
			o = s.o
		case <-time.After(10 * time.Second): // a guard against a hang only
			t.Fatalf("%s: the run did not end on the stop", during)
		}
		if o.code == 0 || len(resultLines(o.stdout)) != 1 || outcomeOf(t, o.stdout, sintelEnHI) != "synced" ||
			strings.Contains(o.stdout, sintelEn+"\n") || !strings.Contains(o.stdout, "Total: 1 of 3 selected subtitles\nsynced 1\n") {
			t.Errorf("%s: exit %d\n%s", during, o.code, o.stdout)
		}
		if n := len(srv.Requests()); n != sent {
			t.Errorf("%s: %d requests after the stop", during, n-sent)
		}
	}
}

// TestSyncOptionsSent: each sync request carries exactly the sync options
// given, with lang:CODE resolved to the reference subtitle's path (G16, F8).
func TestSyncOptionsSent(t *testing.T) {
	for _, c := range []struct {
		args []string
		want map[string]string
	}{
		{[]string{"--language", "es"}, map[string]string{}},
		{[]string{"--language", "es", "--max-offset-seconds", "120", "--no-fix-framerate", "--gss", "--reference", "a:1"},
			map[string]string{"max_offset_seconds": "120", "no_fix_framerate": "True", "gss": "True", "reference": "a:1"}},
		{[]string{"--language", "es", "--reference", "lang:en"}, map[string]string{"reference": sintelEn}},
	} {
		srv := fakebazarr.New(t, fakebazarr.Config{Library: syncLibrary()})
		s := startSync(t, srv, append([]string{"--movie-id", "7"}, c.args...)...)
		ran := map[string]bool{}
		s.finish(func() { runSubmitted(t, srv, ran, "11/22/26 08:15:02") })
		for _, r := range srv.Requests() {
			if r.Method != http.MethodPatch {
				continue
			}
			got := map[string]string{}
			for k, v := range r.Params() {
				if !slices.Contains([]string{"action", "language", "path", "type", "id", "forced", "hi"}, k) {
					got[k] = strings.Join(v, ",")
				}
			}
			if len(got) != len(c.want) {
				t.Errorf("%q: options sent %v, want %v", c.args, got, c.want)
			}
			for k, v := range c.want {
				if got[k] != v {
					t.Errorf("%q: %s=%q, want %q", c.args, k, got[k], v)
				}
			}
		}
		if n := len(syncsSent(srv)); n != 1 {
			t.Errorf("%q: %d syncs sent", c.args, n)
		}
	}
}

// TestSyncConfirmation: without --yes a run asks at a terminal, naming how
// many subtitles are selected, and without a terminal says --yes is
// required and sends nothing (G12).
func TestSyncConfirmation(t *testing.T) {
	srv := fakebazarr.New(t, fakebazarr.Config{Library: syncLibrary()})
	o := runAgainst(t, srv, "sync", "--movie-id", "7")
	if o.code == 0 || !strings.Contains(o.stderr, "--yes") || len(syncsSent(srv)) != 0 {
		t.Errorf("no terminal: exit %d, stderr %q, syncs %q", o.code, o.stderr, syncsSent(srv))
	}
	r := startWith(context.Background(), fakeclock.New(t), envFor(srv), typed("n\n"), "sync", "--movie-id", "7")
	o = await(t, r.done)
	if o.code == 0 || !strings.Contains(o.stderr, "4 subtitles selected. Sync them? [y/N] ") || len(syncsSent(srv)) != 0 {
		t.Errorf("answered no: exit %d, stderr %q, syncs %q", o.code, o.stderr, syncsSent(srv))
	}
}

// TestSyncTotals: a run prints no line for a subtitle it skips, counts it
// as done in the progress and under its reason in the totals, and exits
// zero when every other subtitle is synced (P3, G14, G6).
func TestSyncTotals(t *testing.T) {
	srv := fakebazarr.New(t, fakebazarr.Config{Library: syncLibrary()})
	srv.AddMovieEvent(7, fakebazarr.Event{Action: 5, Time: "11/21/26 10:00:00", Path: sintelEs})
	s := startSync(t, srv, "--movie-id", "7")
	ran := map[string]bool{}
	o := s.finish(func() { runSubmitted(t, srv, ran, "11/22/26 08:15:02") })
	if o.code != 0 || len(resultLines(o.stdout)) != 2 {
		t.Errorf("exit %d\n%s", o.code, o.stdout)
	}
	if !strings.Contains(o.stdout, "Total: 4 of 4 selected subtitles\n") || !strings.Contains(o.stdout, "synced 2") ||
		!strings.Contains(o.stdout, "skipped (forced) 1") || !strings.Contains(o.stdout, "skipped (currently synced) 1") {
		t.Errorf("totals:\n%s", o.stdout)
	}
	if !strings.HasSuffix(o.stderr, "4 of 4 subtitles done\n") {
		t.Errorf("progress %q", o.stderr)
	}
}

// noStateFile fails the test when the run left a state file or its
// temporary file at state.
func noStateFile(t *testing.T, state string) {
	t.Helper()
	for _, p := range []string{state, state + ".tmp"} {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("the run wrote %s", p)
		}
	}
}
