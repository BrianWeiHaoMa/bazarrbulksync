package commands_test

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/commands"
	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/fakebazarr"
	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/fakeclock"
	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/reporter"
)

// The subtitles `--language en` selects from library, in P2's order.
const (
	enHI      = "/movies/m7/film.en.hi.srt"
	enPlain   = "/movies/m7/film.en.srt"
	enSpecial = "/tv/s3/Specials/S00E01.en.srt"
)

var selectedEn = []string{enHI, enPlain, enSpecial}

// listLines returns each subtitle's line as `list` prints it now.
func listLines(t *testing.T, srv *fakebazarr.Server) map[string]string {
	t.Helper()
	o := runAgainst(t, srv, "list")
	lines := map[string]string{}
	for _, path := range []string{enHI, enPlain, enSpecial, "/movies/m7/film.fr.forced.srt", "/tv/s3/Season 01/S01E02.de.srt"} {
		if strings.Contains(o.stdout, path) {
			lines[path] = lineOf(t, o.stdout, path)
		}
	}
	return lines
}

// deletes returns the paths of the DELETE requests the fake received.
func deletes(srv *fakebazarr.Server) []string {
	var paths []string
	for _, r := range srv.Requests() {
		if r.Method == http.MethodDelete {
			paths = append(paths, r.Params().Get("path"))
		}
	}
	return paths
}

// resultLines returns standard output's lines before the blank line that
// opens the totals.
func resultLines(stdout string) []string {
	var lines []string
	for _, l := range strings.Split(stdout, "\n") {
		if l == "" {
			break
		}
		lines = append(lines, l)
	}
	return lines
}

func TestDeleteNeedsSelection(t *testing.T) {
	srv := fakebazarr.New(t, fakebazarr.Config{Library: library})
	for _, args := range [][]string{
		{"delete"}, {"delete", "--yes"}, {"delete", "--dry-run"},
		{"delete", "--url", srv.URL, "--api-key", srv.APIKey(), "--yes"},
	} {
		o := runAgainst(t, srv, args...)
		failed(t, strings.Join(args, " "), o)
		if o.code != 2 || !strings.Contains(o.stderr, "selection option") {
			t.Errorf("%q: code %d, stderr %q; want G26's message as a usage error", args, o.code, o.stderr)
		}
	}
	if o := runAgainst(t, srv, "delete", "--help"); o.code != 0 || !strings.Contains(o.stdout, "Usage: bazarrbulksync delete") {
		t.Errorf("delete --help: code %d, stdout %q", o.code, o.stdout)
	}
	if n := len(srv.Requests()); n != 0 {
		t.Errorf("the fake received %d requests, want none (G26)", n)
	}
}

func TestDeleteConnectionCheck(t *testing.T) {
	srv := fakebazarr.New(t, fakebazarr.Config{APIKey: "another-key", Library: library})
	o := run(context.Background(), fakeclock.New(t), map[string]string{"BAZARR_URL": srv.URL, "BAZARR_API_KEY": "secret-key-0b1c"},
		"delete", "--language", "en", "--yes")
	failed(t, "rejected key", o)
	if !strings.Contains(o.stderr, "rejected") || !strings.Contains(o.stderr, srv.URL) {
		t.Errorf("stderr %q; want the rejected key named with the address (G2)", o.stderr)
	}
	if n := len(srv.Requests()); n != 1 {
		t.Errorf("%d requests, want the connection check alone", n)
	}
}

func TestDeleteBeforeConfirmation(t *testing.T) {
	srv := fakebazarr.New(t, fakebazarr.Config{Library: library})
	for _, extra := range []string{"--yes", "--dry-run", "--language"} {
		args := []string{"delete", "--language", "xx", extra}
		if extra == "--language" {
			args = append(args, "yy") // neither --yes nor --dry-run
		}
		o := runAgainst(t, srv, args...)
		if o.code != 0 || len(resultLines(o.stdout)) != 0 || !strings.Contains(o.stdout, "Total: 0") ||
			!strings.Contains(o.stderr, "no external subtitle") || strings.Contains(o.stderr, "--yes") {
			t.Errorf("%q: code %d, stdout %q, stderr %q; want G9's note, the zero totals (G13), exit 0", args, o.code, o.stdout, o.stderr)
		}
	}
	o := runAgainst(t, srv, "delete", "--title", "nosuch", "--yes")
	failed(t, "unmatched title", o)
	if !strings.Contains(o.stderr, "nosuch") {
		t.Errorf("stderr %q does not name the title (G8)", o.stderr)
	}
	if d := deletes(srv); len(d) != 0 {
		t.Errorf("deleted %q", d)
	}
}

