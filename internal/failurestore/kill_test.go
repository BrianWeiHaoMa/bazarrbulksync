package failurestore_test

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/failurestore"
)

// The longer run, and T3's on Windows, raise -kills and may point -killdir
// at the filesystem under test; see AGENTS.md.
var (
	kills   = flag.Int("kills", 60, "kills of the recording child in TestKilledWhileRecording")
	killDir = flag.String("killdir", "", "directory for TestKilledWhileRecording's state files (default: a temporary one)")
)

// The child's environment: the state path (with childFresh, the directory
// of a new state path per failure), the address, and the prefix of the
// paths it records.
const (
	childPath   = "FAILURESTORE_CHILD_PATH"
	childFresh  = "FAILURESTORE_CHILD_FRESH"
	childAddr   = "FAILURESTORE_CHILD_ADDR"
	childPrefix = "FAILURESTORE_CHILD_PREFIX"
)

func TestMain(m *testing.M) {
	if path := os.Getenv(childPath); path != "" {
		recordUntilKilled(path, os.Getenv(childFresh) != "", os.Getenv(childAddr), os.Getenv(childPrefix))
	}
	os.Exit(m.Run())
}

// freshPath is the state path of a fresh child's i-th failure: a file
// whose directories do not exist before that failure is recorded.
func freshPath(dir string, i int) string {
	return filepath.Join(dir, strconv.Itoa(i), "sub", "state.json")
}

// recordUntilKilled is the child: it records failure after failure, each
// path with itself as its one identity, and reports each on standard
// output once Record has returned. A fresh child records each failure in a
// state file of its own, so that every write is the first.
func recordUntilKilled(path string, fresh bool, address, prefix string) {
	s := failurestore.Load(path, address)
	if err := s.Err(); err != nil {
		fmt.Println("unusable:", err)
		os.Exit(3)
	}
	fmt.Println("ready")
	for i := 0; ; i++ {
		p := prefix + strconv.Itoa(i)
		if fresh {
			s = failurestore.Load(freshPath(path, i), address)
		}
		if err := s.Record(p, []string{p}); err != nil {
			fmt.Println("failed:", err)
			os.Exit(3)
		}
		fmt.Println(p)
	}
}

