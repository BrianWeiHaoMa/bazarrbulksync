package failurestore_test

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/failurestore"
)

const addr = "http://bazarr:6767"

// statePath is a state file's path in a fresh directory that does not
// exist yet.
func statePath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "config", "bazarrbulksync", "state.json")
}

func record(t *testing.T, s *failurestore.Store, path string, ids ...string) {
	t.Helper()
	if err := s.Record(path, ids); err != nil {
		t.Fatalf("record %q: %v", path, err)
	}
}

func usable(t *testing.T, path, address string) *failurestore.Store {
	t.Helper()
	s := failurestore.Load(path, address)
	if err := s.Err(); err != nil {
		t.Fatalf("load %s for %s: %v", path, address, err)
	}
	return s
}

// wantRecord fails unless s has path's record with exactly ids.
func wantRecord(t *testing.T, s *failurestore.Store, path string, ids ...string) {
	t.Helper()
	got, ok := s.Lookup(path)
	if !ok {
		t.Fatalf("%q: no record, want %q", path, ids)
	}
	if len(got) != len(ids) || !slices.Equal(got, ids) {
		t.Fatalf("%q: record %q, want %q", path, got, ids)
	}
}

func wantNoRecord(t *testing.T, s *failurestore.Store, path string) {
	t.Helper()
	if got, ok := s.Lookup(path); ok {
		t.Fatalf("%q: record %q, want none", path, got)
	}
}