func TestDeleteConfirmation(t *testing.T) {
	t.Run("asks on standard error once the selection is known", func(t *testing.T) {
		srv := fakebazarr.New(t, fakebazarr.Config{Library: library})
		lines := listLines(t, srv)
		keys := newKeyboard()
		r := startWith(context.Background(), fakeclock.New(t), envFor(srv), stdin{reader: keys, terminal: true}, "delete", "--language", "en")
		keys.waitAsked(t)
		if s := r.stderr.String(); !strings.Contains(s, "3 subtitles selected") || !strings.Contains(s, "[y/N]") {
			t.Errorf("standard error %q, want the number selected and the question (G12)", s)
		}
		if s := r.stdout.String(); s != "" {
			t.Errorf("standard output %q before the answer, want nothing (G5)", s)
		}
		if d := deletes(srv); len(d) != 0 {
			t.Errorf("deleted %q before the answer", d)
		}
		keys.lines <- "y\n"
		o := await(t, r.done)
		if o.code != 0 || len(deletes(srv)) != 3 {
			t.Fatalf("code %d, deletes %q, stderr %q; want the three deleted", o.code, deletes(srv), o.stderr)
		}
		for i, line := range resultLines(o.stdout) {
			if want := "deleted  " + lines[selectedEn[i]]; line != want {
				t.Errorf("line %d %q, want %q (P3)", i, line, want)
			}
		}
	})
	for _, answer := range []string{"y\n", "Y\n", "yes\n", "YES\n", "yes\r\n"} {
		srv := fakebazarr.New(t, fakebazarr.Config{Library: library})
		o := <-startWith(context.Background(), fakeclock.New(t), envFor(srv), typed(answer), "delete", "--language", "en").done
		if o.code != 0 || len(deletes(srv)) != 3 {
			t.Errorf("answer %q: code %d, deletes %q, stderr %q; want the three deleted (P3)", answer, o.code, deletes(srv), o.stderr)
		}
	}
	for _, answer := range []string{"\n", "n\n", "no\n", "yess\n", "y es\n", " y\n", "yes \n", "", "y"} {
		srv := fakebazarr.New(t, fakebazarr.Config{Library: library})
		o := <-startWith(context.Background(), fakeclock.New(t), envFor(srv), typed(answer), "delete", "--language", "en").done
		failed(t, "answer "+answer, o)
		if d := deletes(srv); len(d) != 0 {
			t.Errorf("answer %q deleted %q (G12)", answer, d)
		}
	}
	t.Run("standard input not a terminal", func(t *testing.T) {
		srv := fakebazarr.New(t, fakebazarr.Config{Library: library})
		o := runAgainst(t, srv, "delete", "--language", "en")
		failed(t, "not a terminal", o)
		if !strings.Contains(o.stderr, "--yes") || strings.Contains(o.stderr, "[y/N]") {
			t.Errorf("stderr %q; want --yes required and no question (G12)", o.stderr)
		}
		if d := deletes(srv); len(d) != 0 {
			t.Errorf("deleted %q", d)
		}
	})
}

func TestDeleteDryRun(t *testing.T) {
	for _, extra := range [][]string{nil, {"--yes"}} {
		srv := fakebazarr.New(t, fakebazarr.Config{Library: library})
		lines := listLines(t, srv)
		o := runAgainst(t, srv, append([]string{"delete", "--language", "en", "--dry-run"}, extra...)...)
		if o.code != 0 || len(deletes(srv)) != 0 {
			t.Fatalf("%q: code %d, deletes %q, stderr %q; want nothing deleted, exit 0 (G13)", extra, o.code, deletes(srv), o.stderr)
		}
		got := resultLines(o.stdout)
		if len(got) != 3 {
			t.Fatalf("%q: standard output %q, want three lines and the totals", extra, o.stdout)
		}
		for i, line := range got {
			if want := "would delete  " + lines[selectedEn[i]]; line != want {
				t.Errorf("%q: line %d %q, want %q (P3)", extra, i, line, want)
			}
		}
		if !strings.Contains(o.stdout, "would delete 3") {
			t.Errorf("%q: standard output %q, want the totals (G13)", extra, o.stdout)
		}
	}
}

