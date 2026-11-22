package main_test

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// windowsHost is a Windows machine whose user's shell is cmd.exe. Nothing
// the product is given passes through cmd.exe: this test binary, built for
// Windows and copied beside the executable as checks.exe, is run there with
// one request (windowsRequest, carried as one URL-safe base64 word) and
// does what testbed_winhelper_windows_test.go says: it starts the product
// with exactly the arguments and environment asked, delivers Ctrl-C, and
// reads files.
type windowsHost struct {
	*sshConn
	scratch string // the scratch folder there, absolute
	rel     string // the same, from the user's home, where cmd.exe starts
	made    int    // folders made in scratch, counted under sshConn.mu
}

// windowsHelperWord, as the first argument of the test binary on Windows,
// makes it act on the request in the second instead of running tests.
const windowsHelperWord = "bazarrbulksync-windows"

// windowsHelperFailed is the helper's exit code when it could not do what
// was asked; the product exits 0, 1, or 2.
const windowsHelperFailed = 125

// windowsRequest is one thing the helper does on Windows.
type windowsRequest struct {
	// Op is run, interrupt, alive, kill, mkdir, read, exists, or killAll.
	Op string
	// Exe is the program a run starts, and the image a process of the
	// run is known by.
	Exe  string
	Args []string
	// Env is the run's environment beyond SystemRoot and PATH, which the
	// helper adds.
	Env map[string]string
	// Dir is the run's own folder, made by the run, holding its pid file.
	Dir string
	// Out and Err, when set, are files for the run's standard output and
	// error.
	Out, Err string
	Path     string // what mkdir, read, and exists look at
}

