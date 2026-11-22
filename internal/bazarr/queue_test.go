package bazarr_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/bazarr"
	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/fakebazarr"
	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/fakeclock"
)

func readQueue(t *testing.T, client *bazarr.Client) bazarr.Queue {
	t.Helper()
	q, retried, err := client.Queue(context.Background())
	if err != nil || retried {
		t.Fatalf("queue read: retried %v, %v", retried, err)
	}
	return q
}

// A sync job of a subtitle is a pending or running job named exactly
// "Syncing <path>" or "Synced <path>" (S1); one read shows every path.
func TestQueueSyncJobs(t *testing.T) {
	srv, client := changeServer(t, nil, bazarr.Limits{})
	for name, state := range map[string]fakebazarr.JobState{
		"Syncing movies with Radarr": fakebazarr.JobPending, // Bazarr's own tasks
		"Synced series with Sonarr":  fakebazarr.JobRunning,
		"Syncing /m/pending.srt":     fakebazarr.JobPending,
		"Syncing /m/running.srt":     fakebazarr.JobRunning,
		"Synced /m/ran.srt":          fakebazarr.JobRunning,
		"Synced /m/odd.srt":          fakebazarr.JobPending,
		"Synced /m/completed.srt":    fakebazarr.JobCompleted, // still listed after it ended
		"Syncing /m/failed.srt":      fakebazarr.JobFailed,
		"Syncing /m/moving.srt":      fakebazarr.JobMoving, // shown in neither queue (S2)
		"Syncing /m/a.en.srt.bak":    fakebazarr.JobPending,
		"Syncing /m/V.de.srt":        fakebazarr.JobRunning,
		"Translating /m/t.srt":       fakebazarr.JobRunning,
	} {
		srv.AddJob(name, state)
	}
	q := readQueue(t, client)
	if len(q.Jobs) != 11 {
		t.Errorf("%d jobs, want the 11 Bazarr lists", len(q.Jobs))
	}
	for path, want := range map[string]bool{
		"/m/pending.srt": true, "/m/running.srt": true, "/m/ran.srt": true, "/m/odd.srt": true,
		"/m/completed.srt": false, "/m/failed.srt": false, "/m/moving.srt": false,
		"/m/a.en.srt": false, "/m/a.en": false, "/m/V.en.srt": false, "/m/t.srt": false, "/m/pending": false,
		"movies with Radarr": true, // a name is all Bazarr gives; no subtitle has this path
	} {
		if got := q.HasSyncOf(path); got != want {
			t.Errorf("HasSyncOf(%q) = %v, want %v", path, got, want)
		}
	}
}

// A sync the client submits is followed through the fake's queue as Bazarr
// 1.6.2 moves it, and a restart empties the queue.
func TestQueueFollowsSync(t *testing.T) {
	srv, client := changeServer(t, nil, bazarr.Limits{})
	if res, err := client.Sync(context.Background(), movieSub, bazarr.SyncOptions{}); err != nil || res.Outcome != bazarr.Accepted {
		t.Fatalf("sync: %+v, %v", res, err)
	}
	id, ok := srv.SyncJob(movieSub.Path)
	if !ok {
		t.Fatal("the fake queued no job")
	}
	other := episodeSub.Path
	step := func(what string, want bool) {
		t.Helper()
		q := readQueue(t, client)
		if q.HasSyncOf(movieSub.Path) != want || q.HasSyncOf(other) {
			t.Errorf("%s: queue %+v shows a sync job of it: %v, want %v (and none of %s)", what, q.Jobs, !want, want, other)
		}
	}
	step("queued", true)
	srv.SetJob(id, fakebazarr.JobMoving)
	step("between pending and running", false)
	srv.SetJob(id, fakebazarr.JobRunning)
	step("running", true)
	srv.SyncRan(id, "11/22/26 09:17:04")
	step("ran, renamed", true)
	srv.SetJob(id, fakebazarr.JobCompleted)
	step("completed", false)
	if q := readQueue(t, client); len(q.Jobs) != 1 || q.Jobs[0] != (bazarr.Job{Name: "Synced " + movieSub.Path, Status: "completed"}) {
		t.Errorf("ended: jobs %+v, want the job still listed, renamed and completed", q.Jobs)
	}

	if _, err := client.Sync(context.Background(), movieSub, bazarr.SyncOptions{}); err != nil {
		t.Fatal(err)
	}
	step("queued again", true)
	srv.Restart("1763800000.25")
	if q := readQueue(t, client); len(q.Jobs) != 0 {
		t.Errorf("after a restart: jobs %+v, want none", q.Jobs)
	}
	start, _, err := client.StartTime(context.Background())
	if err != nil || !bazarr.Restarted(client.Status().StartTime, start) {
		t.Errorf("start time after a restart %q (%v), check's %q; want Bazarr restarted", start, err, client.Status().StartTime)
	}
}