func TestDeleteOutcomes(t *testing.T) {
	t.Run("one refused, one unanswered, one accepted", func(t *testing.T) {
		srv := fakebazarr.New(t, fakebazarr.Config{Library: library})
		lines := listLines(t, srv)
		var mu sync.Mutex
		n := 0
		srv.Respond(func(r fakebazarr.Request) fakebazarr.Behavior {
			if r.Method != http.MethodDelete {
				return fakebazarr.Serve
			}
			mu.Lock()
			defer mu.Unlock()
			n++
			switch n {
			case 1:
				return fakebazarr.Reply(500, `"Subtitle file not found"`)
			case 2:
				return fakebazarr.Status(504)
			}
			return fakebazarr.Serve
		})
		o := runAgainst(t, srv, "delete", "--language", "en", "--yes")
		if o.code == 0 {
			t.Errorf("exit code 0, want nonzero for failed and unconfirmed (G6)")
		}
		want := []string{
			"failed  " + lines[enHI] + "  (Subtitle file not found)",
			"unconfirmed  " + lines[enPlain],
			"deleted  " + lines[enSpecial],
		}
		got := resultLines(o.stdout)
		if strings.Join(got, "\n") != strings.Join(want, "\n") {
			t.Errorf("result lines\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
		}
		if d := deletes(srv); strings.Join(d, "|") != strings.Join(selectedEn, "|") {
			t.Errorf("deletes %q, want each selected subtitle once, in order (G27)", d)
		}
		for _, w := range []string{"Total: 3 of 3", "deleted 1", "failed 1", "unconfirmed 1"} {
			if !strings.Contains(o.stdout, w) {
				t.Errorf("standard output %q lacks %q (G14)", o.stdout, w)
			}
		}
	})
	t.Run("all accepted", func(t *testing.T) {
		srv := fakebazarr.New(t, fakebazarr.Config{Library: library})
		before := listLines(t, srv)
		o := runAgainst(t, srv, "delete", "--language", "en", "--yes")
		if o.code != 0 || !strings.Contains(o.stdout, "deleted 3") {
			t.Fatalf("code %d, stdout %q, stderr %q; want exit 0", o.code, o.stdout, o.stderr)
		}
		after := listLines(t, srv)
		for path, line := range before {
			selected := strings.HasSuffix(path, ".en.srt") || strings.HasSuffix(path, ".en.hi.srt")
			if _, still := after[path]; still == selected {
				t.Errorf("%s still listed: %v, want %v", path, still, !selected)
			} else if still && after[path] != line {
				t.Errorf("%s changed: %q, was %q", path, after[path], line)
			}
		}
	})
}

func TestDeleteLinesAsKnown(t *testing.T) {
	srv := fakebazarr.New(t, fakebazarr.Config{Library: library})
	lines := listLines(t, srv)
	srv.Respond(func(r fakebazarr.Request) fakebazarr.Behavior {
		if r.Method == http.MethodDelete && r.Params().Get("path") == enPlain {
			return fakebazarr.Hold
		}
		return fakebazarr.Serve
	})
	ctx, stop := context.WithCancel(context.Background())
	r := startWith(ctx, fakeclock.New(t), envFor(srv), notTerminal, "delete", "--language", "en", "--yes")
	srv.WaitHeld()
	if s := r.stdout.String(); !strings.Contains(s, "deleted  "+lines[enHI]+"\n") {
		t.Errorf("standard output %q while the second is held, want the first's line (G14)", s)
	}
	stop()
	await(t, r.done)
}

func TestDeleteProgress(t *testing.T) {
	srv := fakebazarr.New(t, fakebazarr.Config{Library: library})
	o := runAgainst(t, srv, "delete", "--language", "en", "--yes")
	for _, w := range []string{"1 of 3", "2 of 3", "3 of 3"} {
		if !strings.Contains(o.stderr, w) {
			t.Errorf("standard error %q lacks %q (G14)", o.stderr, w)
		}
	}
	for name, s := range map[string]string{"standard output": o.stdout, "standard error": o.stderr} {
		if strings.ContainsAny(s, "\r\x1b") {
			t.Errorf("%s %q holds a carriage return or an escape sequence, not being a terminal (G5, P3)", name, s)
		}
	}
	for _, line := range resultLines(o.stdout) {
		if !strings.HasPrefix(line, "deleted  ") {
			t.Errorf("standard output line %q is not a result line (G5)", line)
		}
	}
}

func TestDeleteStop(t *testing.T) {
	srv := fakebazarr.New(t, fakebazarr.Config{Library: library})
	lines := listLines(t, srv)
	srv.Respond(func(r fakebazarr.Request) fakebazarr.Behavior {
		if r.Method == http.MethodDelete && r.Params().Get("path") == enPlain {
			return fakebazarr.Hold
		}
		return fakebazarr.Serve
	})
	ctx, stop := context.WithCancel(context.Background())
	r := startWith(ctx, fakeclock.New(t), envFor(srv), notTerminal, "delete", "--language", "en", "--yes")
	srv.WaitHeld()
	sent := len(srv.Requests())
	stop()
	o := await(t, r.done)
	if o.code == 0 {
		t.Errorf("exit code 0, want nonzero (G4)")
	}
	want := []string{"deleted  " + lines[enHI], "unconfirmed  " + lines[enPlain]}
	if got := resultLines(o.stdout); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("result lines %q, want %q (G4, Stopping)", got, want)
	}
	for _, w := range []string{"Total: 2 of 3", "deleted 1", "unconfirmed 1"} {
		if !strings.Contains(o.stdout, w) {
			t.Errorf("standard output %q lacks %q (G4)", o.stdout, w)
		}
	}
	if n := len(srv.Requests()); n != sent {
		t.Errorf("%d requests after the stop, want none (G4)", n-sent)
	}
	if !strings.Contains(o.stderr, "stopped") {
		t.Errorf("standard error %q does not say the run stopped", o.stderr)
	}
}

