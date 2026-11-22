package main_test

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"unsafe"
)

// The Windows side of windowsHost (testbed_sshwindows_test.go): run as
// `checks.exe bazarrbulksync-windows <request>`, this test binary does the
// one request and exits from init, before TestMain builds anything.
func init() {
	if len(os.Args) == 3 && os.Args[1] == windowsHelperWord {
		os.Exit(windowsHelper(os.Args[2]))
	}
}

var (
	kernel32                  = syscall.NewLazyDLL("kernel32.dll")
	procSetConsoleCtrlHandler = kernel32.NewProc("SetConsoleCtrlHandler")
	procFreeConsole           = kernel32.NewProc("FreeConsole")
	procAttachConsole         = kernel32.NewProc("AttachConsole")
	procGenerateCtrlEvent     = kernel32.NewProc("GenerateConsoleCtrlEvent")
	procQueryImageName        = kernel32.NewProc("QueryFullProcessImageNameW")
)

func windowsHelper(word string) int {
	var req windowsRequest
	data, err := base64.RawURLEncoding.DecodeString(word)
	if err == nil {
		err = json.Unmarshal(data, &req)
	}
	code := 0
	if err == nil {
		code, err = windowsDo(req)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return windowsHelperFailed
	}
	return code
}

func windowsDo(req windowsRequest) (int, error) {
	switch req.Op {
	case "run":
		return windowsRun(req)
	case "interrupt":
		return 0, windowsInterrupt(req)
	case "alive":
		pid, alive, err := runAlive(req)
		if err == nil && alive {
			return 1, fmt.Errorf("process %d of %s is still running", pid, req.Exe)
		}
		return 0, err
	case "kill":
		return 0, killRun(req)
	case "killAll":
		dirs, err := filepath.Glob(filepath.Join(req.Path, "*", "pid"))
		for _, pid := range dirs {
			err = errors.Join(err, killRun(windowsRequest{Exe: req.Exe, Dir: filepath.Dir(pid)}))
		}
		return 0, err
	case "mkdir":
		return 0, os.Mkdir(req.Path, 0o755)
	case "read":
		data, err := os.ReadFile(req.Path)
		if errors.Is(err, fs.ErrNotExist) {
			return 3, nil
		}
		if err == nil {
			_, err = os.Stdout.Write(data)
		}
		return 0, err
	case "exists":
		_, err := os.Lstat(req.Path)
		if errors.Is(err, fs.ErrNotExist) {
			return 1, nil
		}
		return 0, err
	}
	return 0, fmt.Errorf("no such request %q", req.Op)
}