// An answer that is not Bazarr's queue is a failed read, never an empty
// queue.
func TestQueueNotBazarrs(t *testing.T) {
	for name, b := range map[string]fakebazarr.Behavior{
		"web page": fakebazarr.Reply(200, "<!doctype html><html><head><title>Bazarr</title></head></html>"),
		"no data":  fakebazarr.Body(`{"total": 0}`), "null data": fakebazarr.Body(`{"data": null}`),
		"job without name":   fakebazarr.Body(`{"data": [{"job_id": 1, "status": "pending"}]}`),
		"job without status": fakebazarr.Body(`{"data": [{"job_id": 1, "job_name": "Syncing /m/a.srt"}]}`),
		"garbled":            fakebazarr.Garble, "500": fakebazarr.Status(500),
	} {
		clock := fakeclock.New(t)
		srv, client := changeServer(t, clock, bazarr.Limits{})
		srv.AddJob("Syncing "+movieSub.Path, fakebazarr.JobPending)
		srv.Respond(func(r fakebazarr.Request) fakebazarr.Behavior {
			if r.N == 2 {
				return b
			}
			return fakebazarr.Serve
		})
		done := make(chan bazarr.Queue, 1)
		var retried bazarr.Retried
		var err error
		go func() {
			var q bazarr.Queue
			q, retried, err = client.Queue(context.Background())
			done <- q
		}()
		clock.Advance(clock.WaitSleep())
		q := await(t, done)
		if err != nil || !bool(retried) || !q.HasSyncOf(movieSub.Path) {
			t.Errorf("%s: jobs %+v, retried %v, %v; want the queue read again and the sync job shown", name, q.Jobs, retried, err)
		}
	}
}

// The queue read tells when its answered request was sent and answered on
// the client's clock: after a retry, the attempt that succeeded.
func TestQueueTimes(t *testing.T) {
	clock := fakeclock.New(t)
	srv, client := changeServer(t, clock, bazarr.Limits{})
	clock.Advance(42 * time.Second)
	q := readQueue(t, client)
	if !q.Sent.Equal(clock.Now()) || !q.Answered.Equal(clock.Now()) {
		t.Errorf("sent %v answered %v, want both %v", q.Sent, q.Answered, clock.Now())
	}

	// An answer that takes three seconds: answered three seconds after it was sent.
	srv.Respond(func(fakebazarr.Request) fakebazarr.Behavior {
		clock.Advance(3 * time.Second)
		return fakebazarr.Serve
	})
	sent := clock.Now()
	q = readQueue(t, client)
	if !q.Sent.Equal(sent) || !q.Answered.Equal(sent.Add(3*time.Second)) {
		t.Errorf("slow answer: sent %v answered %v, want %v and three seconds later", q.Sent, q.Answered, sent)
	}

	srv.Respond(func(r fakebazarr.Request) fakebazarr.Behavior {
		if r.N == 4 {
			return fakebazarr.Status(503)
		}
		return fakebazarr.Serve
	})
	first := clock.Now()
	done := make(chan bazarr.Queue, 1)
	go func() {
		q, _, _ := client.Queue(context.Background())
		done <- q
	}()
	clock.Advance(clock.WaitSleep())
	q = await(t, done)
	if !q.Sent.After(first) || !q.Sent.Equal(clock.Now()) {
		t.Errorf("after a retry: sent %v, want the second attempt's %v, not the first's %v", q.Sent, clock.Now(), first)
	}
}

// F4, R2: every new call goes under the base path's /api/, the key in its
// header only.
func TestSyncCallsUnderBasePath(t *testing.T) {
	srv := fakebazarr.New(t, fakebazarr.Config{BasePath: "/bazarr", Library: changeLibrary()})
	client := connect(t, srv, nil, bazarr.Limits{})
	ctx := context.Background()
	if _, err := client.Sync(ctx, episodeSub, bazarr.SyncOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := client.Queue(ctx); err != nil {
		t.Fatal(err)
	}
	if _, _, err := client.Events(ctx, movieSub, []bazarr.Video{episodeSub.Video()}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := client.VideoNow(ctx, episodeSub); err != nil {
		t.Fatal(err)
	}
	if _, _, err := client.StartTime(ctx); err != nil {
		t.Fatal(err)
	}
	requests := srv.Requests()[1:]
	var got []string
	for _, r := range requests {
		got = append(got, r.Method+" "+r.Path)
		if r.Header.Get("X-API-KEY") != srv.APIKey() || strings.Contains(r.Path+"?"+r.RawQuery+r.Body, srv.APIKey()) {
			t.Errorf("%s %s: the key must travel in X-API-KEY only (R2)", r.Method, r.Path)
		}
	}
	want := []string{http.MethodPatch + " /bazarr/api/subtitles", "GET /bazarr/api/system/jobs", "GET /bazarr/api/movies/history",
		"GET /bazarr/api/episodes/history", "GET /bazarr/api/episodes", "GET /bazarr/api/system/status"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("requests %q, want %q", got, want)
	}
}
