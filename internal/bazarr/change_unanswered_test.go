package bazarr_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/bazarr"
	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/fakebazarr"
	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/fakeclock"
)

// The changes Bazarr never answered, and the changes a stop cut short.

// heldUntilLimit advances clock to the change's limit once the fake holds it.
func heldUntilLimit(t *testing.T, srv *fakebazarr.Server, clock *fakeclock.Clock) {
	srv.WaitHeld()
	clock.Advance(clock.WaitTimer())
}

func TestChangeUnanswered(t *testing.T) {
	for _, tc := range []struct {
		name     string
		behavior fakebazarr.Behavior
	}{
		{"502", fakebazarr.Status(502)}, {"503", fakebazarr.Status(503)}, {"504", fakebazarr.Status(504)},
		{"connection closed", fakebazarr.Drop}, {"held past the limit", fakebazarr.Hold},
	} {
		clock := fakeclock.New(t)
		srv, client := changeServer(t, clock, bazarr.Limits{})
		srv.SetStartTime("1763800000.5")
		srv.Respond(onChanges(tc.behavior))
		done := inBackground(func() (bazarr.ChangeResult, error) {
			return client.Fix(context.Background(), episodeSub, bazarr.FixOCR)
		})
		if tc.behavior == fakebazarr.Hold {
			heldUntilLimit(t, srv, clock)
		}
		out := await(t, done)
		if out.err != nil || out.res.Outcome != bazarr.Unanswered || out.res.StartTime != "1763800000.5" {
			t.Errorf("%s: %+v, %v; want unanswered with the start time read after it", tc.name, out.res, out.err)
		}
		requests := srv.Requests()
		if len(requests) != 3 || requests[1].Method != http.MethodPatch || requests[2].Path != "/api/system/status" {
			t.Errorf("%s: requests %v; want the check, the change once, then a status read", tc.name, describe(requests))
		}
	}
}

func describe(requests []fakebazarr.Request) []string {
	var out []string
	for _, r := range requests {
		out = append(out, r.Method+" "+r.Path)
	}
	return out
}

// G24, G27: whatever happens to a change, Bazarr receives it once, also
// while the start-time read after it is retried.
func TestChangeSentOnce(t *testing.T) {
	for name, b := range map[string]fakebazarr.Behavior{
		"500": fakebazarr.Status(500), "502": fakebazarr.Status(502), "503": fakebazarr.Status(503),
		"504": fakebazarr.Status(504), "dropped": fakebazarr.Drop, "held": fakebazarr.Hold,
	} {
		for _, change := range []string{"fix", "delete"} {
			clock := fakeclock.New(t)
			srv, client := changeServer(t, clock, bazarr.Limits{})
			var reads atomic.Int32
			srv.Respond(func(r fakebazarr.Request) fakebazarr.Behavior {
				if r.Method != http.MethodGet {
					return b
				}
				if reads.Add(1) <= 3 { // the start-time read fails three times
					return fakebazarr.Status(503)
				}
				return fakebazarr.Serve
			})
			done := inBackground(func() (bazarr.ChangeResult, error) {
				if change == "fix" {
					return client.Fix(context.Background(), movieSub, bazarr.FixRemoveHI)
				}
				return client.Delete(context.Background(), movieSub)
			})
			if b == fakebazarr.Hold {
				heldUntilLimit(t, srv, clock)
			}
			if b != fakebazarr.Status(500) {
				for range 3 {
					clock.Advance(clock.WaitSleep())
				}
			}
			out := await(t, done)
			if n := len(changesReceived(srv)); n != 1 {
				t.Errorf("%s %s: Bazarr received the change %d times, want once (%+v, %v)", name, change, n, out.res, out.err)
			}
		}
	}
}

func TestUnansweredReadsStartTime(t *testing.T) {
	t.Run("gives up five minutes after the change was sent", func(t *testing.T) {
		for _, b := range []fakebazarr.Behavior{fakebazarr.Status(502), fakebazarr.Hold} {
			clock := fakeclock.New(t)
			srv, client := changeServer(t, clock, bazarr.Limits{Change: 50 * time.Second})
			clock.Advance(time.Hour) // the window is not measured from the run's start
			sent := clock.Now()
			srv.Respond(func(r fakebazarr.Request) fakebazarr.Behavior {
				if r.Method != http.MethodGet {
					return b
				}
				return fakebazarr.Status(500)
			})
			done := inBackground(func() (bazarr.ChangeResult, error) { return client.Delete(context.Background(), episodeSub) })
			if b == fakebazarr.Hold {
				heldUntilLimit(t, srv, clock)
			}
			end := sent.Add(5 * time.Minute) // G3's words, not the constant under test
			for clock.Now().Before(end) {
				clock.Advance(clock.WaitSleep())
			}
			out := await(t, done)
			var ue *bazarr.UnreachableError
			if out.res.Outcome != bazarr.Unanswered || !errors.As(out.err, &ue) {
				t.Errorf("%+v, %v; want unanswered and Bazarr unreachable", out.res, out.err)
			}
			if got := clock.Now().Sub(sent); got != 5*time.Minute {
				t.Errorf("gave up %v after the change was sent, want 5m", got)
			}
			reads := len(srv.Requests()) - 2
			if n := len(changesReceived(srv)); n != 1 || reads < 2 {
				t.Errorf("%d changes and %d status reads; want one change and the read retried", n, reads)
			}
		}
	})
	t.Run("answers again within the window", func(t *testing.T) {
		clock := fakeclock.New(t)
		srv, client := changeServer(t, clock, bazarr.Limits{})
		var reads atomic.Int32
		srv.Respond(func(r fakebazarr.Request) fakebazarr.Behavior {
			if r.Method != http.MethodGet {
				return fakebazarr.Status(504)
			}
			if reads.Add(1) <= 4 {
				return fakebazarr.Status(500)
			}
			return fakebazarr.Serve
		})
		done := inBackground(func() (bazarr.ChangeResult, error) {
			return client.Fix(context.Background(), movieSub, bazarr.FixEmoji)
		})
		for range 4 {
			clock.Advance(clock.WaitSleep())
		}
		out := await(t, done)
		if out.err != nil || out.res.Outcome != bazarr.Unanswered || out.res.StartTime != fakebazarr.DefaultStartTime {
			t.Errorf("%+v, %v; want unanswered, the run going on, with the start time", out.res, out.err)
		}
		// The outage is over: a later read gets a window of its own.
		srv.Respond(nil)
		if _, retried, err := client.Library(context.Background()); err != nil || retried {
			t.Errorf("read after: retried %v, err %v", retried, err)
		}
	})
}