func TestDeleteStopBeforeChanges(t *testing.T) {
	zeroTotals := func(t *testing.T, name string, o outcome) {
		t.Helper()
		if o.code == 0 || !strings.Contains(o.stdout, "Total: 0") || len(resultLines(o.stdout)) != 0 {
			t.Errorf("%s: code %d, stdout %q, stderr %q; want the zero totals alone and a nonzero exit (G4)", name, o.code, o.stdout, o.stderr)
		}
	}
	for held, call := range map[string]string{"connection check": "/api/system/status", "library read": "/api/movies"} {
		srv := fakebazarr.New(t, fakebazarr.Config{Library: library})
		srv.Respond(func(r fakebazarr.Request) fakebazarr.Behavior {
			if r.Path == call {
				return fakebazarr.Hold
			}
			return fakebazarr.Serve
		})
		ctx, stop := context.WithCancel(context.Background())
		r := startWith(ctx, fakeclock.New(t), envFor(srv), notTerminal, "delete", "--language", "en", "--yes")
		srv.WaitHeld()
		stop()
		zeroTotals(t, "stop during the "+held, await(t, r.done))
	}

	srv := fakebazarr.New(t, fakebazarr.Config{Library: library})
	srv.Respond(func(r fakebazarr.Request) fakebazarr.Behavior {
		if r.N == 1 {
			return fakebazarr.Serve
		}
		return fakebazarr.Status(500)
	})
	clock := fakeclock.New(t)
	start := clock.Now()
	r := startWith(context.Background(), clock, envFor(srv), notTerminal, "delete", "--language", "en", "--yes")
	for clock.Now().Before(start.Add(5 * time.Minute)) {
		clock.Advance(clock.WaitSleep())
	}
	o := await(t, r.done)
	zeroTotals(t, "unreachable read", o)
	if !strings.Contains(o.stderr, "unreachable") {
		t.Errorf("standard error %q, want Bazarr named unreachable (G3)", o.stderr)
	}

	srv = fakebazarr.New(t, fakebazarr.Config{Library: library})
	keys := newKeyboard()
	ctx, stop := context.WithCancel(context.Background())
	r = startWith(ctx, fakeclock.New(t), envFor(srv), stdin{reader: keys, terminal: true}, "delete", "--language", "en")
	keys.waitAsked(t)
	stop()
	zeroTotals(t, "stop at the prompt", await(t, r.done))
	if d := deletes(srv); len(d) != 0 {
		t.Errorf("the stopped prompt deleted %q", d)
	}

	// Neither a failed check nor an answer other than yes is a stop.
	rejecting := fakebazarr.New(t, fakebazarr.Config{APIKey: "another-key", Library: library})
	o = run(context.Background(), fakeclock.New(t), map[string]string{"BAZARR_URL": rejecting.URL, "BAZARR_API_KEY": "k"},
		"delete", "--language", "en", "--yes")
	failed(t, "rejected key", o)
	srv = fakebazarr.New(t, fakebazarr.Config{Library: library})
	o = <-startWith(context.Background(), fakeclock.New(t), envFor(srv), typed("n\n"), "delete", "--language", "en").done
	failed(t, "answered no", o)
}

