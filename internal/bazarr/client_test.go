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

// await returns what the operation running in the background returned. The
// real-time bound only turns a hang into a failure.
func await[T any](t *testing.T, done <-chan T) T {
	t.Helper()
	select {
	case v := <-done:
		return v
	case <-time.After(10 * time.Second):
		t.Fatal("the operation did not return within 10s")
		panic("unreachable")
	}
}

// connect checks the connection to srv on clock and returns the client.
func connect(t *testing.T, srv *fakebazarr.Server, clock bazarr.Clock, limits bazarr.Limits) *bazarr.Client {
	t.Helper()
	client, err := bazarr.Connect(context.Background(), bazarr.Connection{
		URL: srv.URL, APIKey: srv.APIKey(), Clock: clock, Limits: limits})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

// untilGivesUp advances clock through every wait between attempts until the
// window ending at end has passed, then returns what the read returned.
func untilGivesUp(t *testing.T, clock *fakeclock.Clock, done <-chan libraryResult, end time.Time) libraryResult {
	t.Helper()
	for clock.Now().Before(end) {
		clock.Advance(clock.WaitSleep())
	}
	return await(t, done)
}

type libraryResult struct {
	lib     bazarr.Library
	retried bazarr.Retried
	err     error
}

func readLibrary(ctx context.Context, client *bazarr.Client) <-chan libraryResult {
	done := make(chan libraryResult, 1)
	go func() {
		lib, retried, err := client.Library(ctx)
		done <- libraryResult{lib, retried, err}
	}()
	return done
}

func TestLimitsWithinRetryWindow(t *testing.T) {
	srv := fakebazarr.New(t, fakebazarr.Config{})
	srv.Respond(func(fakebazarr.Request) fakebazarr.Behavior { return fakebazarr.Hold })
	clock := fakeclock.New(t)
	go func() {
		_, _ = bazarr.Connect(context.Background(), bazarr.Connection{URL: srv.URL, APIKey: srv.APIKey(), Clock: clock,
			Limits: bazarr.Limits{Read: 6 * time.Minute}})
	}()
	srv.WaitHeld()
	if d := clock.WaitTimer(); d != 5*time.Minute {
		t.Errorf("a 6-minute limit carried as %v, want it held to G3's 5 minutes (T1)", d)
	}
	clock.Advance(time.Hour)
}

// The client tells the read limit in effect, which the judging of a sync
// compares the time its reads took against.
func TestReadLimitTold(t *testing.T) {
	srv := fakebazarr.New(t, fakebazarr.Config{})
	for _, tc := range []struct {
		limits bazarr.Limits
		want   time.Duration
	}{
		{bazarr.Limits{}, 60 * time.Second},
		{bazarr.Limits{Read: 7 * time.Second, Change: 9 * time.Second}, 7 * time.Second},
		{bazarr.Limits{Read: 6 * time.Minute}, 5 * time.Minute},
	} {
		if got := connect(t, srv, nil, tc.limits).ReadLimit(); got != tc.want {
			t.Errorf("limits %+v: read limit %v, want %v", tc.limits, got, tc.want)
		}
	}
}

func TestDefaultLimits(t *testing.T) {
	if bazarr.DefaultLimits.Read != 60*time.Second {
		t.Errorf("DefaultLimits.Read = %v, want 60s until measured (T1)", bazarr.DefaultLimits.Read)
	}
	srv := fakebazarr.New(t, fakebazarr.Config{})
	srv.Respond(func(fakebazarr.Request) fakebazarr.Behavior { return fakebazarr.Hold })
	clock := fakeclock.New(t)
	go func() {
		_, _ = bazarr.Connect(context.Background(), bazarr.Connection{URL: srv.URL, APIKey: srv.APIKey(), Clock: clock})
	}()
	srv.WaitHeld()
	if d := clock.WaitTimer(); d != 60*time.Second {
		t.Errorf("a request with no limit set carries %v, want 60s", d)
	}
	clock.Advance(time.Minute)
}

func TestBasePath(t *testing.T) {
	for _, trailing := range []string{"", "/"} {
		srv := fakebazarr.New(t, fakebazarr.Config{BasePath: "/bazarr", Library: fakebazarr.Library{
			Series: []fakebazarr.Series{{SonarrSeriesID: 3, Title: "S"}}}})
		client, err := bazarr.Connect(context.Background(), bazarr.Connection{URL: srv.URL + trailing, APIKey: srv.APIKey()})
		if err != nil {
			t.Fatalf("address %q: %v", srv.URL+trailing, err)
		}
		if _, _, err := client.Library(context.Background()); err != nil {
			t.Fatal(err)
		}
		requests := srv.Requests()
		if len(requests) != 4 {
			t.Errorf("address %q: %d requests, want status, movies, series, episodes", srv.URL+trailing, len(requests))
		}
		for _, r := range requests {
			if !strings.HasPrefix(r.Path, "/bazarr/api/") || strings.Contains(r.Path, "//") {
				t.Errorf("address %q: request to %q, want it under /bazarr/api/", srv.URL+trailing, r.Path)
			}
		}
	}
}

func TestAPIKey(t *testing.T) {
	srv := fakebazarr.New(t, fakebazarr.Config{BasePath: "/b", Library: fakebazarr.Library{
		Series: []fakebazarr.Series{{SonarrSeriesID: 1, Title: "S"}}}})
	key := srv.APIKey()
	client := connect(t, srv, nil, bazarr.Limits{})
	if _, _, err := client.Library(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, r := range srv.Requests() {
		if r.Header.Get("X-API-KEY") != key {
			t.Errorf("%s: X-API-KEY header %q, want the key", r.Path, r.Header.Get("X-API-KEY"))
		}
		if strings.Contains(r.Path+"?"+r.RawQuery, key) {
			t.Errorf("the key appears in the URL %s?%s", r.Path, r.RawQuery)
		}
	}

	// No cause the client returns names the key, whatever fails.
	var causes []error
	for _, conn := range []bazarr.Connection{
		{URL: srv.URL, APIKey: key + "x"},
		{URL: srv.URL + "/?apikey=" + key, APIKey: key}, // a pasted link carrying the key
		{URL: "ftp://h", APIKey: key},
		{URL: "http://127.0.0.1:1", APIKey: key},
	} {
		_, err := bazarr.Connect(context.Background(), conn)
		causes = append(causes, err)
	}
	for _, b := range []fakebazarr.Behavior{fakebazarr.Status(401), fakebazarr.Status(500), fakebazarr.Garble} {
		srv.Respond(func(fakebazarr.Request) fakebazarr.Behavior { return b })
		_, err := bazarr.Connect(context.Background(), bazarr.Connection{URL: srv.URL, APIKey: key})
		causes = append(causes, err)
	}
	clock := fakeclock.New(t)
	srv.Respond(nil)
	client = connect(t, srv, clock, bazarr.Limits{})
	srv.Respond(func(fakebazarr.Request) fakebazarr.Behavior { return fakebazarr.Status(500) })
	res := untilGivesUp(t, clock, readLibrary(context.Background(), client), clock.Now().Add(bazarr.RetryWindow))
	causes = append(causes, res.err)
	for _, err := range causes {
		if err == nil || strings.Contains(err.Error(), key) {
			t.Errorf("cause %v: want a cause that does not contain the key", err)
		}
	}
}

func TestReadRetry(t *testing.T) {
	library := fakebazarr.Library{Movies: []fakebazarr.Movie{{RadarrID: 1, Title: "M",
		Subtitles: []fakebazarr.Subtitle{{Path: "/m/M.en.srt", Language: "en"}}}}}

	t.Run("error answers are retried", func(t *testing.T) {
		for name, b := range map[string]fakebazarr.Behavior{
			"garbled 200": fakebazarr.Garble, "500": fakebazarr.Status(500),
			"200 without data": fakebazarr.Body(`{"total": 0}`), "200 with null data": fakebazarr.Body(`{"data": null}`),
			"200 movie without ID": fakebazarr.Body(`{"data": [{"title": "M", "subtitles": []}]}`),
		} {
			srv := fakebazarr.New(t, fakebazarr.Config{Library: library})
			clock := fakeclock.New(t)
			client := connect(t, srv, clock, bazarr.Limits{})
			srv.Respond(func(r fakebazarr.Request) fakebazarr.Behavior {
				if r.N == 2 {
					return b
				}
				return fakebazarr.Serve
			})
			done := readLibrary(context.Background(), client)
			clock.Advance(clock.WaitSleep())
			res := await(t, done)
			if res.err != nil || !res.retried || len(res.lib.Subtitles()) != 1 {
				t.Errorf("%s: library %+v, retried %v, err %v; want the subtitle after a retry", name, res.lib, res.retried, res.err)
			}
			if n := len(srv.Requests()); n != 4 {
				t.Errorf("%s: %d requests, want the check, movies twice, and series", name, n)
			}
		}
	})

	t.Run("gives up five minutes after the first failure", func(t *testing.T) {
		srv := fakebazarr.New(t, fakebazarr.Config{Library: library})
		clock := fakeclock.New(t)
		client := connect(t, srv, clock, bazarr.Limits{Read: 50 * time.Second})
		clock.Advance(time.Hour) // the window is not measured from the run's start
		start := clock.Now()
		end := start.Add(5 * time.Minute) // G3's words, not the constant under test
		// Four failed answers, then every attempt held until its limit.
		srv.Respond(func(r fakebazarr.Request) fakebazarr.Behavior {
			if r.N <= 5 {
				return fakebazarr.Status(500)
			}
			return fakebazarr.Hold
		})
		done := readLibrary(context.Background(), client)
		for range 4 {
			clock.Advance(clock.WaitSleep())
		}
		for {
			srv.WaitHeld()
			d := clock.WaitTimer()
			if clock.Now().Add(d).After(end) {
				t.Fatalf("an attempt sent at %v has a limit past the window's end", clock.Now().Sub(start))
			}
			clock.Advance(d)
			if !clock.Now().Before(end) {
				break
			}
			clock.Advance(clock.WaitSleep())
			if !clock.Now().Before(end) {
				break
			}
		}
		res := await(t, done)
		var ue *bazarr.UnreachableError
		if !errors.As(res.err, &ue) {
			t.Fatalf("err = %v, want UnreachableError", res.err)
		}
		if got := clock.Now().Sub(start); got != 5*time.Minute {
			t.Errorf("gave up %v after the first failure, want exactly 5m", got)
		}
		if !strings.Contains(ue.Error(), srv.URL) || !strings.Contains(ue.Last.Error(), "no answer within") {
			t.Errorf("cause %q: want the address and the last failure, the attempt cut short", ue.Error())
		}
	})

	t.Run("a later outage gets its own five minutes", func(t *testing.T) {
		srv := fakebazarr.New(t, fakebazarr.Config{Library: library})
		clock := fakeclock.New(t)
		client := connect(t, srv, clock, bazarr.Limits{})
		var failing atomic.Bool
		failing.Store(true)
		srv.Respond(func(fakebazarr.Request) fakebazarr.Behavior {
			if failing.Load() {
				return fakebazarr.Status(503)
			}
			return fakebazarr.Serve
		})
		// The first outage lasts over four minutes, then a read succeeds.
		done := readLibrary(context.Background(), client)
		start := clock.Now()
		for clock.Now().Sub(start) < 4*time.Minute {
			clock.Advance(clock.WaitSleep())
		}
		d := clock.WaitSleep()
		failing.Store(false)
		clock.Advance(d)
		if res := await(t, done); res.err != nil || !res.retried {
			t.Fatalf("retried %v, err %v; want success after retries", res.retried, res.err)
		}
		// The second outage starts at T and gets its five minutes from T.
		failing.Store(true)
		second := clock.Now()
		res := untilGivesUp(t, clock, readLibrary(context.Background(), client), second.Add(5*time.Minute))
		var ue *bazarr.UnreachableError
		if !errors.As(res.err, &ue) {
			t.Fatalf("err = %v, want UnreachableError", res.err)
		}
		if got := clock.Now().Sub(second); got != 5*time.Minute {
			t.Errorf("second outage gave up after %v, want 5m", got)
		}
		if !strings.Contains(ue.Last.Error(), "503") {
			t.Errorf("last failure %q, want the 503", ue.Last)
		}
	})
}

func TestReadLimit(t *testing.T) {
	srv := fakebazarr.New(t, fakebazarr.Config{})
	clock := fakeclock.New(t)
	client := connect(t, srv, clock, bazarr.Limits{Read: 9 * time.Second})
	srv.Respond(func(r fakebazarr.Request) fakebazarr.Behavior {
		if r.N == 2 {
			return fakebazarr.Hold
		}
		return fakebazarr.Serve
	})
	done := readLibrary(context.Background(), client)
	srv.WaitHeld()
	if d := clock.WaitTimer(); d != 9*time.Second {
		t.Fatalf("held read's limit = %v, want the 9s the test set", d)
	}
	clock.Advance(9 * time.Second)
	// The abandoned read counts as failed: the client waits, then retries.
	clock.Advance(clock.WaitSleep())
	res := await(t, done)
	if res.err != nil || !res.retried {
		t.Fatalf("retried %v, err %v; want success after a retry", res.retried, res.err)
	}
}

func TestStop(t *testing.T) {
	t.Run("while a read is held", func(t *testing.T) {
		srv := fakebazarr.New(t, fakebazarr.Config{})
		client := connect(t, srv, fakeclock.New(t), bazarr.Limits{})
		srv.Respond(func(fakebazarr.Request) fakebazarr.Behavior { return fakebazarr.Hold })
		ctx, stop := context.WithCancel(context.Background())
		done := readLibrary(ctx, client)
		srv.WaitHeld()
		stop()
		if res := await(t, done); !errors.Is(res.err, bazarr.ErrStopped) {
			t.Fatalf("err = %v, want ErrStopped", res.err)
		}
		if n := len(srv.Requests()); n != 2 {
			t.Errorf("%d requests, want the check and the held read only", n)
		}
	})
	t.Run("during the wait between attempts", func(t *testing.T) {
		srv := fakebazarr.New(t, fakebazarr.Config{})
		clock := fakeclock.New(t)
		client := connect(t, srv, clock, bazarr.Limits{})
		srv.Respond(func(fakebazarr.Request) fakebazarr.Behavior { return fakebazarr.Status(http.StatusInternalServerError) })
		ctx, stop := context.WithCancel(context.Background())
		done := readLibrary(ctx, client)
		clock.WaitSleep()
		stop()
		if res := await(t, done); !errors.Is(res.err, bazarr.ErrStopped) {
			t.Fatalf("err = %v, want ErrStopped", res.err)
		}
		clock.Advance(time.Hour)
		if n := len(srv.Requests()); n != 2 {
			t.Errorf("%d requests, want the check and the failed read only", n)
		}
	})
	t.Run("before a read", func(t *testing.T) {
		srv := fakebazarr.New(t, fakebazarr.Config{})
		client := connect(t, srv, nil, bazarr.Limits{})
		ctx, stop := context.WithCancel(context.Background())
		stop()
		if _, _, err := client.Library(ctx); !errors.Is(err, bazarr.ErrStopped) {
			t.Fatalf("err = %v, want ErrStopped", err)
		}
		if n := len(srv.Requests()); n != 1 {
			t.Errorf("%d requests, want only the check", n)
		}
	})
}
