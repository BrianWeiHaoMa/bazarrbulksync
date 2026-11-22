package bazarr_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
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
	// The embedded tracks are in Bazarr's answer, as entries without a path,
	// so their absence above is the client leaving them out.
	if got := embeddedInAnswer(t, m); len(m.Embedded) == 0 || !slices.Equal(got, m.Embedded) {
		t.Errorf("videos with an embedded track in Bazarr's answer %v, want %v (and at least one)", got, m.Embedded)
	}
}

// noRedirects follows no redirect, which would carry the key elsewhere.
var noRedirects = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

// embeddedInAnswer reads Bazarr's movies and episodes directly and returns
// the paths of the videos that have a subtitle entry whose path is null.
func embeddedInAnswer(t *testing.T, m bed.Manifest) []string {
	t.Helper()
	calls := []string{"movies"}
	for _, s := range m.Series {
		calls = append(calls, fmt.Sprintf("episodes?seriesid[]=%d", s.ID))
	}
	var paths []string
	for _, call := range calls {
		req, err := http.NewRequest("GET", m.URL+"/api/"+call, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("X-API-KEY", m.APIKey)
		resp, err := noRedirects.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		var answer struct {
			Data []struct {
				Path      string `json:"path"`
				Subtitles []struct {
					Path *string `json:"path"`
				} `json:"subtitles"`
			} `json:"data"`
		}
		err = json.NewDecoder(resp.Body).Decode(&answer)
		resp.Body.Close()
		if err != nil {
			t.Fatalf("GET %s: %v", call, err)
		}
		for _, v := range answer.Data {
			for _, sub := range v.Subtitles {
				if sub.Path == nil {
					paths = append(paths, v.Path)
					break
				}
			}
		}
	}
	return paths
}