func TestDeleteStopDuringDryRun(t *testing.T) {
	srv := fakebazarr.New(t, fakebazarr.Config{Library: library})
	ctx, stop := context.WithCancel(context.Background())
	stopping := &stoppingWriter{stop: stop}
	var stderr syncBuffer
	code := commands.Run(ctx, []string{"delete", "--language", "en", "--dry-run"}, commands.Env{
		Getenv: func(name string) string { return envFor(srv)[name] }, Reporter: reporter.New(stopping, &stderr), Clock: fakeclock.New(t),
	})
	out := stopping.written.String()
	if code == 0 || len(resultLines(out)) != 1 || !strings.Contains(out, "Total: 1 of 3") {
		t.Errorf("code %d, stdout %q; want the first line, its totals, and a nonzero exit (G4)", code, out)
	}
}

func TestDeleteUnreachable(t *testing.T) {
	// silentFrom makes every request from the second DELETE on answer 503,
	// as a proxy does for a Bazarr that is down, until answering says so.
	silentFrom := func(srv *fakebazarr.Server, answering func(reads int) bool) {
		var mu sync.Mutex
		changes, reads := 0, 0
		srv.Respond(func(r fakebazarr.Request) fakebazarr.Behavior {
			mu.Lock()
			defer mu.Unlock()
			if r.Method == http.MethodDelete {
				changes++
				if changes == 2 {
					return fakebazarr.Status(503)
				}
				return fakebazarr.Serve
			}
			if changes < 2 {
				return fakebazarr.Serve
			}
			reads++
			if answering(reads) {
				return fakebazarr.Serve
			}
			return fakebazarr.Status(503)
		})
	}
	t.Run("silent for five minutes", func(t *testing.T) {
		srv := fakebazarr.New(t, fakebazarr.Config{Library: library})
		lines := listLines(t, srv)
		silentFrom(srv, func(int) bool { return false })
		clock := fakeclock.New(t)
		start := clock.Now()
		r := startWith(context.Background(), clock, envFor(srv), notTerminal, "delete", "--language", "en", "--yes")
		for clock.Now().Before(start.Add(5 * time.Minute)) {
			clock.Advance(clock.WaitSleep())
		}
		o := await(t, r.done)
		if o.code == 0 {
			t.Errorf("exit code 0, want nonzero (G3)")
		}
		want := []string{"deleted  " + lines[enHI], "unconfirmed  " + lines[enPlain]}
		if got := resultLines(o.stdout); strings.Join(got, "\n") != strings.Join(want, "\n") {
			t.Errorf("result lines %q, want %q", got, want)
		}
		if d := deletes(srv); len(d) != 2 {
			t.Errorf("deletes %q, want no third while Bazarr is silent", d)
		}
		if !strings.Contains(o.stdout, "Total: 2 of 3") || !strings.Contains(o.stderr, "unreachable") {
			t.Errorf("stdout %q, stderr %q; want the totals and Bazarr named unreachable (G3, G4)", o.stdout, o.stderr)
		}
	})
	t.Run("answers again inside the window", func(t *testing.T) {
		srv := fakebazarr.New(t, fakebazarr.Config{Library: library})
		lines := listLines(t, srv)
		silentFrom(srv, func(reads int) bool { return reads > 2 })
		clock := fakeclock.New(t)
		r := startWith(context.Background(), clock, envFor(srv), notTerminal, "delete", "--language", "en", "--yes")
		for range 2 {
			clock.Advance(clock.WaitSleep())
		}
		o := await(t, r.done)
		want := []string{"deleted  " + lines[enHI], "unconfirmed  " + lines[enPlain], "deleted  " + lines[enSpecial]}
		if got := resultLines(o.stdout); strings.Join(got, "\n") != strings.Join(want, "\n") {
			t.Errorf("result lines %q, want %q", got, want)
		}
		if o.code == 0 {
			t.Errorf("exit code 0, want nonzero for the unconfirmed (G6)")
		}
	})
}
