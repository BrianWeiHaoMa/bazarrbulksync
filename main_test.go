package main_test

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/fakebazarr"
)

// executable is the program built once for these tests, without cgo, as it
// is released.
var executable string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "bazarrbulksync-test-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	executable = filepath.Join(dir, "bazarrbulksync")
	if runtime.GOOS == "windows" {
		executable += ".exe"
	}
	build := exec.Command("go", "build", "-o", executable, ".")
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "building the executable: %v\n%s", err, out)
		os.RemoveAll(dir)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// result is what one run of the executable left behind.
type result struct {
	code           int
	stdout, stderr string
}

// command prepares the executable with args, its environment holding only
// BAZARR_URL and BAZARR_API_KEY, and its output streams captured through pipes.
func command(url, key string, args ...string) (*exec.Cmd, *bytes.Buffer, *bytes.Buffer) {
	cmd := exec.Command(executable, args...)
	cmd.Env = []string{"BAZARR_URL=" + url, "BAZARR_API_KEY=" + key}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	return cmd, &stdout, &stderr
}

// runExecutable runs the executable to its end.
func runExecutable(t *testing.T, url, key string, args ...string) result {
	t.Helper()
	cmd, stdout, stderr := command(url, key, args...)
	err := cmd.Start()
	if err != nil {
		t.Fatal(err)
	}
	waitExit(t, cmd)
	return result{code: cmd.ProcessState.ExitCode(), stdout: stdout.String(), stderr: stderr.String()}
}

// waitExit waits for cmd to end by itself; the deadline only turns a hang
// into a failure, and the process is killed then.
func waitExit(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		_ = cmd.Process.Kill()
		<-done
		t.Fatalf("%v did not end within 20s", cmd.Args)
	}
}

func TestExecutableUsage(t *testing.T) {
	srv := fakebazarr.New(t, fakebazarr.Config{})
	for _, args := range [][]string{{"list", "--movies", "--series"}, {"sync"}, {}} {
		r := runExecutable(t, srv.URL, srv.APIKey(), args...)
		if r.code == 0 || r.stdout != "" || !strings.Contains(r.stderr, "--help") {
			t.Errorf("%q: code %d, stdout %q, stderr %q; want a usage error on standard error, nonzero (G1)", args, r.code, r.stdout, r.stderr)
		}
	}
	for _, args := range [][]string{{"--help"}, {"--version"}, {"list", "--help"}} {
		r := runExecutable(t, srv.URL, srv.APIKey(), args...)
		if r.code != 0 || r.stdout == "" || r.stderr != "" {
			t.Errorf("%q: code %d, stdout %q, stderr %q; want text on standard output, exit 0 (F5)", args, r.code, r.stdout, r.stderr)
		}
	}
	if n := len(srv.Requests()); n != 0 {
		t.Errorf("the fake received %d requests, want none (G1)", n)
	}
}

func TestExecutablePlainOutput(t *testing.T) {
	odd := "/movies/Odd/Odd \x1b[31mred\nsecond.en.srt"
	srv := fakebazarr.New(t, fakebazarr.Config{Library: fakebazarr.Library{Movies: []fakebazarr.Movie{
		{RadarrID: 1, Title: "Odd \x1b]0;title\x07", Subtitles: []fakebazarr.Subtitle{
			{Path: odd, Language: "en"}, {Path: "/movies/Odd/Odd.fr.srt", Language: "fr"},
		}},
	}}})
	r := runExecutable(t, srv.URL, srv.APIKey(), "list")
	if r.code != 0 || r.stderr != "" {
		t.Fatalf("code %d, stderr %q", r.code, r.stderr)
	}
	if strings.ContainsAny(r.stdout, "\x1b\x07\r") {
		t.Errorf("standard output through a pipe holds an escape or control character (G5):\n%q", r.stdout)
	}
	lines := strings.Split(strings.TrimSuffix(r.stdout, "\n"), "\n")
	// Two subtitle lines, a blank line, and two lines of totals.
	if len(lines) != 5 || !strings.Contains(lines[0], `Odd \x1b[31mred\x0asecond.en.srt`) {
		t.Errorf("want one line per subtitle, the control characters written as escapes, got:\n%s", r.stdout)
	}
}

// With Go's HTTP/2 debugging on, an HTTP/2 client logs every header it
// sends to standard error, the API key's among them (R2).
func TestHTTP2DebugKeepsKey(t *testing.T) {
	if runtime.GOOS == "darwin" || runtime.GOOS == "windows" {
		t.Skip("the executable trusts the fake's certificate through SSL_CERT_FILE, which only the Linux rule reads (S7)")
	}
	srv := fakebazarr.New(t, fakebazarr.Config{HTTPS: true, Library: fakebazarr.Library{Movies: []fakebazarr.Movie{
		{RadarrID: 1, Title: "Sintel", Subtitles: []fakebazarr.Subtitle{{Path: "/movies/Sintel/Sintel.en.srt", Language: "en"}}},
	}}})
	// The check shows nothing unless the fake offers HTTP/2.
	resp, err := srv.Client().Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.ProtoMajor != 2 {
		t.Fatalf("the HTTPS fake answered over %s, want HTTP/2 offered", resp.Proto)
	}
	certFile := filepath.Join(t.TempDir(), "fake.pem")
	if err := os.WriteFile(certFile, srv.CertificatePEM(), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd, stdout, stderr := command(srv.URL, srv.APIKey(), "list")
	cmd.Env = append(cmd.Env, "SSL_CERT_FILE="+certFile, "GODEBUG=http2debug=1")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	waitExit(t, cmd)
	if code := cmd.ProcessState.ExitCode(); code != 0 || !strings.Contains(stdout.String(), "Sintel.en.srt") {
		t.Fatalf("code %d, stdout %q, stderr %q; want the fake's subtitle listed", code, stdout, stderr)
	}
	if strings.Contains(stderr.String(), srv.APIKey()) {
		t.Errorf("standard error holds the API key (R2):\n%s", stderr)
	}
}
