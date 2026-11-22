//go:build unix

package main_test

import (
	"strings"
	"syscall"
	"testing"

	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/fakebazarr"
)

// TestExecutableStops sends the running executable SIGINT (Ctrl-C) and
// SIGTERM (a container stop) while the fake holds a request unanswered: the
// connection check, or a read of the library. The request's own limit is a
// minute, so only the signal reaching it ends the run within the deadline.
func TestExecutableStops(t *testing.T) {
	for _, sig := range []syscall.Signal{syscall.SIGINT, syscall.SIGTERM} {
		for held, call := range map[string]string{"connection check": "/api/system/status", "library read": "/api/movies"} {
			t.Run(sig.String()+" during the "+held, func(t *testing.T) {
				srv := fakebazarr.New(t, fakebazarr.Config{})
				srv.Respond(func(r fakebazarr.Request) fakebazarr.Behavior {
					if r.Path == call {
						return fakebazarr.Hold
					}
					return fakebazarr.Serve
				})
				cmd, stdout, stderr := command(srv.URL, srv.APIKey(), "list")
				if err := cmd.Start(); err != nil {
					t.Fatal(err)
				}
				srv.WaitHeld()
				sent := len(srv.Requests())
				if err := cmd.Process.Signal(sig); err != nil {
					t.Fatal(err)
				}
				waitExit(t, cmd)
				status := cmd.ProcessState.Sys().(syscall.WaitStatus)
				if status.Signaled() {
					t.Fatalf("ended by the signal's default action (%v), not with an exit code of its own (S8)", status.Signal())
				}
				if n := len(srv.Requests()); n != sent {
					t.Errorf("%d requests after the stop, want none (G4)", n-sent)
				}
				if cmd.ProcessState.ExitCode() == 0 {
					t.Errorf("exit code 0, want nonzero (G4)")
				}
				if stdout.Len() != 0 || !strings.Contains(stderr.String(), "stopped") {
					t.Errorf("stdout %q, stderr %q; want only the stop said on standard error", stdout, stderr)
				}
			})
		}
	}
}

// TestExecutableStopsDelete sends SIGINT and SIGTERM while the fake holds the
// second DELETE: the first is reported deleted, the held one unconfirmed,
// then the totals; nothing more is sent (G4).
func TestExecutableStopsDelete(t *testing.T) {
	for _, sig := range []syscall.Signal{syscall.SIGINT, syscall.SIGTERM} {
		t.Run(sig.String(), func(t *testing.T) {
			srv := fakebazarr.New(t, fakebazarr.Config{Library: deleteLibrary})
			srv.Respond(func(r fakebazarr.Request) fakebazarr.Behavior {
				if r.Method == "DELETE" && r.Params().Get("path") == "/movies/Tears/Tears.es.hi.srt" {
					return fakebazarr.Hold
				}
				return fakebazarr.Serve
			})
			cmd, stdout, stderr := command(srv.URL, srv.APIKey(), "delete", "--language", "es", "--yes")
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			srv.WaitHeld()
			sent := len(srv.Requests())
			if err := cmd.Process.Signal(sig); err != nil {
				t.Fatal(err)
			}
			waitExit(t, cmd)
			if status := cmd.ProcessState.Sys().(syscall.WaitStatus); status.Signaled() {
				t.Fatalf("ended by the signal's default action (%v), not with an exit code of its own (S8)", status.Signal())
			}
			if n := len(srv.Requests()); n != sent {
				t.Errorf("%d requests after the stop, want none (G4)", n-sent)
			}
			out := stdout.String()
			if cmd.ProcessState.ExitCode() == 0 || !strings.Contains(out, "deleted  Sintel") ||
				!strings.Contains(out, "unconfirmed  Tears of Steel") || !strings.Contains(out, "Total: 2 of 3") {
				t.Errorf("code %d, stdout %q, stderr %q; want the deleted and unconfirmed lines and their totals, nonzero",
					cmd.ProcessState.ExitCode(), out, stderr)
			}
		})
	}
}
