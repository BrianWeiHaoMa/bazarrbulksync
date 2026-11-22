package main_test

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// sshHost is a machine reached through one SSH control connection, which
// carries every run, copy, look, and forward.
type sshHost struct {
	alias   string
	control string // the control socket's path here
	master  *exec.Cmd
	scratch string // the scratch folder there
	mu      sync.Mutex
	made    int          // folders made in scratch
	ports   map[int]bool // ports forwarded from there to here
}

func dialSSH(alias string) (*sshHost, error) {
	local, err := os.MkdirTemp("", "bazarrbulksync-ssh-")
	if err != nil {
		return nil, err
	}
	s := &sshHost{alias: alias, control: filepath.Join(local, "control"), ports: map[int]bool{}}
	s.master = exec.Command("ssh", "-N", "-o", "ControlMaster=yes", "-o", "ControlPersist=no", "-o", "ControlPath="+s.control,
		"-o", "BatchMode=yes", "-o", "ExitOnForwardFailure=yes", "-o", "ServerAliveInterval=15", alias)
	var masterErr bytes.Buffer
	s.master.Stderr = &masterErr
	if err := s.master.Start(); err != nil {
		os.RemoveAll(local)
		return nil, err
	}
	for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(100 * time.Millisecond) {
		if exec.Command("ssh", "-o", "ControlPath="+s.control, "-O", "check", alias).Run() == nil {
			break
		}
		if time.Now().After(deadline) {
			_ = s.master.Process.Kill()
			_ = s.master.Wait()
			os.RemoveAll(local)
			return nil, fmt.Errorf("ssh %s: no connection within 30s: %s", alias, masterErr.String())
		}
	}
	out, err := s.shell(`mktemp -d "$HOME/bazarrbulksync-checks.XXXXXX"`, nil)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("making the scratch folder on %s: %w", alias, err), s.close())
	}
	s.scratch = strings.TrimSpace(out)
	return s, nil
}

// close ends every run left in the scratch folder, removes the folder, and
// ends the connection; a folder left there is an error.
func (s *sshHost) close() error {
	var errs []error
	if s.scratch != "" {
		script := `for p in ` + shQuote(s.scratch) + `/*/pid; do test -f "$p" && ` + s.runAlive(`"$p"`, `kill -KILL "$pid"`) + `; done; ` +
			`rm -rf ` + shQuote(s.scratch) + ` && test ! -e ` + shQuote(s.scratch)
		if _, err := s.shell("/bin/sh -c "+shQuote(script), nil); err != nil {
			errs = append(errs, fmt.Errorf("removing %s on %s: %w", s.scratch, s.alias, err))
		}
	}
	_ = exec.Command("ssh", "-o", "ControlPath="+s.control, "-O", "exit", s.alias).Run()
	done := make(chan struct{})
	go func() { _ = s.master.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		_ = s.master.Process.Kill()
		<-done
	}
	os.RemoveAll(filepath.Dir(s.control))
	return errors.Join(errs...)
}

// runAlive is shell text that runs then, with $pid set, when the run whose
// process ID is in the file pidFile is still there: its process ID names a
// process whose command holds the scratch folder, so that a reused ID is
// never taken for it.
func (s *sshHost) runAlive(pidFile, then string) string {
	return `{ pid=$(cat ` + pidFile + `); case "$(ps -p "$pid" -o command= 2>/dev/null)" in *` + shQuote(s.scratch+"/") + `*) ` + then + `;; esac; }`
}

// command prepares ssh running script with the user's shell there.
func (s *sshHost) command(script string, extra ...string) *exec.Cmd {
	args := append([]string{"-o", "ControlPath=" + s.control, "-o", "BatchMode=yes"}, extra...)
	return exec.Command("ssh", append(args, s.alias, script)...)
}