func TestChangeStop(t *testing.T) {
	t.Run("before the request is sent", func(t *testing.T) {
		srv, client := changeServer(t, nil, bazarr.Limits{})
		ctx, stop := context.WithCancel(context.Background())
		stop()
		res, err := client.Delete(ctx, movieSub)
		if !errors.Is(err, bazarr.ErrStopped) || res.Outcome != bazarr.NotSent {
			t.Errorf("%+v, %v; want not sent and stopped", res, err)
		}
		if n := len(srv.Requests()); n != 1 {
			t.Errorf("%d requests, want only the check", n)
		}
	})
	t.Run("while the change is held", func(t *testing.T) {
		clock := fakeclock.New(t)
		srv, client := changeServer(t, clock, bazarr.Limits{})
		srv.Respond(onChanges(fakebazarr.Hold))
		ctx, stop := context.WithCancel(context.Background())
		done := inBackground(func() (bazarr.ChangeResult, error) { return client.Fix(ctx, movieSub, bazarr.FixRemoveTags) })
		srv.WaitHeld()
		stop()
		out := await(t, done) // no limit passes on the clock
		if !errors.Is(out.err, bazarr.ErrStopped) || out.res.Outcome != bazarr.Stopped {
			t.Errorf("%+v, %v; want stopped", out.res, out.err)
		}
		clock.Advance(time.Hour)
		if n := len(srv.Requests()); n != 2 {
			t.Errorf("%d requests, want the check and the change only", n)
		}
	})
	t.Run("during the start-time read", func(t *testing.T) {
		for _, read := range []fakebazarr.Behavior{fakebazarr.Status(500), fakebazarr.Hold} {
			clock := fakeclock.New(t)
			srv, client := changeServer(t, clock, bazarr.Limits{})
			srv.Respond(func(r fakebazarr.Request) fakebazarr.Behavior {
				if r.Method != http.MethodGet {
					return fakebazarr.Status(503)
				}
				return read
			})
			ctx, stop := context.WithCancel(context.Background())
			done := inBackground(func() (bazarr.ChangeResult, error) { return client.Delete(ctx, episodeSub) })
			if read == fakebazarr.Hold {
				srv.WaitHeld()
			} else {
				clock.WaitSleep()
			}
			stop()
			out := await(t, done)
			if !errors.Is(out.err, bazarr.ErrStopped) || out.res.Outcome != bazarr.Unanswered {
				t.Errorf("%+v, %v; want the change unanswered and the run stopped", out.res, out.err)
			}
			n := len(srv.Requests())
			clock.Advance(time.Hour)
			if m := len(srv.Requests()); m != n || n != 3 {
				t.Errorf("%d requests, then %d; want the check, the change, one read, nothing after the stop", n, m)
			}
		}
	})
	t.Run("after the answer", func(t *testing.T) {
		srv, client := changeServer(t, nil, bazarr.Limits{})
		ctx, stop := context.WithCancel(context.Background())
		res, err := client.Delete(ctx, movieSub)
		stop()
		if err != nil || res.Outcome != bazarr.Accepted {
			t.Errorf("%+v, %v; want accepted, standing", res, err)
		}
		if n := len(srv.Requests()); n != 2 {
			t.Errorf("%d requests, want the check and the change", n)
		}
	})
}

// A refusal whose body is cut short was never answered completely.
func TestTruncatedRefusalUnanswered(t *testing.T) {
	for name, body := range map[string]string{"short": `"Movie not found"`, "long": strings.Repeat("x", 70000)} {
		clock := fakeclock.New(t)
		srv, client := changeServer(t, clock, bazarr.Limits{})
		srv.Respond(onChanges(fakebazarr.Truncate(409, body)))
		res, err := client.Delete(context.Background(), movieSub)
		if err != nil || res.Outcome != bazarr.Unanswered {
			t.Errorf("%s: %+v, %v; want unanswered", name, res, err)
		}
		if r := srv.Requests(); len(r) != 3 || r[2].Method != http.MethodGet {
			t.Errorf("%s: requests %v; want the check, the change, a status read", name, describe(r))
		}
	}
}
