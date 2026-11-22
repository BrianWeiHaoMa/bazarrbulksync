package commands_test

import (
	"context"
	"net/http"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

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

// heavyQueueRead makes each read of srv's queue weigh about the bytes it
// returns: Bazarr's queue lists ended jobs too, here with long names.
func heavyQueueRead(srv *fakebazarr.Server) int {
	const jobs, nameLen = 200, 1000
	for i := range jobs {
		srv.AddJob(strconv.Itoa(i)+strings.Repeat("x", nameLen), fakebazarr.JobCompleted)
	}
	return jobs * nameLen
}

// queueReads counts the reads of its queue the fake received.
func queueReads(srv *fakebazarr.Server) int {
	n := 0
	for _, r := range srv.Requests() {
		if strings.HasSuffix(r.Path, "system/jobs") {
			n++
		}
	}
	return n
}

// liveHeap is the heap still in use after a collection.
func liveHeap() int {
	runtime.GC()
	runtime.GC()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return int(m.HeapAlloc)
}

// heapAcrossQueueReads lets the run's sleeps pass until it has read the
// queue a hundred more times, and fails the test when the heap grew by
// more than ten of those reads, which State does not hold.
func (s *syncing) heapAcrossQueueReads(readSize int) {
	s.t.Helper()
	s.idle()
	before, from := liveHeap(), queueReads(s.srv)
	for range 10000 {
		if queueReads(s.srv)-from >= 100 {
			break
		}
		s.step()
		s.idle()
	}
	grew, reads := liveHeap()-before, queueReads(s.srv)-from
	if reads < 100 {
		s.t.Fatalf("only %d queue reads", reads)
	}
	if grew > 10*readSize {
		s.t.Errorf("the heap grew by %d bytes over %d queue reads of about %d bytes each", grew, reads, readSize)
	}
	s.stop()
	<-s.ended
}

// TestSyncKeepsFewQueueReads: while the run follows a sync through a
// hundred queue reads, its memory does not grow by what those reads showed
// (State: the selection and each followed sync's baseline).
func TestSyncKeepsFewQueueReads(t *testing.T) {
	srv := fakebazarr.New(t, fakebazarr.Config{Library: syncLibrary()})
	readSize := heavyQueueRead(srv)
	// The sync's job stays pending: the run follows it, reading the queue.
	s := startSync(t, srv, "--movie-id", "7", "--no-forced", "--concurrency", "1")
	s.until("a sync is followed", func() bool { return len(syncsSent(srv)) == 1 }, nil)
	s.heapAcrossQueueReads(readSize)
}

// TestSyncJudgingAgainKeepsFewQueueReads: a judging that starts again pass
// after pass, each read of the history answered only after retries past
// the read limit, keeps no more of its queue reads than a run does.
func TestSyncJudgingAgainKeepsFewQueueReads(t *testing.T) {
	srv := fakebazarr.New(t, fakebazarr.Config{Library: syncLibrary()})
	readSize := heavyQueueRead(srv)
	s := startSync(t, srv, "--movie-id", "7", "--no-forced")
	// The first judging cannot submit: no queue read is five seconds old.
	s.idle()
	var answered time.Time
	srv.Respond(func(r fakebazarr.Request) fakebazarr.Behavior {
		if !strings.Contains(r.Path, "history") {
			return fakebazarr.Serve
		}
		if now := s.clock.Now(); answered.IsZero() || now.Sub(answered) <= 61*time.Second {
			if answered.IsZero() {
				answered = now
			}
			return fakebazarr.Status(http.StatusServiceUnavailable)
		}
		answered = s.clock.Now()
		return fakebazarr.Serve
	})
	s.heapAcrossQueueReads(readSize)
	if got := syncsSent(srv); len(got) != 0 {
		t.Errorf("syncs sent %q while every judging started again", got)
	}
}
