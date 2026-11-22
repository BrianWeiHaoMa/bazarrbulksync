package commands_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/commands"
	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/fakebazarr"
	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/fakeclock"
	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/reporter"
)

// outcome is what one run of the composed command left behind.
type outcome struct {
	code   int
	stdout string
	stderr string
}

// run runs the composed command with args, the environment env, and the
// test's clock, as main runs it, with standard input not a terminal, and
// returns its exit code and streams.
func run(ctx context.Context, clock *fakeclock.Clock, env map[string]string, args ...string) outcome {
	return <-startWith(ctx, clock, env, notTerminal, args...).done
}

// stdin is the standard input a run gets: its reader, and whether the
// terminal query says it is a terminal.
type stdin struct {
	reader   io.Reader
	terminal bool
}

// notTerminal is standard input as a scheduler gives it: /dev/null.
var notTerminal = stdin{reader: strings.NewReader("")}

// typed is a terminal on which the user has typed text.
func typed(text string) stdin { return stdin{reader: strings.NewReader(text), terminal: true} }

// syncBuffer is a buffer the test may read while the run writes to it.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// keyboard is a terminal the test types on once the run reads from it.
type keyboard struct {
	asked   chan struct{}
	once    sync.Once
	lines   chan string
	pending string
}

func newKeyboard() *keyboard {
	return &keyboard{asked: make(chan struct{}), lines: make(chan string, 1)}
}

func (k *keyboard) Read(p []byte) (int, error) {
	k.once.Do(func() { close(k.asked) })
	if k.pending == "" {
		line, ok := <-k.lines
		if !ok {
			return 0, io.EOF
		}
		k.pending = line
	}
	n := copy(p, k.pending)
	k.pending = k.pending[n:]
	return n, nil
}

// waitAsked blocks until the run reads its answer.
func (k *keyboard) waitAsked(t *testing.T) {
	t.Helper()
	select {
	case <-k.asked:
	case <-time.After(10 * time.Second): // a guard against a hang only
		t.Fatal("the run never read standard input")
	}
}

// running is a run in the background: its streams so far, and its outcome
// once it ends.
type running struct {
	stdout, stderr *syncBuffer
	done           <-chan outcome
}

// startWith starts the composed command in the background with in as
// standard input; standard output and error are not terminals.
func startWith(ctx context.Context, clock *fakeclock.Clock, env map[string]string, in stdin, args ...string) running {
	r := running{stdout: &syncBuffer{}, stderr: &syncBuffer{}}
	done := make(chan outcome, 1)
	r.done = done
	rep := reporter.FromStreams(reporter.Streams{In: in.reader, Out: r.stdout, Err: r.stderr, InTerminal: in.terminal})
	go func() {
		code := commands.Run(ctx, args, commands.Env{Getenv: func(name string) string { return env[name] }, Reporter: rep, Clock: clock})
		done <- outcome{code: code, stdout: r.stdout.String(), stderr: r.stderr.String()}
	}()
	return r
}

// runAgainst runs the composed command pointed at srv through the
// environment, as the product's documentation shows.
func runAgainst(t *testing.T, srv *fakebazarr.Server, args ...string) outcome {
	t.Helper()
	return run(context.Background(), fakeclock.New(t), envFor(srv), args...)
}

func envFor(srv *fakebazarr.Server) map[string]string {
	return map[string]string{"BAZARR_URL": srv.URL, "BAZARR_API_KEY": srv.APIKey()}
}

// startRun runs the composed command in the background; the returned
// channel delivers its outcome.
func startRun(ctx context.Context, clock *fakeclock.Clock, env map[string]string, args ...string) <-chan outcome {
	return startWith(ctx, clock, env, notTerminal, args...).done
}

// await returns the run's outcome; the deadline only turns a hang into a failure.
func await(t *testing.T, done <-chan outcome) outcome {
	t.Helper()
	select {
	case o := <-done:
		return o
	case <-time.After(10 * time.Second):
		t.Fatal("the command did not end within 10s")
		return outcome{}
	}
}

// failed asserts the run failed with a message on standard error and
// nothing on standard output (G5, G6).
func failed(t *testing.T, name string, o outcome) {
	t.Helper()
	if o.code == 0 {
		t.Errorf("%s: exit code 0, want nonzero", name)
	}
	if o.stdout != "" {
		t.Errorf("%s: standard output %q, want nothing", name, o.stdout)
	}
	if strings.TrimSpace(o.stderr) == "" {
		t.Errorf("%s: standard error empty, want the message", name)
	}
}

// failingWriter fails every write, as a full disk or a closed file does.
type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("no space left on device") }