// killRound starts the child on path, kills it delay after it says it is
// ready, and returns the paths it reported recorded.
func killRound(t *testing.T, path string, fresh bool, prefix string, delay time.Duration) []string {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(self)
	cmd.Env = append(os.Environ(), childPath+"="+path, childAddr+"="+addr, childPrefix+"="+prefix)
	if fresh {
		cmd.Env = append(cmd.Env, childFresh+"=1")
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	lines := bufio.NewScanner(out)
	if !lines.Scan() || lines.Text() != "ready" {
		cmd.Process.Kill()
		cmd.Wait()
		t.Fatalf("child did not start: %q %v", lines.Text(), lines.Err())
	}
	// The parent reads while the child records, so that the child never
	// waits on a full pipe instead of writing.
	done := make(chan []string)
	go func() {
		var reported []string
		for lines.Scan() {
			reported = append(reported, lines.Text())
		}
		done <- reported
	}()
	// The delay chooses the kill's moment; what the test asserts holds
	// whatever moment it lands on.
	time.Sleep(delay)
	killErr := cmd.Process.Kill()
	reported := <-done
	for _, line := range reported {
		if !strings.HasPrefix(line, prefix) {
			t.Fatalf("child: %s", line)
		}
	}
	if killErr != nil {
		t.Fatalf("kill after %d failures: %v", len(reported), killErr)
	}
	cmd.Wait()
	return reported
}

// diskState names what a kill left: the state file and the temporary file,
// each absent, partial (not a state), or complete.
func diskState(t *testing.T, path string) string {
	t.Helper()
	kind := func(p string) string {
		if _, err := os.Stat(p); err != nil {
			return "absent"
		}
		if failurestore.Load(p, addr).Err() != nil {
			return "partial"
		}
		return "complete"
	}
	return "state " + kind(path) + ", tmp " + kind(path+".tmp")
}

// afterKill loads path after a kill and checks that it holds every path in
// known; of the failure being recorded, the child's n-th, nothing or all;
// and none after it. It returns whether the n-th is there and what the
// kill left on disk.
func afterKill(t *testing.T, round int, path string, known map[string]bool, prefix string, n int) (bool, string) {
	t.Helper()
	left := diskState(t, path)
	s := failurestore.Load(path, addr)
	if err := s.Err(); err != nil {
		t.Fatalf("round %d (%s): after the kill: %v", round, left, err)
	}
	for p := range known {
		if ids, ok := s.Lookup(p); !ok || !slices.Equal(ids, []string{p}) {
			t.Fatalf("round %d (%s): reported failure %.40q reads %.40q, %v", round, left, p, ids, ok)
		}
	}
	next := prefix + strconv.Itoa(n)
	ids, ok := s.Lookup(next)
	if ok && !slices.Equal(ids, []string{next}) {
		t.Fatalf("round %d (%s): the failure recorded at the kill reads %q", round, left, ids)
	}
	if later := prefix + strconv.Itoa(n+1); func() bool { _, found := s.Lookup(later); return found }() {
		t.Fatalf("round %d (%s): %q recorded after the kill's failure", round, left, later)
	}
	return ok, left
}

// G22: a child killed at any moment while it records failures leaves a
// state the next load reads, holding every failure the child reported and
// at most the one it was recording, complete.
func TestKilledWhileRecording(t *testing.T) {
	dir := *killDir
	if dir == "" {
		dir = t.TempDir()
	} else {
		dir = filepath.Join(dir, fmt.Sprintf("kills-%d", time.Now().UnixNano()))
		t.Cleanup(func() { os.RemoveAll(dir) })
	}
	outcomes := map[string]int{}
	rounds := 0
	// What each kill left, also when a round fails: T3's evidence.
	defer func() {
		var lines []string
		for k, n := range outcomes {
			lines = append(lines, fmt.Sprintf("%5d  %s", n, k))
		}
		sort.Strings(lines)
		t.Logf("%d kills checked under %s:\n%s", rounds, dir, strings.Join(lines, "\n"))
	}()
	var growPath string
	var growKnown map[string]bool // every path a load of growPath showed or a child reported
	for round := 0; round < *kills; round++ {
		prefix := fmt.Sprintf("/r%d/", round)
		kind := ""
		switch round % 4 {
		case 1, 3:
			// Each failure in a state file of its own: the kill falls in
			// a first write, with no state file and no directory yet.
			kind = "first write"
			base := filepath.Join(dir, fmt.Sprintf("r%d", round))
			reported := killRound(t, base, true, prefix, time.Duration(round/2%10)*time.Millisecond)
			for i, p := range reported {
				afterKill(t, round, freshPath(base, i), map[string]bool{p: true}, prefix, i+1)
			}
			found, left := afterKill(t, round, freshPath(base, len(reported)), nil, prefix, len(reported))
			outcomes[fmt.Sprintf("%s: %s; failure at the kill %s", kind, left, presence(found))]++
			rounds++
			continue
		case 0:
			kind = "later write"
			if round%20 == 0 {
				growPath = filepath.Join(dir, fmt.Sprintf("r%d", round), "sub", "state.json")
				growKnown = map[string]bool{}
			}
		case 2:
			// What a kill between a first write's close and its rename
			// leaves: no state file, a complete temporary file. The
			// child's first write must put it in place before writing.
			// A large record makes that write long enough for kills to
			// fall inside it, where a write over the temporary file before
			// its rename would lose every record.
			kind = "later write"
			if diskState(t, growPath) == "state complete, tmp absent" {
				kind = "pending temporary file"
				s := usable(t, growPath, addr)
				if _, ok := s.Lookup(ballast); !ok {
					record(t, s, ballast, ballast)
					growKnown[ballast] = true
				}
				if err := os.Rename(growPath, growPath+".tmp"); err != nil {
					t.Fatal(err)
				}
			}
		}
		delay := time.Duration(round/4%20) * time.Millisecond
		if round%4 == 2 {
			delay = time.Duration(round/4%20) * 250 * time.Microsecond
		}
		reported := killRound(t, growPath, false, prefix, delay)
		for _, p := range reported {
			growKnown[p] = true
		}
		found, left := afterKill(t, round, growPath, growKnown, prefix, len(reported))
		if found {
			growKnown[prefix+strconv.Itoa(len(reported))] = true
		}
		outcomes[fmt.Sprintf("%s: %s; failure at the kill %s", kind, left, presence(found))]++
		rounds++
	}
}

// ballast is a path of a megabyte, recorded with itself as its identity.
var ballast = "/ballast/" + strings.Repeat("x", 1<<20)

func presence(found bool) string {
	if found {
		return "complete"
	}
	return "absent"
}