// listing is every name under dir, relative to it, sorted.
func listing(t *testing.T, dir string) []string {
	t.Helper()
	var names []string
	err := filepath.WalkDir(dir, func(p string, _ os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p != dir {
			rel, _ := filepath.Rel(dir, p)
			names = append(names, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return names
}

func read(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func write(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// wantUnusable fails unless s is unusable with a cause naming path and
// answers that no path has a record.
func wantUnusable(t *testing.T, s *failurestore.Store, path string, paths ...string) {
	t.Helper()
	err := s.Err()
	if err == nil {
		t.Fatalf("store usable, want unusable")
	}
	if !strings.Contains(err.Error(), path) {
		t.Fatalf("cause %q does not name %s", err, path)
	}
	for _, p := range paths {
		wantNoRecord(t, s, p)
	}
}

const twoServers = `{"bazarrbulksync_state": 1, "servers": {` +
	`"http://bazarr:6767": {"/movies/A/A.en.srt": ["5 11/20/26 10:00:00"], "/movies/B/B.es.srt": []},` +
	`"https://other/bazarr": {"/movies/A/A.en.srt": ["1 01/02/26 03:04:05", "5 01/03/26 03:04:05"]}}}`

// A record may hold no identities: a subtitle that never had an event
// failed with an empty baseline, and that is still a record.
func TestRecordWithoutIdentities(t *testing.T) {
	path := statePath(t)
	s := usable(t, path, addr)
	record(t, s, "/movies/A/A.en.srt")
	wantRecord(t, s, "/movies/A/A.en.srt")
	if err := s.Record("/movies/B/B.en.srt", []string{}); err != nil {
		t.Fatal(err)
	}

	again := usable(t, path, addr)
	wantRecord(t, again, "/movies/A/A.en.srt")
	wantRecord(t, again, "/movies/B/B.en.srt")
	wantNoRecord(t, again, "/movies/C/C.en.srt")
}

// One state file holds the records of several Bazarr servers, each apart.
func TestServersKeptApart(t *testing.T) {
	path := statePath(t)
	write(t, path, twoServers)
	s := usable(t, path, addr)
	record(t, s, "/tv/S/S01E01.en.srt", "5 11/21/26 09:00:00")

	other := usable(t, path, "https://other/bazarr")
	wantRecord(t, other, "/movies/A/A.en.srt", "1 01/02/26 03:04:05", "5 01/03/26 03:04:05")
	wantNoRecord(t, other, "/tv/S/S01E01.en.srt")
	wantNoRecord(t, other, "/movies/B/B.es.srt")

	record(t, other, "/movies/C/C.en.srt")
	mine := usable(t, path, addr)
	wantRecord(t, mine, "/movies/A/A.en.srt", "5 11/20/26 10:00:00")
	wantRecord(t, mine, "/movies/B/B.es.srt")
	wantRecord(t, mine, "/tv/S/S01E01.en.srt", "5 11/21/26 09:00:00")
	wantNoRecord(t, mine, "/movies/C/C.en.srt")

	fresh := usable(t, path, "http://third:6767")
	wantNoRecord(t, fresh, "/movies/A/A.en.srt")
}

// The address is the key as given with its trailing slashes removed, and
// nothing else about it is changed.
func TestAddressKey(t *testing.T) {
	path := statePath(t)
	s := usable(t, path, "http://h:6767/bazarr//")
	record(t, s, "/a.srt", "5 x")

	for _, same := range []string{"http://h:6767/bazarr", "http://h:6767/bazarr/"} {
		wantRecord(t, usable(t, path, same), "/a.srt", "5 x")
	}
	for _, other := range []string{"http://H:6767/bazarr", "https://h:6767/bazarr", "http://h:6767/bazarr/x", "http://h:6767", "http://h:6767/Bazarr"} {
		wantNoRecord(t, usable(t, path, other), "/a.srt")
	}
	if got := string(read(t, path)); !strings.Contains(got, `"http://h:6767/bazarr":`) {
		t.Fatalf("state file %s does not key the address without its slashes", got)
	}
}

// Loading creates nothing; the first record creates the missing
// directories and the state file, and leaves no temporary file.
func TestFirstRecordCreates(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "a", "b", "state.json")
	s := usable(t, path, addr)
	wantNoRecord(t, s, "/a.srt")
	if got := listing(t, root); len(got) != 0 {
		t.Fatalf("load created %q", got)
	}

	record(t, s, "/a.srt", "5 x")
	if got := listing(t, root); !slices.Equal(got, []string{"a", "a/b", "a/b/state.json"}) {
		t.Fatalf("after the first record the directory holds %q", got)
	}
	record(t, s, "/b.srt")
	if err := s.Forget("/a.srt"); err != nil {
		t.Fatal(err)
	}
	if got := listing(t, root); !slices.Equal(got, []string{"a", "a/b", "a/b/state.json"}) {
		t.Fatalf("after later writes the directory holds %q", got)
	}
	again := usable(t, path, addr)
	wantNoRecord(t, again, "/a.srt")
	wantRecord(t, again, "/b.srt")
}

// A write that fails before its rename leaves the state file as it was,
// and the store then answers that no path has a record.
func TestWriteFails(t *testing.T) {
	path := statePath(t)
	write(t, path, twoServers)
	s := usable(t, path, addr)
	wantRecord(t, s, "/movies/A/A.en.srt", "5 11/20/26 10:00:00")
	// A directory where the temporary file goes makes the write fail where
	// it happens, also for a user whom permissions do not stop.
	if err := os.Mkdir(path+".tmp", 0o700); err != nil {
		t.Fatal(err)
	}

	err := s.Record("/tv/S/S01E01.en.srt", []string{"5 x"})
	if err == nil || !strings.Contains(err.Error(), path) {
		t.Fatalf("record returned %v, want a cause naming %s", err, path)
	}
	wantUnusable(t, s, path, "/movies/A/A.en.srt", "/movies/B/B.es.srt", "/tv/S/S01E01.en.srt")
	if got := string(read(t, path)); got != twoServers {
		t.Fatalf("state file changed to %s", got)
	}
	if err := s.Forget("/movies/A/A.en.srt"); err == nil {
		t.Fatal("forget after a failed write returned no error")
	}
	if got := string(read(t, path)); got != twoServers {
		t.Fatalf("state file changed to %s", got)
	}
	if fi, err := os.Stat(path + ".tmp"); err != nil || !fi.IsDir() {
		t.Fatalf("the directory at the temporary path was touched: %v", err)
	}
}

// Forgetting a failure the run's caller found lapsed is a write too.
func TestForget(t *testing.T) {
	path := statePath(t)
	write(t, path, twoServers)
	s := usable(t, path, addr)
	if err := s.Forget("/movies/A/A.en.srt"); err != nil {
		t.Fatal(err)
	}
	wantNoRecord(t, s, "/movies/A/A.en.srt")

	again := usable(t, path, addr)
	wantNoRecord(t, again, "/movies/A/A.en.srt")
	wantRecord(t, again, "/movies/B/B.es.srt")
	wantRecord(t, usable(t, path, "https://other/bazarr"), "/movies/A/A.en.srt", "1 01/02/26 03:04:05", "5 01/03/26 03:04:05")

	// Forgetting a path without a record writes nothing: a directory in
	// the temporary file's place would make a write fail.
	before := read(t, path)
	if err := os.Mkdir(path+".tmp", 0o700); err != nil {
		t.Fatal(err)
	}
	if err := again.Forget("/nothing.srt"); err != nil || again.Err() != nil {
		t.Fatalf("forgetting nothing wrote: %v, %v", err, again.Err())
	}

	// Forgetting fails as a write fails.
	if err := again.Forget("/movies/B/B.es.srt"); err == nil || !strings.Contains(err.Error(), path) {
		t.Fatalf("forget returned %v, want a cause naming %s", err, path)
	}
	wantUnusable(t, again, path, "/movies/B/B.es.srt")
	if got := read(t, path); !bytes.Equal(got, before) {
		t.Fatalf("a failed forget changed the file to %s", got)
	}
}

// When the state file is missing, a complete temporary file is the state:
// loading reads it and changes nothing, and the next write puts its
// records and the new one in the state file.
func TestTemporaryFile(t *testing.T) {
	t.Run("complete, no state file", func(t *testing.T) {
		path := statePath(t)
		write(t, path+".tmp", twoServers)
		dir := filepath.Dir(path)
		s := usable(t, path, addr)
		wantRecord(t, s, "/movies/A/A.en.srt", "5 11/20/26 10:00:00")
		wantRecord(t, s, "/movies/B/B.es.srt")
		if got := listing(t, dir); !slices.Equal(got, []string{"state.json.tmp"}) {
			t.Fatalf("load changed the directory to %q", got)
		}
		if got := string(read(t, path+".tmp")); got != twoServers {
			t.Fatalf("load changed the temporary file to %s", got)
		}

		record(t, s, "/tv/S/S01E01.en.srt", "5 x")
		if got := listing(t, dir); !slices.Equal(got, []string{"state.json"}) {
			t.Fatalf("after a write the directory holds %q", got)
		}
		again := usable(t, path, addr)
		wantRecord(t, again, "/movies/A/A.en.srt", "5 11/20/26 10:00:00")
		wantRecord(t, again, "/tv/S/S01E01.en.srt", "5 x")
		wantRecord(t, usable(t, path, "https://other/bazarr"), "/movies/A/A.en.srt", "1 01/02/26 03:04:05", "5 01/03/26 03:04:05")
	})
	t.Run("truncated, no state file", func(t *testing.T) {
		path := statePath(t)
		write(t, path+".tmp", twoServers[:len(twoServers)/2])
		s := usable(t, path, addr)
		wantNoRecord(t, s, "/movies/A/A.en.srt")
		record(t, s, "/a.srt")
		again := usable(t, path, addr)
		wantRecord(t, again, "/a.srt")
		wantNoRecord(t, again, "/movies/A/A.en.srt")
	})
	t.Run("beside a state file", func(t *testing.T) {
		path := statePath(t)
		write(t, path, `{"bazarrbulksync_state": 1, "servers": {}}`)
		write(t, path+".tmp", twoServers)
		s := usable(t, path, addr)
		wantNoRecord(t, s, "/movies/A/A.en.srt")
		record(t, s, "/a.srt")
		again := usable(t, path, addr)
		wantRecord(t, again, "/a.srt")
		wantNoRecord(t, again, "/movies/A/A.en.srt")
		if got := listing(t, filepath.Dir(path)); !slices.Equal(got, []string{"state.json"}) {
			t.Fatalf("after a write the directory holds %q", got)
		}
	})
	t.Run("beside a state file that is not one", func(t *testing.T) {
		path := statePath(t)
		write(t, path, `{}`)
		write(t, path+".tmp", twoServers)
		s := failurestore.Load(path, addr)
		wantUnusable(t, s, path, "/movies/A/A.en.srt")
		if err := s.Record("/a.srt", nil); err == nil {
			t.Fatal("record on an unusable store returned no error")
		}
		if got := string(read(t, path+".tmp")); got != twoServers {
			t.Fatalf("temporary file changed to %s", got)
		}
	})
}

// A state file that cannot be read makes the store unusable, as one of
// another shape does, and nothing is written.
func TestUnreadable(t *testing.T) {
	t.Run("directory", func(t *testing.T) {
		path := statePath(t)
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
		s := failurestore.Load(path, addr)
		wantUnusable(t, s, path, "/a.srt")
		if err := s.Record("/a.srt", nil); err == nil {
			t.Fatal("record on an unusable store returned no error")
		}
		if got := listing(t, filepath.Dir(path)); !slices.Equal(got, []string{"state.json"}) {
			t.Fatalf("directory holds %q", got)
		}
	})
	t.Run("parent is a file", func(t *testing.T) {
		root := t.TempDir()
		write(t, filepath.Join(root, "config"), "a file")
		path := filepath.Join(root, "config", "state.json")
		s := failurestore.Load(path, addr)
		if s.Err() == nil {
			// Windows reports a missing path here; the first write then fails.
			if err := s.Record("/a.srt", nil); err == nil {
				t.Fatal("record under a file returned no error")
			}
		}
		wantUnusable(t, s, path, "/a.srt")
		if got := string(read(t, filepath.Join(root, "config"))); got != "a file" {
			t.Fatalf("the file in the way changed to %q", got)
		}
	})
	t.Run("no read permission", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("permissions do not stop root; run as another user to see this case")
		}
		path := statePath(t)
		write(t, path, twoServers)
		if err := os.Chmod(path, 0o200); err != nil {
			t.Fatal(err)
		}
		if f, err := os.Open(path); err == nil {
			f.Close()
			t.Skip("this system lets the owner read a file without read permission")
		}
		s := failurestore.Load(path, addr)
		wantUnusable(t, s, path, "/movies/A/A.en.srt")
		if err := s.Record("/a.srt", nil); err == nil {
			t.Fatal("record on an unusable store returned no error")
		}
		if err := os.Chmod(path, 0o600); err != nil {
			t.Fatal(err)
		}
		if got := string(read(t, path)); got != twoServers {
			t.Fatalf("file changed to %s", got)
		}
		if got := listing(t, filepath.Dir(path)); !slices.Equal(got, []string{"state.json"}) {
			t.Fatalf("directory holds %q", got)
		}
	})
}

// Paths and identities come back byte for byte, compared as opaque text.
func TestBytesRoundTrip(t *testing.T) {
	paths := []string{
		`/movies/Amélie (2001)/Amélie "Le Fabuleux" <Destin> & co.fr.srt`,
		`C:\Media\Movies\Ça\Ça.fr.srt`,
		"/tv/東京/Season 01/東京 - S01E01 .ja.srt",
		"/movies/tab\tand\nnewline.srt",
		"/movies/back\\slash/'quote'.en.srt",
	}
	ids := []string{"5 11/22/26 08:15:02", "  spaced  ", `"quoted" \ <x> & é`, ""}
	path := statePath(t)
	s := usable(t, path, addr)
	for _, p := range paths {
		record(t, s, p, ids...)
	}
	again := usable(t, path, addr)
	for _, p := range paths {
		wantRecord(t, again, p, ids...)
	}
	wantNoRecord(t, again, strings.ToUpper(paths[0]))
	wantNoRecord(t, again, paths[0]+" ")
}

// The identities a caller hands over, and those the store hands back, are
// copies: changing one changes nothing in the store.
func TestRecordsAreCopies(t *testing.T) {
	path := statePath(t)
	s := usable(t, path, addr)
	ids := []string{"5 a", "1 b"}
	record(t, s, "/a.srt", ids...)
	ids[0] = "changed"
	got, _ := s.Lookup("/a.srt")
	got[1] = "changed too"
	wantRecord(t, s, "/a.srt", "5 a", "1 b")
	wantRecord(t, usable(t, path, addr), "/a.srt", "5 a", "1 b")
}

// A path that is not a regular file is not a state file, and loading it
// never waits.
func TestNotARegularFile(t *testing.T) {
	path := statePath(t)
	if err := makeFIFO(path); err != nil {
		t.Skip("no named pipes here:", err)
	}
	loaded := make(chan *failurestore.Store)
	go func() { loaded <- failurestore.Load(path, addr) }()
	select {
	case s := <-loaded:
		wantUnusable(t, s, path, "/a.srt")
	case <-time.After(10 * time.Second):
		t.Fatal("Load waits on a named pipe") // a guard; Load never needs time
	}
}

// Without a path there is no state file: nothing is read or written, not
// even beside the working directory.
func TestNoPath(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	write(t, filepath.Join(dir, ".tmp"), twoServers)
	s := failurestore.Load("", addr)
	if s.Err() == nil {
		t.Fatal("a store without a path is usable")
	}
	wantNoRecord(t, s, "/movies/A/A.en.srt")
	if err := s.Record("/a.srt", nil); err == nil {
		t.Fatal("record without a path returned no error")
	}
	if got := listing(t, dir); !slices.Equal(got, []string{".tmp"}) {
		t.Fatalf("directory holds %q", got)
	}
}

// A leftover temporary file is never written through: a link there to the
// state file leaves the state whole.
func TestLeftoverTemporaryLink(t *testing.T) {
	t.Run("hard link", func(t *testing.T) {
		path := statePath(t)
		write(t, path, twoServers)
		if err := os.Link(path, path+".tmp"); err != nil {
			t.Skip("no hard links here:", err)
		}
		s := usable(t, path, addr)
		record(t, s, "/a.srt")
		if got := listing(t, filepath.Dir(path)); !slices.Equal(got, []string{"state.json"}) {
			t.Fatalf("directory holds %q", got)
		}
		again := usable(t, path, addr)
		wantRecord(t, again, "/a.srt")
		wantRecord(t, again, "/movies/A/A.en.srt", "5 11/20/26 10:00:00")
	})
	t.Run("symbolic link", func(t *testing.T) {
		path := statePath(t)
		write(t, path, twoServers)
		if err := os.Symlink(path, path+".tmp"); err != nil {
			t.Skip("no symbolic links here:", err)
		}
		s := usable(t, path, addr)
		record(t, s, "/a.srt")
		if fi, err := os.Lstat(path); err != nil || !fi.Mode().IsRegular() {
			t.Fatalf("state file after the write: %v, %v", fi, err)
		}
		again := usable(t, path, addr)
		wantRecord(t, again, "/a.srt")
		wantRecord(t, again, "/movies/A/A.en.srt", "5 11/20/26 10:00:00")
	})
}
