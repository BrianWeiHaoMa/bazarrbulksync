package fakebazarr_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/fakebazarr"
)

// Real Bazarr 1.6.2 answers a call it does not have, even under /api/, with
// or without a key, with its web page and HTTP 200, not with 404 or 401.
func TestUnknownCallAnswersWebPage(t *testing.T) {
	srv := fakebazarr.New(t, fakebazarr.Config{})
	for _, key := range []string{srv.APIKey(), ""} {
		req, err := http.NewRequest("GET", srv.URL+"/api/nonexistent", nil)
		if err != nil {
			t.Fatal(err)
		}
		if key != "" {
			req.Header.Set("X-API-KEY", key)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/html") {
			t.Errorf("unknown call (key given: %v) answered %d %q, want 200 and Bazarr's web page",
				key != "", resp.StatusCode, resp.Header.Get("Content-Type"))
		}
	}
}