// windowsRun runs the program and returns its exit code. Its environment
// is exactly the request's, plus SystemRoot, without which Windows cannot
// look up a name, and the user's PATH with every folder holding go.exe
// left out: Go removed from PATH for the run only (the intent). It is
// given the arguments as the request holds them, quoted by Go as Windows
// parses a command line, and this process's standard input.
func windowsRun(req windowsRequest) (int, error) {
	if err := os.MkdirAll(req.Dir, 0o755); err != nil {
		return 0, err
	}
	var path []string
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if _, err := os.Stat(filepath.Join(dir, "go.exe")); err != nil {
			path = append(path, dir)
		}
	}
	env := []string{"SystemRoot=" + os.Getenv("SystemRoot"), "PATH=" + strings.Join(path, ";")}
	for k, v := range req.Env {
		env = append(env, k+"="+v)
	}
	sort.Strings(env)
	cmd := exec.Command(req.Exe, req.Args...)
	cmd.Env = env
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if req.Out != "" {
		out, err := os.Create(req.Out)
		if err != nil {
			return 0, err
		}
		defer out.Close()
		cmd.Stdout = out
	}
	if req.Err != "" {
		errFile, err := os.Create(req.Err)
		if err != nil {
			return 0, err
		}
		defer errFile.Close()
		cmd.Stderr = errFile
	}
	// sshd starts a session without a terminal with Ctrl-C ignored, which
	// every process it starts inherits; a console a user types into does
	// not, so the run is started with Ctrl-C handled as there. This process
	// outlives a Ctrl-C itself, to hand on the run's own exit code.
	signal.Notify(make(chan os.Signal, 1), os.Interrupt)
	if r, _, err := procSetConsoleCtrlHandler.Call(0, 0); r == 0 {
		return 0, fmt.Errorf("SetConsoleCtrlHandler: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	pidFile := filepath.Join(req.Dir, "pid")
	if err := os.WriteFile(pidFile+".tmp", []byte(strconv.Itoa(cmd.Process.Pid)), 0o644); err != nil {
		_ = cmd.Process.Kill()
		return 0, err
	}
	if err := os.Rename(pidFile+".tmp", pidFile); err != nil {
		_ = cmd.Process.Kill()
		return 0, err
	}
	err := cmd.Wait()
	var exit *exec.ExitError
	if err != nil && !errors.As(err, &exit) {
		return 0, err
	}
	// sshd reports 0 for every session with a terminal, so the code is
	// also left in the run's folder.
	code := cmd.ProcessState.ExitCode()
	return code, os.WriteFile(filepath.Join(req.Dir, "code"), []byte(strconv.Itoa(code)), 0o644)
}

// runAlive reports whether the run whose pid file is in req.Dir is still
// running: its process ID names a running process whose image is req.Exe,
// so that a reused ID is never taken for it.
func runAlive(req windowsRequest) (int, bool, error) {
	data, err := os.ReadFile(filepath.Join(req.Dir, "pid"))
	if errors.Is(err, fs.ErrNotExist) {
		return 0, false, nil // the run never started
	}
	if err != nil {
		return 0, false, err
	}
	pid, err := strconv.Atoi(string(data))
	if err != nil {
		return 0, false, err
	}
	const queryLimited, stillActive = 0x1000, 259
	h, err := syscall.OpenProcess(queryLimited, false, uint32(pid))
	if err == syscall.Errno(87) { // ERROR_INVALID_PARAMETER
		return pid, false, nil // no such process
	}
	if err != nil {
		return pid, false, fmt.Errorf("looking at process %d: %w", pid, err)
	}
	defer syscall.CloseHandle(h)
	var code uint32
	if err := syscall.GetExitCodeProcess(h, &code); err != nil || code != stillActive {
		return pid, false, err
	}
	name := make([]uint16, syscall.MAX_LONG_PATH)
	size := uint32(len(name))
	if r, _, err := procQueryImageName.Call(uintptr(h), 0, uintptr(unsafe.Pointer(&name[0])), uintptr(unsafe.Pointer(&size))); r == 0 {
		return pid, false, fmt.Errorf("QueryFullProcessImageName(%d): %w", pid, err)
	}
	return pid, strings.EqualFold(syscall.UTF16ToString(name[:size]), req.Exe), nil
}

// killRun ends the run in req.Dir when it is still running: a hang only.
// Its exit code is then the helper's failure, which no check takes for the
// product's.
func killRun(req windowsRequest) error {
	pid, alive, err := runAlive(req)
	if err != nil || !alive {
		return err
	}
	const terminate = 0x0001
	h, err := syscall.OpenProcess(terminate, false, uint32(pid))
	if err != nil {
		return err
	}
	defer syscall.CloseHandle(h)
	return syscall.TerminateProcess(h, windowsHelperFailed)
}

// windowsInterrupt sends Ctrl-C to the run's console as the console does
// when Ctrl+C is pressed at it: CTRL_C_EVENT (never CTRL_BREAK_EVENT) to
// every process attached to it. A Ctrl+C key written into the console's
// input with WriteConsoleInput was tried and is taken as a plain key, no
// signal.
func windowsInterrupt(req windowsRequest) error {
	pid, alive, err := runAlive(req)
	if err != nil {
		return err
	}
	if !alive {
		return fmt.Errorf("no run of %s in %s to interrupt", req.Exe, req.Dir)
	}
	_, _, _ = procFreeConsole.Call()
	if r, _, err := procAttachConsole.Call(uintptr(pid)); r == 0 {
		return fmt.Errorf("attaching to the console of process %d: %w", pid, err)
	}
	// This process ignores the Ctrl-C it sends.
	if r, _, err := procSetConsoleCtrlHandler.Call(0, 1); r == 0 {
		return fmt.Errorf("SetConsoleCtrlHandler: %w", err)
	}
	const ctrlCEvent, allOfTheConsole = 0, 0
	if r, _, err := procGenerateCtrlEvent.Call(ctrlCEvent, allOfTheConsole); r == 0 {
		return fmt.Errorf("GenerateConsoleCtrlEvent: %w", err)
	}
	return nil
}
