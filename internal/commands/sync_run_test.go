package commands_test

import (
	"context"
	"net/http"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/fakebazarr"
	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/fakeclock"
)

// syncing is a real `sync` run in the background against the fake Bazarr,
// on a clock only the test advances: while the run sleeps, nothing in it
// moves, so the test can look at what it did and change Bazarr.
type syncing struct {
	t      *testing.T
	srv    *fakebazarr.Server
	clock  *fakeclock.Clock
	state  string
	stdout *syncBuffer
	stop   context.CancelFunc
	ended  chan struct{}
	o      outcome
}

// startSync starts `sync --yes` with args against srv, with a state file
// in a new directory.
func startSync(t *testing.T, srv *fakebazarr.Server, args ...string) *syncing {
	t.Helper()
	return startSyncState(t, srv, filepath.Join(t.TempDir(), "state.json"), args...)
}

func startSyncState(t *testing.T, srv *fakebazarr.Server, state string, args ...string) *syncing {
	t.Helper()
	ctx, stop := context.WithCancel(context.Background())
	t.Cleanup(stop)
	s := &syncing{t: t, srv: srv, clock: fakeclock.New(t), state: state, stop: stop, ended: make(chan struct{})}
	r := startWith(ctx, s.clock, envFor(srv), notTerminal, append([]string{"sync", "--yes", "--state-file", state}, args...)...)
	s.stdout = r.stdout
	go func() {
		s.o = <-r.done
		close(s.ended)
	}()
	return s
}

// idle waits until the run sleeps, and fails the test if it ends instead.
func (s *syncing) idle() {
	s.t.Helper()
	if _, ok := s.clock.WaitSleepUnless(s.ended); !ok {
		s.t.Fatalf("the run ended early: exit %d\n%s\n%s", s.o.code, s.o.stdout, s.o.stderr)
	}
}

// step lets the run's current sleep pass.
func (s *syncing) step() {
	s.t.Helper()
	d, ok := s.clock.WaitSleepUnless(s.ended)
	if !ok {
		s.t.Fatalf("the run ended early: exit %d\n%s\n%s", s.o.code, s.o.stdout, s.o.stderr)
	}
	s.clock.Advance(d)
}

// until lets the run's sleeps pass until cond holds while it sleeps; after
// each sleep's start, check runs first, so that it sees every still state.
func (s *syncing) until(what string, cond func() bool, check func()) {
	s.t.Helper()
	for range 200 {
		s.idle()
		if check != nil {
			check()
		}
		if cond() {
			return
		}
		s.step()
	}
	s.t.Fatalf("never: %s", what)
}

// untilHeld lets the run's sleeps pass until held is closed, as the fake
// starts holding a request.
func (s *syncing) untilHeld(held <-chan struct{}) {
	s.t.Helper()
	for {
		d, ok := s.clock.WaitSleepUnless(held)
		if !ok {
			return
		}
		s.clock.Advance(d)
	}
}

// finish lets the run's sleeps pass until it ends, and returns its outcome;
// check runs at each still state.
func (s *syncing) finish(check func()) outcome {
	s.t.Helper()
	for range 400 {
		d, ok := s.clock.WaitSleepUnless(s.ended)
		if !ok {
			return s.o
		}
		if check != nil {
			check()
		}
		s.clock.Advance(d)
	}
	s.t.Fatal("the run never ended")
	return outcome{}
}

// syncsSent returns the paths of the sync requests the fake received, in order.
func syncsSent(srv *fakebazarr.Server) []string {
	var got []string
	for _, r := range srv.Requests() {
		if r.Method == http.MethodPatch && r.Params().Get("action") == "sync" {
			got = append(got, r.Params().Get("path"))
		}
	}
	return got
}

// runJob runs the job of the latest sync of path to its end in the fake,
// writing a sync event at eventTime unless it is empty.
func runJob(t *testing.T, srv *fakebazarr.Server, path, eventTime string) {
	t.Helper()
	id, ok := srv.SyncJob(path)
	if !ok {
		t.Fatalf("no sync job of %s", path)
	}
	srv.SetJob(id, fakebazarr.JobRunning)
	srv.SyncRan(id, eventTime)
	srv.SetJob(id, fakebazarr.JobCompleted)
}