// shell runs script there to its end, with in as its standard input, and
// returns its standard output; a nonzero exit is an error naming its
// standard error.
func (s *sshHost) shell(script string, in io.Reader) (string, error) {
	cmd := s.command(script)
	cmd.Stdin = in
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return stdout.String(), fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

// install copies the executable into the scratch folder unchanged, and
// checks there that it is the file given and that nothing finds Go.
func (s *sshHost) install(exe, sum string) (path, system string, err error) {
	file, err := os.Open(exe)
	if err != nil {
		return "", "", err
	}
	defer file.Close()
	path = s.scratch + "/bazarrbulksync"
	if _, err := s.shell("cat > "+shQuote(path)+" && chmod 755 "+shQuote(path), file); err != nil {
		return "", "", fmt.Errorf("copying %s to %s: %w", exe, s.alias, err)
	}
	out, err := s.shell("uname -s; shasum -a 256 "+shQuote(path), nil)
	if err != nil {
		return "", "", err
	}
	fields := strings.Fields(out)
	if len(fields) < 2 || fields[1] != sum {
		return "", "", fmt.Errorf("on %s the copy of %s reads %q; want SHA-256 %s", s.alias, exe, out, sum)
	}
	system = strings.ToLower(fields[0])
	// F2: no runtime installed, as the user's shell there and the run's own
	// empty environment look for one.
	if out, err := s.shell(`"$SHELL" -l -c 'command -v go'; env -i /bin/sh -c 'command -v go'; true`, nil); err != nil || strings.TrimSpace(out) != "" {
		return "", "", fmt.Errorf("on %s, go is found (%q, %v); want a machine with no Go installed", s.alias, out, err)
	}
	return path, system, nil
}

// folder makes a new empty folder in the scratch folder.
func (s *sshHost) folder() (string, error) {
	s.mu.Lock()
	s.made++
	dir := fmt.Sprintf("%s/%d", s.scratch, s.made)
	s.mu.Unlock()
	_, err := s.shell("mkdir "+shQuote(dir), nil)
	return dir, err
}

func (s *sshHost) readFile(path string) ([]byte, error) {
	out, err := s.shell("if test -e "+shQuote(path)+" || test -L "+shQuote(path)+"; then cat "+shQuote(path)+"; else exit 3; fi", nil)
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 3 {
		return nil, fmt.Errorf("%s on %s: %w", path, s.alias, fs.ErrNotExist)
	}
	return []byte(out), err
}

func (s *sshHost) exists(path string) (bool, error) {
	// A folder that cannot be searched is an error, as it is to Lstat, never
	// an absence.
	dir, base := path[:strings.LastIndex(path, "/")+1], path[strings.LastIndex(path, "/")+1:]
	_, err := s.shell("test -d "+shQuote(dir)+" || exit 1; cd "+shQuote(dir)+" || exit 2; test -e "+shQuote(base)+" || test -L "+shQuote(base), nil)
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 {
		return false, nil
	}
	return err == nil, err
}

// forward makes the address in rawURL, when it reaches a listener here,
// reach the same listener from the same port there. An address nothing
// listens on here is left alone, so it reaches nothing there either.
func (s *sshHost) forward(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return nil // the product names what it was given
	}
	port := u.Port()
	if port == "" {
		port = map[string]string{"http": "80", "https": "443"}[u.Scheme]
	}
	n, err := strconv.Atoi(port)
	if err != nil || !isLoopback(u.Hostname()) {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ports[n] {
		return nil
	}
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(u.Hostname(), port), time.Second)
	if err != nil {
		return nil
	}
	conn.Close()
	spec := fmt.Sprintf("%d:%s:%d", n, u.Hostname(), n)
	if out, err := exec.Command("ssh", "-o", "ControlPath="+s.control, "-O", "forward", "-R", spec, s.alias).CombinedOutput(); err != nil {
		return fmt.Errorf("forwarding port %d from %s: %v: %s", n, s.alias, err, out)
	}
	s.ports[n] = true
	return nil
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// runScript is the script that runs exe with args and exactly env, the
// run's process ID first written into a new folder, so that the run can be
// interrupted and looked for. Every value is quoted, so the user's shell
// there hands each argument over as given.
func (s *sshHost) runScript(exe string, env runEnv, args []string) (dir, script string, err error) {
	if err := s.forward(env.url); err != nil {
		return "", "", err
	}
	if dir, err = s.folder(); err != nil {
		return "", "", err
	}
	words := []string{"exec", "env", "-i"}
	for _, v := range env.vars() {
		words = append(words, shQuote(v))
	}
	words = append(words, shQuote(exe))
	for _, a := range args {
		words = append(words, shQuote(a))
	}
	return dir, "echo $$ > " + shQuote(dir+"/pid") + " && " + strings.Join(words, " "), nil
}

func (s *sshHost) start(exe string, env runEnv, args []string) (*productRun, error) {
	dir, script, err := s.runScript(exe, env, args)
	if err != nil {
		return nil, err
	}
	cmd := s.command(script)
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
		interrupt: func() error { return s.signal(dir, "INT") },
		kill: func() {
			_ = s.signal(dir, "KILL")
			_ = cmd.Process.Kill()
		},
		wait: func() (result, error) {
			rest, _ := io.ReadAll(out)
			err := cmd.Wait()
			r := result{stdout: string(rest), stderr: stderr.String()}
			r.code, err = s.ended(dir, cmd, err, r.stderr)
			return r, err
		},
	}, nil
}

