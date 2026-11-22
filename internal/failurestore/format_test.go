package failurestore_test

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/failurestore"
)

// A file of any other shape than the state's is not a state file: the
// store cannot be used, and recording a failure leaves the file as it was
// and writes no temporary file.
func TestNotAStateFile(t *testing.T) {
	cases := map[string]string{
		"empty object":                 `{}`,
		"array":                        `[]`,
		"newer version":                `{"bazarrbulksync_state": 2, "servers": {}}`,
		"version as text":              `{"bazarrbulksync_state": "1", "servers": {}}`,
		"version as decimal":           `{"bazarrbulksync_state": 1.0, "servers": {}}`,
		"third member":                 `{"bazarrbulksync_state": 1, "servers": {}, "note": "x"}`,
		"missing servers":              `{"bazarrbulksync_state": 1}`,
		"repeated member":              `{"bazarrbulksync_state": 1, "servers": {}, "servers": {}}`,
		"servers null":                 `{"bazarrbulksync_state": 1, "servers": null}`,
		"servers array":                `{"bazarrbulksync_state": 1, "servers": []}`,
		"server not object":            `{"bazarrbulksync_state": 1, "servers": {"http://bazarr:6767": ["/a.srt"]}}`,
		"server null":                  `{"bazarrbulksync_state": 1, "servers": {"http://bazarr:6767": null}}`,
		"record not array":             `{"bazarrbulksync_state": 1, "servers": {"http://bazarr:6767": {"/a.srt": "5 x"}}}`,
		"record null":                  `{"bazarrbulksync_state": 1, "servers": {"http://bazarr:6767": {"/a.srt": null}}}`,
		"identity not string":          `{"bazarrbulksync_state": 1, "servers": {"http://bazarr:6767": {"/a.srt": [5]}}}`,
		"server with a trailing slash": `{"bazarrbulksync_state": 1, "servers": {"http://bazarr:6767/": {"/a.srt": []}}}`,
		"repeated path":                `{"bazarrbulksync_state": 1, "servers": {"http://bazarr:6767": {"/a.srt": [], "/a.srt": []}}}`,
		"trailing data":                `{"bazarrbulksync_state": 1, "servers": {}} {}`,
		"truncated":                    `{"bazarrbulksync_state": 1, "servers": {"http://bazarr:6767": {"/a.srt": ["5`,
		"invalid UTF-8":                "{\"bazarrbulksync_state\": 1, \"servers\": {\"http://bazarr:6767\": {\"/\xff.srt\": []}}}",
		"byte order mark":              "\ufeff{\"bazarrbulksync_state\": 1, \"servers\": {}}",
		"plain text":                   "not json at all\n",
		"zero bytes":                   "",
		"another tool's settings":      `{"theme": "dark", "servers": {"http://bazarr:6767": {}}}`,
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			path := statePath(t)
			write(t, path, content)
			s := failurestore.Load(path, addr)
			wantUnusable(t, s, path, "/a.srt")
			if err := s.Record("/a.srt", []string{"5 11/22/26 08:15:02"}); err == nil {
				t.Fatal("record on an unusable store returned no error")
			}
			if err := s.Forget("/a.srt"); err == nil {
				t.Fatal("forget on an unusable store returned no error")
			}
			if got := read(t, path); string(got) != content {
				t.Fatalf("file changed to %q", got)
			}
			if got := listing(t, filepath.Dir(path)); !slices.Equal(got, []string{"state.json"}) {
				t.Fatalf("directory holds %q", got)
			}
		})
	}
}

// The state file the store writes is the one object of two members, and a
// state written by hand in that shape loads.
func TestStateFormat(t *testing.T) {
	path := statePath(t)
	write(t, path, twoServers)
	s := usable(t, path, addr)
	wantRecord(t, s, "/movies/A/A.en.srt", "5 11/20/26 10:00:00")
	wantRecord(t, s, "/movies/B/B.es.srt")
	record(t, s, "/tv/S/S01E01.en.srt", "5 11/21/26 09:00:00", "1 11/19/26 09:00:00")

	want := `{"bazarrbulksync_state":1,"servers":{` +
		`"http://bazarr:6767":{"/movies/A/A.en.srt":["5 11/20/26 10:00:00"],"/movies/B/B.es.srt":[],` +
		`"/tv/S/S01E01.en.srt":["5 11/21/26 09:00:00","1 11/19/26 09:00:00"]},` +
		`"https://other/bazarr":{"/movies/A/A.en.srt":["1 01/02/26 03:04:05","5 01/03/26 03:04:05"]}}}` + "\n"
	if got := string(read(t, path)); got != want {
		t.Fatalf("state file\n%s\nwant\n%s", got, want)
	}
}