func dialWindows(c *sshConn) (*windowsHost, error) {
	w := &windowsHost{sshConn: c}
	// cmd.exe starts in the user's home, and `cd` prints it.
	home, err := w.shell("cd", nil)
	if err != nil {
		return nil, errors.Join(err, c.closeConn())
	}
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	w.rel = "bazarrbulksync-checks." + hex.EncodeToString(b)
	w.scratch = strings.TrimSpace(home) + `\` + w.rel
	if _, err := w.shell("mkdir "+w.rel, nil); err != nil {
		return nil, errors.Join(fmt.Errorf("making the scratch folder on %s: %w", c.alias, err), c.closeConn())
	}
	return w, nil
}

func (w *windowsHost) join(elem ...string) string { return strings.Join(elem, `\`) }

// install copies the executable and the helper into the scratch folder,
// checks the executable's SHA-256 there with the system's own certutil, and
// checks that a run's environment finds no Go while the user's does: Go is
// removed from PATH for the runs only (the intent).
func (w *windowsHost) install(exe, sum string) (path, system string, err error) {
	arch, err := w.shell("echo %PROCESSOR_ARCHITECTURE%", nil)
	if err != nil {
		return "", "", err
	}
	goarch := map[string]string{"AMD64": "amd64", "ARM64": "arm64"}[strings.TrimSpace(arch)]
	if goarch == "" {
		return "", "", fmt.Errorf("%s runs on %q, which no release executable is for", w.alias, arch)
	}
	local, err := os.MkdirTemp("", "bazarrbulksync-windows-helper-")
	if err != nil {
		return "", "", err
	}
	defer os.RemoveAll(local)
	helper := filepath.Join(local, "checks.exe")
	build := exec.Command("go", "test", "-c", "-o", helper, ".")
	build.Env = append(os.Environ(), "GOOS=windows", "GOARCH="+goarch, "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		return "", "", fmt.Errorf("building the Windows helper: %v\n%s", err, out)
	}
	for from, to := range map[string]string{exe: "bazarrbulksync.exe", helper: "checks.exe"} {
		if out, err := exec.Command("scp", "-q", "-o", "ControlPath="+w.control, "-o", "BatchMode=yes", from, w.alias+":"+w.rel+"/"+to).CombinedOutput(); err != nil {
			return "", "", fmt.Errorf("copying %s to %s: %v: %s", from, w.alias, err, out)
		}
	}
	path = w.scratch + `\bazarrbulksync.exe`
	out, err := w.shell("certutil -hashfile "+w.rel+`\bazarrbulksync.exe SHA256`, nil)
	if err != nil {
		return "", "", err
	}
	if lines := strings.Split(out, "\n"); len(lines) < 2 || strings.ToLower(strings.ReplaceAll(strings.TrimSpace(lines[1]), " ", "")) != sum {
		return "", "", fmt.Errorf("on %s the copy of %s reads %q; want SHA-256 %s", w.alias, exe, out, sum)
	}
	// F2 with the intent's "Go removed from PATH": the user's own shell
	// finds Go, and the very environment a run gets does not.
	theirs, err := w.shell("where go.exe", nil)
	if err != nil {
		return "", "", fmt.Errorf("on %s the user's shell finds no Go (%v), so removing it shows nothing", w.alias, err)
	}
	// The lookup names go.exe: a run's environment has no PATHEXT, which
	// would turn a bare "go" into a name no file has, found nowhere.
	where := `C:\Windows\System32\where.exe`
	r, err := w.runToEnd(windowsRequest{Op: "run", Exe: where, Args: []string{"go.exe"}, Dir: w.newFolder()}, nil, time.Minute)
	if err != nil || r.code != 1 || r.stdout != "" {
		return "", "", fmt.Errorf("on %s, `where go.exe` in a run's environment: code %d, stdout %q, stderr %q (%v); want Go not found", w.alias, r.code, r.stdout, r.stderr, err)
	}
	fmt.Fprintf(os.Stderr, "on %s the user's `where go.exe` finds %s; a run's finds none (exit %d: %s)\n",
		w.alias, strings.TrimSpace(theirs), r.code, strings.TrimSpace(r.stderr))
	return path, "windows", nil
}

// newFolder names a new folder in the scratch folder; its name holds a
// space, so every state path a check gives a run there holds backslashes
// and a space.
func (w *windowsHost) newFolder() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.made++
	return fmt.Sprintf(`%s\run %d`, w.scratch, w.made)
}

func (w *windowsHost) folder() (string, error) {
	dir := w.newFolder()
	_, err := w.ask(windowsRequest{Op: "mkdir", Path: dir})
	return dir, err
}

// helper prepares ssh running the helper there with req.
func (w *windowsHost) helper(req windowsRequest, extra ...string) *exec.Cmd {
	data, err := json.Marshal(req)
	if err != nil {
		panic(err)
	}
	return w.command(w.rel+`\checks.exe `+windowsHelperWord+" "+base64.RawURLEncoding.EncodeToString(data), extra...)
}

// ask has the helper do req to its end and returns its standard output; a
// failure of the helper is an error naming its standard error.
func (w *windowsHost) ask(req windowsRequest) (string, error) {
	cmd := w.helper(req)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if err != nil {
		err = fmt.Errorf("%s on %s: %w: %s", req.Op, w.alias, err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), err
}

func (w *windowsHost) readFile(path string) ([]byte, error) {
	out, err := w.ask(windowsRequest{Op: "read", Path: path})
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 3 {
		return nil, fmt.Errorf("%s on %s: %w", path, w.alias, fs.ErrNotExist)
	}
	return []byte(out), err
}

func (w *windowsHost) exists(path string) (bool, error) {
	_, err := w.ask(windowsRequest{Op: "exists", Path: path})
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 {
		return false, nil
	}
	return err == nil, err
}

// runRequest is the request that runs exe with args and env in a new
// folder; the address is forwarded first, as on every host.
func (w *windowsHost) runRequest(exe string, env runEnv, args []string) (windowsRequest, error) {
	if err := w.forward(env.url); err != nil {
		return windowsRequest{}, err
	}
	vars := map[string]string{"BAZARR_URL": env.url, "BAZARR_API_KEY": env.key}
	if env.home != "" {
		vars["AppData"] = env.home // P1's folder on Windows
	}
	return windowsRequest{Op: "run", Exe: exe, Args: args, Env: vars, Dir: w.newFolder()}, nil
}

func (w *windowsHost) start(exe string, env runEnv, args []string) (*productRun, error) {
	req, err := w.runRequest(exe, env, args)
	if err != nil {
		return nil, err
	}
	return w.startRequest(req, nil)
}

// startRequest starts the helper with req; with redirect, cmd.exe gives the
// helper, and so the run, that standard input.
func (w *windowsHost) startRequest(req windowsRequest, redirect []string) (*productRun, error) {
	cmd := w.helper(req)
	if redirect != nil {
		cmd.Args[len(cmd.Args)-1] += " " + strings.Join(redirect, " ")
	}
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
		interrupt: func() error { _, err := w.ask(windowsRequest{Op: "interrupt", Exe: req.Exe, Dir: req.Dir}); return err },
		kill: func() {
			_, _ = w.ask(windowsRequest{Op: "kill", Exe: req.Exe, Dir: req.Dir})
			_ = cmd.Process.Kill()
		},
		wait: func() (result, error) {
			rest, _ := io.ReadAll(out)
			err := cmd.Wait()
			r := result{stdout: string(rest), stderr: stderr.String()}
			r.code, err = w.ended(req, cmd, err, r.stderr)
			return r, err
		},
	}, nil
}

// runToEnd runs req to its end, standard input redirected as redirect says.
func (w *windowsHost) runToEnd(req windowsRequest, redirect []string, deadline time.Duration) (result, error) {
	run, err := w.startRequest(req, redirect)
	if err != nil {
		return result{}, err
	}
	timer := time.AfterFunc(deadline, run.kill)
	defer timer.Stop()
	return run.wait()
}

// fromNull runs exe with its standard input redirected from NUL by cmd.exe.
func (w *windowsHost) fromNull(exe string, env runEnv, args []string, deadline time.Duration) (result, error) {
	req, err := w.runRequest(exe, env, args)
	if err != nil {
		return result{}, err
	}
	return w.runToEnd(req, []string{"<", "NUL"}, deadline)
}

// atTerminal runs exe with the console of an SSH terminal session as its
// standard input, typed into it; its standard output and error go to files
// in its folder, read afterwards, so that what a check compares never
// passed through the console, which re-renders what it shows.
func (w *windowsHost) atTerminal(exe string, env runEnv, typed string, args []string, deadline time.Duration) (result, error) {
	req, err := w.runRequest(exe, env, args)
	if err != nil {
		return result{}, err
	}
	req.Out, req.Err = req.Dir+`\out`, req.Dir+`\err`
	cmd := w.helper(req, "-tt")
	var sshErr bytes.Buffer
	cmd.Stdout, cmd.Stderr = io.Discard, &sshErr // the console's screen; ssh's own messages
	in, err := cmd.StdinPipe()
	if err != nil {
		return result{}, err
	}
	if err := cmd.Start(); err != nil {
		return result{}, err
	}
	// A terminal's Enter key sends a carriage return; the console turns it
	// into the end of a line.
	_, _ = io.WriteString(in, strings.ReplaceAll(typed, "\n", "\r"))
	timer := time.AfterFunc(deadline, func() {
		_, _ = w.ask(windowsRequest{Op: "kill", Exe: req.Exe, Dir: req.Dir})
		_ = cmd.Process.Kill()
	})
	defer timer.Stop()
	err = cmd.Wait()
	var r result
	if r.code, err = w.ended(req, cmd, err, sshErr.String()); err != nil {
		return r, err
	}
	var code string
	for name, into := range map[string]*string{req.Out: &r.stdout, req.Err: &r.stderr, req.Dir + `\code`: &code} {
		data, err := w.readFile(name)
		if err != nil {
			return r, err
		}
		*into = string(data)
	}
	// sshd reports 0 for a session with a terminal, whatever the run's
	// code; the helper leaves the code in the run's folder.
	if r.code, err = strconv.Atoi(code); err == nil && (r.code < 0 || r.code > 2) {
		err = fmt.Errorf("the run on %s ended with %d: the helper failed, or killed it", w.alias, r.code)
	}
	return r, err
}

// ended returns the run's exit code from the ssh command that carried it,
// and checks that no process of the run is left there.
func (w *windowsHost) ended(req windowsRequest, cmd *exec.Cmd, err error, stderr string) (int, error) {
	var exit *exec.ExitError
	if err != nil && !errors.As(err, &exit) {
		return 0, err
	}
	// The product exits 0, 1, or 2; anything else is ssh's failure (255),
	// the helper's or a run it killed (windowsHelperFailed), or ssh killed.
	code := cmd.ProcessState.ExitCode()
	if code == windowsHelperFailed {
		return code, fmt.Errorf("the helper on %s failed, or killed the run: %s", w.alias, strings.TrimSpace(stderr))
	}
	if code < 0 || code > 2 {
		return code, fmt.Errorf("ssh %s exited %d (the connection failed, or ssh was killed): %s", w.alias, code, strings.TrimSpace(stderr))
	}
	if _, err := w.ask(windowsRequest{Op: "alive", Exe: req.Exe, Dir: req.Dir}); err != nil {
		return code, fmt.Errorf("a process of the run may still be there after its end: %w", err)
	}
	return code, nil
}

// close ends every run left in the scratch folder, removes the folder, and
// ends the connection; a folder left there is an error.
func (w *windowsHost) close() error {
	var errs []error
	if _, err := w.ask(windowsRequest{Op: "killAll", Exe: w.scratch + `\bazarrbulksync.exe`, Path: w.scratch}); err != nil {
		errs = append(errs, err)
	}
	// The helper is not running now, so its file can be removed too.
	if _, err := w.shell("rmdir /s /q "+w.rel+" && if exist "+w.rel+" exit 1", nil); err != nil {
		errs = append(errs, fmt.Errorf("removing %s on %s: %w", w.scratch, w.alias, err))
	}
	return errors.Join(append(errs, w.closeConn())...)
}