// atTerminal runs exe with a terminal there as its standard input, typed
// into it; its standard output and error go to files beside it, read
// afterwards, since ssh carries a terminal's output as one stream.
func (s *sshHost) atTerminal(exe string, env runEnv, typed string, args []string, deadline time.Duration) (result, error) {
	dir, script, err := s.runScript(exe, env, args)
	if err != nil {
		return result{}, err
	}
	script += " > " + shQuote(dir+"/out") + " 2> " + shQuote(dir+"/err")
	cmd := s.command(script, "-tt")
	var sshErr bytes.Buffer
	cmd.Stdout, cmd.Stderr = io.Discard, &sshErr // the terminal's echo; ssh's own messages
	in, err := cmd.StdinPipe()
	if err != nil {
		return result{}, err
	}
	if err := cmd.Start(); err != nil {
		return result{}, err
	}
	// The terminal holds what is typed until the run reads it; the input
	// stays open until the run ends, as a keyboard does.
	_, _ = io.WriteString(in, typed)
	timer := time.AfterFunc(deadline, func() {
		_ = s.signal(dir, "KILL")
		_ = cmd.Process.Kill()
	})
	defer timer.Stop()
	err = cmd.Wait()
	var r result
	if r.code, err = s.ended(dir, cmd, err, sshErr.String()); err != nil {
		return r, err
	}
	for name, into := range map[string]*string{"out": &r.stdout, "err": &r.stderr} {
		data, err := s.readFile(dir + "/" + name)
		if err != nil {
			return r, err
		}
		*into = string(data)
	}
	// What the run printed is left in no file there.
	_, err = s.shell("rm "+shQuote(dir+"/out")+" "+shQuote(dir+"/err"), nil)
	return r, err
}

// signal sends the run in dir the signal named sig.
func (s *sshHost) signal(dir, sig string) error {
	_, err := s.shell("kill -"+sig+` "$(cat `+shQuote(dir+"/pid")+`)"`, nil)
	return err
}

// ended returns the run's exit code from the ssh command that carried the
// run in dir, and checks that no process of the run is left there.
func (s *sshHost) ended(dir string, cmd *exec.Cmd, err error, sshErr string) (int, error) {
	var exit *exec.ExitError
	if err != nil && !errors.As(err, &exit) {
		return 0, err
	}
	code := cmd.ProcessState.ExitCode()
	// The product exits 0, 1, or 2; ssh reports its own failure, or a run
	// ended by a signal, as 255.
	if code == 255 {
		return code, fmt.Errorf("ssh %s exited 255 (the connection failed, or the run was killed): %s", s.alias, strings.TrimSpace(sshErr))
	}
	if _, err := s.shell("/bin/sh -c "+shQuote(s.runAlive(shQuote(dir+"/pid"), "exit 1")), nil); err != nil {
		return code, fmt.Errorf("a process of the run is still there after its end on %s: %w", s.alias, err)
	}
	return code, nil
}

// shQuote quotes s as one word for a POSIX shell or zsh.
func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
