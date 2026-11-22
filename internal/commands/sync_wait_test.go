package commands_test

import (
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/fakebazarr"
)

// TestSyncWaitsForBazarrsSync: a subtitle Bazarr's queue shows a sync of is
// not submitted while the queue shows it, holds no slot meanwhile, and is
// judged again once the sync is gone: skipped as currently synced when that
// sync wrote its event, submitted when it wrote none (G18, G15).
func TestSyncWaitsForBazarrsSync(t *testing.T) {
	for _, wrote := range []bool{true, false} {
		srv := fakebazarr.New(t, fakebazarr.Config{Library: syncLibrary()})
		own := srv.AddJob("Syncing "+sintelEnHI, fakebazarr.JobPending)
		s := startSync(t, srv, "--movie-id", "7", "--language", "en", "--no-forced")
		s.until("the second is submitted", func() bool { return slices.Contains(syncsSent(srv), sintelEn) }, nil)
		runJob(t, srv, sintelEn, "11/22/26 08:15:02")
		s.until("the second is reported", func() bool { return len(resultLines(s.stdout.String())) == 1 }, nil)
		for range 3 {
			s.step()
		}
		if slices.Contains(syncsSent(srv), sintelEnHI) {
			t.Fatalf("wrote %v: %s submitted while Bazarr's own sync of it is queued", wrote, sintelEnHI)
		}
		srv.SetJob(own, fakebazarr.JobRunning)
		if wrote {
			srv.AddMovieEvent(7, fakebazarr.Event{Action: 5, Time: "11/22/26 08:17:00", Path: sintelEnHI})
		}
		srv.SetJob(own, fakebazarr.JobCompleted)
		ran := map[string]bool{sintelEn: true}
		o := s.finish(func() { runSubmitted(t, srv, ran, "11/22/26 08:18:00") })
		if wrote {
			if slices.Contains(syncsSent(srv), sintelEnHI) || len(resultLines(o.stdout)) != 1 || o.code != 0 {
				t.Errorf("Bazarr's sync wrote its event: syncs %q, exit %d\n%s", syncsSent(srv), o.code, o.stdout)
			}
			if !strings.Contains(o.stdout, "skipped (currently synced) 1") {
				t.Errorf("totals:\n%s", o.stdout)
			}
		} else if w := outcomeOf(t, o.stdout, sintelEnHI); w != "synced" || o.code != 0 {
			t.Errorf("Bazarr's sync wrote no event: %s %s, exit %d", sintelEnHI, w, o.code)
		}
	}
}

// TestSyncOnePathTwoEpisodes: while the run follows its sync of a path, no
// other subtitle at that path is submitted, even when a queue read shows
// the sync in neither of Bazarr's queues; once it is synced, the other is
// judged again and skipped as currently synced (G18, S2).
func TestSyncOnePathTwoEpisodes(t *testing.T) {
	srv := fakebazarr.New(t, fakebazarr.Config{Library: syncLibrary()})
	// Once the first episode's sync is queued, Bazarr starts moving it to
	// its running queue as the run reads the other episode, so that the
	// other's judging finds it in neither queue.
	moved := false
	srv.Respond(func(r fakebazarr.Request) fakebazarr.Behavior {
		if id, ok := srv.SyncJob(towersDouble); ok && !moved && strings.Contains(r.RawQuery, "episodeid%5B%5D") {
			moved = true
			srv.SetJob(id, fakebazarr.JobMoving)
		}
		return fakebazarr.Serve
	})
	s := startSync(t, srv, "--series-id", "3", "--language", "en", "--concurrency", "2")
	s.until("the first episode is submitted", func() bool { return len(syncsSent(srv)) >= 1 }, nil)
	id, _ := srv.SyncJob(towersDouble)
	for i := range 4 {
		s.step()
		s.idle()
		srv.SetJob(id, fakebazarr.JobRunning)
		if n := len(syncsSent(srv)); n != 1 {
			t.Fatalf("after %d reads: %d syncs of %s sent while the run follows one", i+1, n, towersDouble)
		}
	}
	srv.SyncRan(id, "11/22/26 08:15:02")
	srv.SetJob(id, fakebazarr.JobCompleted)
	o := s.finish(nil)
	if n := len(syncsSent(srv)); n != 1 || o.code != 0 {
		t.Errorf("%d syncs sent, exit %d\n%s%s", n, o.code, o.stdout, o.stderr)
	}
	if len(resultLines(o.stdout)) != 1 || !strings.Contains(o.stdout, "synced 1, skipped (currently synced) 1") {
		t.Errorf("want one synced line and one currently synced skip:\n%s", o.stdout)
	}
}

