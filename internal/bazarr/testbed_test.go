package bazarr_test

import (
	"context"
	"slices"
	"testing"

	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/bazarr"
	"github.com/BrianWeiHaoMa/bazarrbulksync/testbed/bed"
)

// TestTestbed holds the connection check and the library read against the
// real Bazarr of the test bed (`go run ./testbed up`). It runs only when
// BAZARRBULKSYNC_TESTBED names the bed's file, never on BAZARR_URL and
// BAZARR_API_KEY, so that `go test ./...` stays offline.
func TestTestbed(t *testing.T) {
	m, ok, err := bed.Load()
	if !ok {
		t.Skipf("no test bed: set %s to the file `go run ./testbed up` writes", bed.EnvVar)
	}
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	client, err := bazarr.Connect(ctx, bazarr.Connection{URL: m.URL, APIKey: m.APIKey})
	if err != nil {
		t.Fatalf("connection check: %v", err)
	}
	t.Logf("Bazarr %s, started at %s", client.Status().Version, client.Status().StartTime)

	lib, retried, err := client.Library(ctx)
	if err != nil {
		t.Fatalf("library read: %v", err)
	}
	if retried {
		t.Errorf("the library read needed a retry against a healthy Bazarr")
	}
	// Exactly the files placed, each with every attribute; every movie and
	// series, the movie without a subtitle included, in Bazarr's order.
	for _, d := range m.Differences(lib) {
		t.Error(d)
	}
	// The reads a sync is judged from: on a freshly provisioned bed no
	// subtitle has an event or a sync job, and each video shows its
	// subtitles as the library read did.
	queue, _, err := client.Queue(ctx)
	if err != nil {
		t.Fatalf("queue read: %v", err)
	}
	for _, sub := range m.Subtitles {
		if queue.HasSyncOf(sub.Path) {
			t.Errorf("the queue shows a sync job of %s", sub.Path)
		}
		events, _, err := client.Events(ctx, sub, nil)
		if err != nil || len(events) != 0 {
			t.Errorf("events of %s: %q, %v; want none", sub.Path, events.Identities(), err)
		}
		video, _, err := client.VideoNow(ctx, sub)
		if err != nil || !video.Listed || !slices.Contains(video.Subtitles, sub) {
			t.Errorf("video of %s: %+v, %v; want it listed with the subtitle", sub.Path, video, err)
		}
	}
	gone := m.Subtitles[0]
	gone.MovieID, gone.EpisodeID = 99999, 99999
	if video, retried, err := client.VideoNow(ctx, gone); err != nil || bool(retried) || video.Listed {
		t.Errorf("video Bazarr lacks: %+v, retried %v, %v; want a successful read showing it unlisted", video, retried, err)
	}
	if start, _, err := client.StartTime(ctx); err != nil || bazarr.Restarted(client.Status().StartTime, start) {
		t.Errorf("start time %q, %v; want the connection check's %q", start, err, client.Status().StartTime)
	}
	// The embedded tracks are in Bazarr's answer, as entries without a path,
	// so their absence above is the client leaving them out.
	if got := embeddedInAnswer(t, m); len(m.Embedded) == 0 || !slices.Equal(got, m.Embedded) {
		t.Errorf("videos with an embedded track in Bazarr's answer %v, want %v (and at least one)", got, m.Embedded)
	}
}

// embeddedInAnswer returns the paths of the videos that have a subtitle
// entry whose path is null in Bazarr's own answer.
func embeddedInAnswer(t *testing.T, m bed.Manifest) []string {
	t.Helper()
	videos, err := m.Shown()
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, v := range videos {
		if slices.Contains(v.Subtitles, nil) {
			paths = append(paths, v.Path)
		}
	}
	return paths
}
