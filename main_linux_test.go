package main_test

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/fakebazarr"
)

// openTerminal opens a pseudo-terminal and returns its two ends: what the
// user types goes into keys; the program reads it from tty.
func openTerminal(t *testing.T) (keys, tty *os.File) {
	t.Helper()
	keys, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { keys.Close() })
	var unlock int32
	var n uint32
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, keys.Fd(), syscall.TIOCSPTLCK, uintptr(unsafe.Pointer(&unlock))); e != 0 {
		t.Fatal(e)
	}
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, keys.Fd(), syscall.TIOCGPTN, uintptr(unsafe.Pointer(&n))); e != 0 {
		t.Fatal(e)
	}
	tty, err = os.OpenFile(fmt.Sprintf("/dev/pts/%d", n), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { tty.Close() })
	return keys, tty
}

// TestExecutableConfirmationTerminal: with a terminal as standard input the
// executable asks, deletes only on yes, and a stop ends the prompt (G12, G4).
func TestExecutableConfirmationTerminal(t *testing.T) {
	for answer, deletes := range map[string]int{"y\n": 3, "YES\n": 3, "n\n": 0, "\n": 0} {
		srv := fakebazarr.New(t, fakebazarr.Config{Library: deleteLibrary})
		keys, tty := openTerminal(t)
		cmd, stdout, stderr := command(srv.URL, srv.APIKey(), "delete", "--language", "es")
		cmd.Stdin = tty
		if _, err := keys.WriteString(answer); err != nil { // the terminal holds it until read
			t.Fatal(err)
		}
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		waitExit(t, cmd)
		code := cmd.ProcessState.ExitCode()
		if !strings.Contains(stderr.String(), "3 subtitles selected") || deletesReceived(srv) != deletes || (code == 0) != (deletes > 0) {
			t.Errorf("answer %q: code %d, %d deletes, stdout %q, stderr %q; want asked, %d deleted", answer, code, deletesReceived(srv), stdout, stderr, deletes)
		}
	}

	t.Run("SIGINT at the prompt", func(t *testing.T) {
		srv := fakebazarr.New(t, fakebazarr.Config{Library: deleteLibrary})
		_, tty := openTerminal(t)
		cmd, stdout, _ := command(srv.URL, srv.APIKey(), "delete", "--language", "es")
		cmd.Stdin = tty
		errRead, errWrite, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		cmd.Stderr = errWrite
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		errWrite.Close()
		var mu sync.Mutex
		var stderr bytes.Buffer
		prompted, drained := make(chan struct{}), make(chan struct{})
		go func() {
			defer close(drained)
			buf := make([]byte, 4096)
			for seen := false; ; {
				n, err := errRead.Read(buf)
				mu.Lock()
				stderr.Write(buf[:n])
				if !seen && strings.Contains(stderr.String(), "[y/N]") {
					seen = true
					close(prompted)
				}
				mu.Unlock()
				if err != nil {
					return
				}
			}
		}()
		select {
		case <-prompted: // the process is waiting at its prompt
		case <-drained:
			waitExit(t, cmd)
			t.Fatalf("exited without asking: %q", stderr.String())
		case <-time.After(20 * time.Second): // a guard against a hang only
			_ = cmd.Process.Kill()
			t.Fatal("no prompt within 20s")
		}
		if err := cmd.Process.Signal(syscall.SIGINT); err != nil {
			t.Fatal(err)
		}
		waitExit(t, cmd)
		<-drained
		if cmd.ProcessState.ExitCode() == 0 || deletesReceived(srv) != 0 || !strings.Contains(stdout.String(), "Total: 0") {
			t.Errorf("code %d, %d deletes, stdout %q, stderr %q; want the zero totals, nothing deleted, nonzero",
				cmd.ProcessState.ExitCode(), deletesReceived(srv), stdout, stderr.String())
		}
	})
}