// TestSyncFiveSeconds: a subtitle is submitted only when the queue reads
// from one answered at least five seconds before the judging's read up to
// it all show no sync of it, so a sync of Bazarr's own moving from its
// pending to its running queue, shown in neither for one read, is waited
// for (G15, G16, G18, S2); and a run's first submission comes after two
// queue reads at least five seconds apart.
func TestSyncFiveSeconds(t *testing.T) {
	srv := fakebazarr.New(t, fakebazarr.Config{Library: syncLibrary()})
	var s *syncing
	var queueReads []time.Time
	var firstSync []time.Time
	srv.Respond(func(r fakebazarr.Request) fakebazarr.Behavior {
		switch {
		case r.Path == "/api/system/jobs":
			queueReads = append(queueReads, s.clock.Now())
		case r.Method == http.MethodPatch && firstSync == nil:
			firstSync = slices.Clone(queueReads)
		}
		return fakebazarr.Serve
	})
	// Bazarr's own sync: absent from the first read, pending at the next,
	// then in neither queue for one read as Bazarr moves it, then running.
	s = startSync(t, srv, "--movie-id", "7", "--language", "es")
	s.idle()
	own := srv.AddJob("Syncing "+sintelEs, fakebazarr.JobPending)
	for _, next := range []fakebazarr.JobState{fakebazarr.JobMoving, fakebazarr.JobRunning, fakebazarr.JobRunning} {
		s.step()
		s.idle()
		if got := syncsSent(srv); len(got) != 0 {
			t.Fatalf("%q sent while Bazarr's own sync of it was pending or moving", got)
		}
		srv.SetJob(own, next)
	}
	s.step()
	s.idle()
	if got := syncsSent(srv); len(got) != 0 {
		t.Fatalf("%q sent while Bazarr's own sync of it runs", got)
	}
	srv.SetJob(own, fakebazarr.JobCompleted) // wrote no event
	s.until("the subtitle is submitted", func() bool { return len(syncsSent(srv)) == 1 }, nil)
	if !twoApart(firstSync) {
		t.Errorf("first sync sent after queue reads at %v; want two at least five seconds apart", firstSync)
	}
	runJob(t, srv, sintelEs, "11/22/26 08:15:02")
	if o := s.finish(nil); outcomeOf(t, o.stdout, sintelEs) != "synced" {
		t.Errorf("%s", o.stdout)
	}

	// A run whose first judging finds nothing in the queue still waits.
	srv = fakebazarr.New(t, fakebazarr.Config{Library: syncLibrary()})
	queueReads, firstSync = nil, nil
	srv.Respond(func(r fakebazarr.Request) fakebazarr.Behavior {
		switch {
		case r.Path == "/api/system/jobs":
			queueReads = append(queueReads, s.clock.Now())
		case r.Method == http.MethodPatch && firstSync == nil:
			firstSync = slices.Clone(queueReads)
		}
		return fakebazarr.Serve
	})
	s = startSync(t, srv, "--movie-id", "7", "--language", "es")
	s.until("the subtitle is submitted", func() bool { return len(syncsSent(srv)) == 1 }, nil)
	if !twoApart(firstSync) {
		t.Errorf("first sync sent after queue reads at %v; want two at least five seconds apart", firstSync)
	}
}

// twoApart reports whether two of the times are at least five seconds apart.
func twoApart(ts []time.Time) bool {
	return len(ts) >= 2 && ts[len(ts)-1].Sub(ts[0]) >= 5*time.Second
}

// TestSyncJudgedBeforeSubmission: each subtitle is judged from Bazarr as
// read just before its submission, not as the run began: one whose history
// gains a sync while another is followed is skipped (G15).
func TestSyncJudgedBeforeSubmission(t *testing.T) {
	srv := fakebazarr.New(t, fakebazarr.Config{Library: syncLibrary()})
	s := startSync(t, srv, sintelThree...)
	s.until("the first is submitted", func() bool { return len(syncsSent(srv)) == 1 }, nil)
	srv.AddMovieEvent(7, fakebazarr.Event{Action: 5, Time: "11/22/26 08:10:00", Path: sintelEs})
	ran := map[string]bool{}
	o := s.finish(func() { runSubmitted(t, srv, ran, "11/22/26 08:15:02") })
	if got := syncsSent(srv); !slices.Equal(got, []string{sintelEnHI, sintelEn}) || o.code != 0 {
		t.Errorf("syncs sent %q, exit %d", got, o.code)
	}
	if !strings.Contains(o.stdout, "skipped (currently synced) 1") {
		t.Errorf("totals:\n%s", o.stdout)
	}
}