// runSubmitted runs to its end, with a sync event at eventTime, the job
// of each sync queued whose path ran does not hold yet, and adds it there.
func runSubmitted(t *testing.T, srv *fakebazarr.Server, ran map[string]bool, eventTime string) {
	t.Helper()
	for _, p := range syncsSent(srv) {
		if _, queued := srv.SyncJob(p); queued && !ran[p] {
			ran[p] = true
			runJob(t, srv, p, eventTime)
		}
	}
}

// outcomeOf returns the word path's result line opens with.
func outcomeOf(t *testing.T, stdout, path string) string {
	t.Helper()
	line := lineOf(t, stdout, path)
	word, _, _ := strings.Cut(line, "  ")
	return word
}

// sintelThree selects Sintel's three subtitles that are not forced, in
// P2's order: sintelEnHI, sintelEn, sintelEs.
var sintelThree = []string{"--movie-id", "7", "--no-forced"}

// TestSyncSlots: a slot is taken before the request is sent and freed only
// when the subtitle's outcome is printed (G17), with at most one request in
// flight whatever --concurrency is (Approach).
func TestSyncSlots(t *testing.T) {
	order := []string{sintelEnHI, sintelEn, sintelEs}
	for _, concurrency := range []int{1, 2} {
		srv := fakebazarr.New(t, fakebazarr.Config{Library: syncLibrary()})
		// At each sync request, the syncs sent before it whose lines are not
		// printed hold the slots.
		var s *syncing
		var overfull []string
		srv.Respond(func(r fakebazarr.Request) fakebazarr.Behavior {
			if r.Method == http.MethodPatch {
				if held := len(syncsSent(srv)) - 1 - len(resultLines(s.stdout.String())); held >= concurrency {
					overfull = append(overfull, r.Params().Get("path"))
				}
			}
			return fakebazarr.Serve
		})
		s = startSync(t, srv, append(sintelThree, "--concurrency", strconv.Itoa(concurrency))...)
		// Every job stays pending: no further request may be sent, however
		// long the run waits.
		s.until("the slots are full", func() bool { return len(syncsSent(srv)) == concurrency }, nil)
		for range 5 {
			s.step()
			s.idle()
			if got := syncsSent(srv); !slices.Equal(got, order[:concurrency]) {
				t.Fatalf("--concurrency %d with every job pending: syncs sent %q", concurrency, got)
			}
		}
		if lines := resultLines(s.stdout.String()); len(lines) != 0 {
			t.Errorf("lines %q before any sync ended", lines)
		}
		// The first one ends: only then is the next submitted.
		runJob(t, srv, order[0], "11/22/26 08:15:02")
		s.until("the first is reported", func() bool { return len(resultLines(s.stdout.String())) == 1 }, nil)
		ran := map[string]bool{order[0]: true}
		o := s.finish(func() { runSubmitted(t, srv, ran, "11/22/26 08:16:02") })
		if o.code != 0 {
			t.Errorf("--concurrency %d: exit %d\n%s%s", concurrency, o.code, o.stdout, o.stderr)
		}
		for _, p := range order {
			if w := outcomeOf(t, o.stdout, p); w != "synced" {
				t.Errorf("--concurrency %d: %s %s", concurrency, p, w)
			}
		}
		if got := syncsSent(srv); !slices.Equal(got, order) {
			t.Errorf("--concurrency %d: syncs sent %q, want each once in order", concurrency, got)
		}
		if len(overfull) > 0 {
			t.Errorf("--concurrency %d: %q sent while every slot was held", concurrency, overfull)
		}
		if n := srv.MostAtOnce(); n != 1 {
			t.Errorf("--concurrency %d: the fake answered %d requests at once", concurrency, n)
		}
	}
}
