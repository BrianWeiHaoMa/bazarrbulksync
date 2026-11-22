package main_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// The bed checks run the product where these settings say, and nowhere else:
// with neither, the executable TestMain builds, here.
//
//	BAZARRBULKSYNC_EXECUTABLE=release/out/bazarrbulksync-<os>-<arch>
//	BAZARRBULKSYNC_RELEASE=<the version the release build was given>
//	BAZARRBULKSYNC_SSH=<ssh host alias>   (optional: run it there)
//
// The executable is copied, here into a temporary folder, on an SSH host
// into a scratch folder in the user's home (remoteHost), which also holds
// every state file and home a check gives a run there; the folder, the
// runs, and the connection end with the test binary (closeHost). Each
// address a run is given that reaches a listener here is forwarded from the
// same port on the host's loopback, so a run there is given the very
// address a run here gets.
const (
	executableVar = "BAZARRBULKSYNC_EXECUTABLE"
	sshVar        = "BAZARRBULKSYNC_SSH"
)

// productHost is where the bed checks run the product.
type productHost struct {
	exe    string     // the executable, as a run where it is names it
	system string     // GOOS of the machine it runs on
	remote remoteHost // nil when the product runs here
	copied string     // the folder here holding the copy of a given executable
}

// runEnv is all of the environment a run gets: the two connection variables
// and, where a check puts the default state file under test, home, the
// folder P1 computes it from: HOME, or on Windows AppData. A run on Windows
// also gets what Windows needs (windowsRun).
type runEnv struct {
	url, key, home string
}

func (e runEnv) vars() []string {
	vars := []string{"BAZARR_URL=" + e.url, "BAZARR_API_KEY=" + e.key}
	if e.home != "" {
		vars = append(vars, "HOME="+e.home)
	}
	return vars
}

var (
	hostOnce sync.Once
	host     *productHost
	hostErr  error
)

// onHost returns where the bed checks run the product, setting it up on the
// first call.
func onHost(t *testing.T) *productHost {
	t.Helper()
	hostOnce.Do(func() { host, hostErr = setUpHost() })
	if hostErr != nil {
		t.Fatal(hostErr)
	}
	return host
}

// closeHost ends what setUpHost started; TestMain calls it.
func closeHost() error {
	if host == nil {
		return nil
	}
	return host.close()
}

func setUpHost() (*productHost, error) {
	given, alias, version := os.Getenv(executableVar), os.Getenv(sshVar), os.Getenv(releaseVar)
	if given == "" {
		if alias != "" {
			return nil, fmt.Errorf("%s is set without %s: name the release executable to run there", sshVar, executableVar)
		}
		return &productHost{exe: executable, system: runtime.GOOS}, nil
	}
	if version == "" {
		return nil, fmt.Errorf("set %s to the version `go run ./release <version>` was given, to check %s by its --version", releaseVar, given)
	}
	exe, err := filepath.Abs(given)
	if err != nil {
		return nil, err
	}
	sum, err := fileSHA256(exe)
	if err != nil {
		return nil, fmt.Errorf("%w; run `go run ./release %s`", err, version)
	}
	h := &productHost{system: runtime.GOOS}
	if alias != "" {
		if h.remote, err = dialSSH(alias); err != nil {
			return nil, err
		}
		h.exe, h.system, err = h.remote.install(exe, sum)
	} else {
		h.exe, err = h.install(exe, sum)
	}
	if err != nil {
		return nil, errors.Join(err, h.close())
	}
	// What runs is the release build's own file: its version, not the
	// versionless build TestMain makes.
	r, err := h.runToEnd(runEnv{}, "", []string{"--version"}, time.Minute)
	if err == nil && (r.code != 0 || r.stdout != "bazarrbulksync "+version+"\n") {
		err = fmt.Errorf("%s --version: code %d, stdout %q, stderr %q; want bazarrbulksync %s", given, r.code, r.stdout, r.stderr, version)
	}
	if err != nil {
		return nil, errors.Join(err, h.close())
	}
	fmt.Fprintf(os.Stderr, "bed checks run %s (SHA-256 %s, version %s) on %s\n", given, sum, version, h.where())
	return h, nil
}

// install copies the executable into a temporary folder here unchanged, so
// that a later build at its path cannot change what the checks run.
func (h *productHost) install(exe, sum string) (string, error) {
	data, err := os.ReadFile(exe)
	if err != nil {
		return "", err
	}
	if h.copied, err = os.MkdirTemp("", "bazarrbulksync-release-"); err != nil {
		return "", err
	}
	path := filepath.Join(h.copied, filepath.Base(exe))
	if err := os.WriteFile(path, data, 0o755); err != nil {
		return "", err
	}
	if got, err := fileSHA256(path); err != nil || got != sum {
		return "", fmt.Errorf("the copy of %s reads SHA-256 %s (%v); want %s", exe, got, err, sum)
	}
	return path, nil
}

func (h *productHost) close() error {
	var err error
	if h.remote != nil {
		err = h.remote.close()
	}
	if h.copied != "" {
		err = errors.Join(err, os.RemoveAll(h.copied))
	}
	return err
}