func TestOutputFailure(t *testing.T) {
	srv := fakebazarr.New(t, fakebazarr.Config{Library: library})
	for _, args := range [][]string{{"list"}, {"list", "--json"}, {"--help"}} {
		var stderr bytes.Buffer
		code := commands.Run(context.Background(), args, commands.Env{
			Getenv:   func(name string) string { return envFor(srv)[name] },
			Reporter: reporter.New(failingWriter{}, &stderr),
			Clock:    fakeclock.New(t),
		})
		if code == 0 || !strings.Contains(stderr.String(), "no space left on device") {
			t.Errorf("%q: code %d, stderr %q; want the failed write said and a nonzero exit", args, code, stderr.String())
		}
	}
}

// stoppingWriter records what is written and ends the run's context on
// the first write, as a signal arriving during that write does.
type stoppingWriter struct {
	written bytes.Buffer
	stop    context.CancelFunc
}

func (w *stoppingWriter) Write(p []byte) (int, error) {
	w.stop()
	return w.written.Write(p)
}

func TestStopDuringLastWrite(t *testing.T) {
	srv := fakebazarr.New(t, fakebazarr.Config{Library: library})
	for _, c := range []struct {
		args     []string
		onStderr bool
	}{
		{[]string{"list", "--json", "--series-id", "3", "--season", "1"}, false}, // one object, the only write
		{[]string{"list", "--language", "xx"}, true},                             // the empty selection's message
	} {
		ctx, stop := context.WithCancel(context.Background())
		stopping := &stoppingWriter{stop: stop}
		var other bytes.Buffer
		out, errOut := io.Writer(stopping), io.Writer(&other)
		if c.onStderr {
			out, errOut = &other, stopping
		}
		code := commands.Run(ctx, c.args, commands.Env{
			Getenv: func(name string) string { return envFor(srv)[name] }, Reporter: reporter.New(out, errOut), Clock: fakeclock.New(t),
		})
		if code == 0 {
			t.Errorf("%q: a stop during the last write exited 0, want nonzero (G4)", c.args)
		}
		stop()
	}
}

func TestUsageContactsNothing(t *testing.T) {
	srv := fakebazarr.New(t, fakebazarr.Config{})
	for _, args := range [][]string{
		{"list", "--movies", "--series"}, {"list", "--hi", "--no-hi"}, {"list", "--forced", "--no-forced"},
		{}, {"synch"}, {"list", "--bogus"}, {"list", "--season", "x"},
	} {
		o := runAgainst(t, srv, args...)
		failed(t, strings.Join(args, " "), o)
		if !strings.Contains(o.stderr, "--help") {
			t.Errorf("%q: standard error %q, want the usage hint", args, o.stderr)
		}
	}
	for _, args := range [][]string{{"--help"}, {"--version"}, {"list", "--help"}} {
		if o := runAgainst(t, srv, args...); o.code != 0 {
			t.Errorf("%q: exit code %d, want 0", args, o.code)
		}
	}
	if n := len(srv.Requests()); n != 0 {
		t.Errorf("the fake received %d requests, want none (G1)", n)
	}
}

func TestHelpAndVersion(t *testing.T) {
	srv := fakebazarr.New(t, fakebazarr.Config{})
	for args, want := range map[string]string{
		"--help": "Usage:", "--version": "bazarrbulksync", "list --help": "--json",
	} {
		o := runAgainst(t, srv, strings.Fields(args)...)
		if o.code != 0 || !strings.Contains(o.stdout, want) || o.stderr != "" {
			t.Errorf("%s: code %d, stdout %q, stderr %q; want %q on standard output, exit 0", args, o.code, o.stdout, o.stderr, want)
		}
	}
}

func TestOptionsOverrideEnvironment(t *testing.T) {
	envSrv := fakebazarr.New(t, fakebazarr.Config{APIKey: "environment-key-0000"})
	optSrv := fakebazarr.New(t, fakebazarr.Config{APIKey: "option-key-1111", Library: library})
	o := run(context.Background(), fakeclock.New(t), envFor(envSrv),
		"list", "--url", optSrv.URL, "--api-key", "option-key-1111")
	if o.code != 0 || o.stdout == "" {
		t.Fatalf("code %d, stdout %q, stderr %q; want the list from the option's Bazarr", o.code, o.stdout, o.stderr)
	}
	if n := len(envSrv.Requests()); n != 0 {
		t.Errorf("the environment's Bazarr received %d requests, want none (F4)", n)
	}
	reqs := optSrv.Requests()
	if len(reqs) == 0 {
		t.Fatal("the option's Bazarr received no request")
	}
	for _, r := range reqs {
		if got := r.Header.Get("X-API-KEY"); got != "option-key-1111" {
			t.Errorf("%s carried key %q, want the option's", r.Path, got)
		}
	}
}