func (h *productHost) where() string {
	if h.remote == nil {
		return "this machine (" + h.system + ")"
	}
	return h.remote.name() + " (" + h.system + ")"
}

func fileSHA256(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// productRun is one run of the product, started by start.
type productRun struct {
	stdout    io.Reader // standard output, as it comes
	wait      func() (result, error)
	interrupt func() error // what Ctrl-C delivers to it
	kill      func()       // ends a run that hangs
}

// start starts the product with args and exactly env. Its standard input is
// not a terminal.
func (h *productHost) start(env runEnv, args []string) (*productRun, error) {
	start := startHere
	if h.remote != nil {
		start = h.remote.start
	}
	run, err := start(h.exe, env, args)
	if err != nil {
		return nil, hideKey(err, env.key)
	}
	wait := run.wait
	run.wait = func() (result, error) {
		r, err := wait()
		return r, hideKey(err, env.key)
	}
	return run, nil
}

// hideKey keeps the key out of an error a check prints (R2).
func hideKey(err error, key string) error {
	if err == nil || key == "" || !strings.Contains(err.Error(), key) {
		return err
	}
	return errors.New(strings.ReplaceAll(err.Error(), key, "<the API key>"))
}

func startHere(exe string, env runEnv, args []string) (*productRun, error) {
	cmd := exec.Command(exe, args...)
	cmd.Env = env.vars()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &productRun{
		stdout:    out,
		interrupt: func() error { return cmd.Process.Signal(os.Interrupt) },
		kill:      func() { _ = cmd.Process.Kill() },
		wait: func() (result, error) {
			// The pipe is read to its end before Wait closes it.
			rest, _ := io.ReadAll(out)
			err := cmd.Wait()
			var exit *exec.ExitError
			if err != nil && !errors.As(err, &exit) {
				return result{}, err
			}
			return result{code: cmd.ProcessState.ExitCode(), stdout: string(rest), stderr: stderr.String()}, nil
		},
	}, nil
}

// runToEnd runs the product to its end, typing typed into a terminal as its
// standard input when typed is not empty, killing it after deadline.
func (h *productHost) runToEnd(env runEnv, typed string, args []string, deadline time.Duration) (result, error) {
	if typed != "" {
		atTerminal := runAtTerminalHere
		if h.remote != nil {
			atTerminal = h.remote.atTerminal
		}
		r, err := atTerminal(h.exe, env, typed, args, deadline)
		return r, hideKey(err, env.key)
	}
	run, err := h.start(env, args)
	if err != nil {
		return result{}, err
	}
	timer := time.AfterFunc(deadline, run.kill)
	defer timer.Stop()
	return run.wait()
}

// runFromNull runs the product to its end with its standard input
// redirected from the system's null device, a character device that is not
// a terminal.
func (h *productHost) runFromNull(env runEnv, args []string, deadline time.Duration) (result, error) {
	if h.remote == nil {
		// startHere leaves standard input unset, which os/exec opens from
		// the null device.
		return h.runToEnd(env, "", args, deadline)
	}
	r, err := h.remote.fromNull(h.exe, env, args, deadline)
	return r, hideKey(err, env.key)
}

// bedRun runs the product to its end with url and key, as runExecutable
// runs the executable TestMain builds.
func bedRun(t *testing.T, url, key string, args ...string) result {
	t.Helper()
	return runProduct(t, runEnv{url: url, key: key}, "", args)
}

// runProduct runs the product to its end with env, at a terminal typed into
// when typed is not empty. The deadline only turns a hang into a failure.
func runProduct(t *testing.T, env runEnv, typed string, args []string) result {
	t.Helper()
	r, err := onHost(t).runToEnd(env, typed, args, time.Minute)
	if err != nil {
		t.Fatalf("%q: %v", args, err)
	}
	return r
}

// stateFile returns a path for a state file where the product runs, in a
// folder of its own that exists, with no file there yet.
func (h *productHost) stateFile(t *testing.T) string {
	t.Helper()
	return h.join(h.folder(t), "state.json")
}

// folder returns a new empty folder where the product runs.
func (h *productHost) folder(t *testing.T) string {
	t.Helper()
	if h.remote == nil {
		return t.TempDir()
	}
	dir, err := h.remote.folder()
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

// join joins path elements as the system where the product runs does.
func (h *productHost) join(elem ...string) string {
	if h.remote == nil {
		return filepath.Join(elem...)
	}
	return h.remote.join(elem...)
}

// readFile reads a file where the product runs; a missing file is
// fs.ErrNotExist.
func (h *productHost) readFile(path string) ([]byte, error) {
	if h.remote == nil {
		return os.ReadFile(path)
	}
	return h.remote.readFile(path)
}

// exists reports whether anything, a dangling link included, is at path
// where the product runs.
func (h *productHost) exists(path string) (bool, error) {
	if h.remote == nil {
		_, err := os.Lstat(path)
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return err == nil, err
	}
	return h.remote.exists(path)
}