func TestConnectionFailures(t *testing.T) {
	const key = "secret-key-7c1e9a0b"
	rejecting := fakebazarr.New(t, fakebazarr.Config{APIKey: "another-key"})
	old := fakebazarr.New(t, fakebazarr.Config{APIKey: key, Version: "1.5.9"})
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	silent := "http://" + listener.Addr().String()
	listener.Close() // nothing listens there now

	cases := []struct {
		name, url, key string
	}{
		{"missing address", "", key},
		{"missing key", rejecting.URL, ""},
		{"unusable address", "localhost:6767", key},
		{"nothing listening", silent, key},
		{"rejected key", rejecting.URL, key},
		{"old version", old.URL, key},
	}
	messages := map[string]string{}
	for _, c := range cases {
		o := run(context.Background(), fakeclock.New(t), map[string]string{"BAZARR_URL": c.url, "BAZARR_API_KEY": c.key}, "list")
		failed(t, c.name, o)
		if c.url != "" && !strings.Contains(o.stderr, c.url) {
			t.Errorf("%s: %q does not name the address %s (G2)", c.name, o.stderr, c.url)
		}
		if strings.Contains(o.stderr, key) {
			t.Errorf("%s: %q holds the API key (R2)", c.name, o.stderr)
		}
		if other, ok := messages[o.stderr]; ok {
			t.Errorf("%s and %s give the same message %q", c.name, other, o.stderr)
		}
		messages[o.stderr] = c.name
	}
}

func TestUnreachable(t *testing.T) {
	srv := fakebazarr.New(t, fakebazarr.Config{Library: library})
	srv.Respond(func(r fakebazarr.Request) fakebazarr.Behavior {
		if r.N == 1 {
			return fakebazarr.Serve // the connection check
		}
		return fakebazarr.Status(500)
	})
	clock := fakeclock.New(t)
	done := startRun(context.Background(), clock, envFor(srv), "list")
	// Advance through every wait between attempts until G3's five minutes
	// have passed since the first failure, which was sent at the clock's start.
	for passed := time.Duration(0); passed < 5*time.Minute; {
		d := clock.WaitSleep()
		clock.Advance(d)
		passed += d
	}
	o := await(t, done)
	failed(t, "unreachable", o)
	if !strings.Contains(o.stderr, "unreachable") || !strings.Contains(o.stderr, "HTTP 500") || !strings.Contains(o.stderr, srv.URL) {
		t.Errorf("standard error %q, want Bazarr at its address named unreachable with the cause (G3)", o.stderr)
	}
}

func TestStop(t *testing.T) {
	for name, behavior := range map[string]fakebazarr.Behavior{
		"read held":          fakebazarr.Hold,
		"during the retries": fakebazarr.Status(503),
	} {
		t.Run(name, func(t *testing.T) {
			srv := fakebazarr.New(t, fakebazarr.Config{Library: library})
			srv.Respond(func(r fakebazarr.Request) fakebazarr.Behavior {
				if r.N == 1 {
					return fakebazarr.Serve
				}
				return behavior
			})
			clock := fakeclock.New(t)
			ctx, stop := context.WithCancel(context.Background())
			done := startRun(ctx, clock, envFor(srv), "list")
			if name == "read held" {
				srv.WaitHeld()
			} else {
				clock.WaitSleep()
			}
			before := clock.Now()
			stop()
			o := await(t, done)
			failed(t, name, o)
			if !clock.Now().Equal(before) {
				t.Errorf("the clock moved; the stop waited for it")
			}
			n := len(srv.Requests())
			if n != 2 {
				t.Errorf("%d requests, want the check and the one read stopped (G4)", n)
			}
		})
	}
}

// TestShortKey: a key short enough to occur in the address or the
// program's words leaves both readable (G2), while a key quoted back from
// the command line is still hidden (R2).
func TestShortKey(t *testing.T) {
	for _, key := range []string{"6767", "e"} {
		srv := fakebazarr.New(t, fakebazarr.Config{APIKey: "another-key", BasePath: "/6767"})
		o := run(context.Background(), fakeclock.New(t), map[string]string{"BAZARR_URL": srv.URL, "BAZARR_API_KEY": key}, "list")
		failed(t, "short key "+key, o)
		if !strings.Contains(o.stderr, "Bazarr at "+srv.URL+" rejected the API key") {
			t.Errorf("key %q: standard error %q, want the full address and a readable cause", key, o.stderr)
		}
	}
	srv := fakebazarr.New(t, fakebazarr.Config{APIKey: "yes", Library: library})
	o := runAgainst(t, srv, "delete", "--language", "en")
	failed(t, "short accepted key", o)
	if !strings.Contains(o.stderr, "--yes") {
		t.Errorf("standard error %q, want --yes required, readable (G12)", o.stderr)
	}
	o = runAgainst(t, srv, "delete", "--title", "yes")
	failed(t, "key as title", o)
	if strings.Contains(o.stderr, `"yes"`) {
		t.Errorf("standard error %q quotes the API key back (R2)", o.stderr)
	}
}
