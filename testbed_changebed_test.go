package main_test

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/bazarr"
	"github.com/BrianWeiHaoMa/bazarrbulksync/testbed/bed"
)

// changeBed is the bed as the changing checks read it. The executable
// reaches Bazarr through a proxy that records what it sends, since the bed's
// files and listing cannot show a request Bazarr refused or ignored.
type changeBed struct {
	bed.Manifest
	proxy    string
	mu       sync.Mutex
	requests []*sentRequest // each request of the last run, answered or not
	keyInURL bool           // a request of the last run carried the key in its URL
	cut      chan struct{}  // open while the network is cut (startCut)
	home     string         // HOME of each run, where a check sets one
}

// sentRequest is one request of a run as the proxy saw it.
type sentRequest struct {
	method, path string
	query        url.Values
	status       int // Bazarr's answer, 0 while unanswered
	sent         time.Time
	answered     time.Time // when the proxy let Bazarr's answer through
}

// statusWriter keeps the status of the answer it passes on.
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func newChangeBed(t *testing.T, m bed.Manifest) *changeBed {
	target, err := url.Parse(m.URL)
	if err != nil {
		t.Fatal(err)
	}
	b := &changeBed{Manifest: m}
	forward := httputil.NewSingleHostReverseProxy(target)
	forward.ModifyResponse, forward.ErrorHandler = b.answerUnlessCut, b.answerError
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b.mu.Lock()
		query, _ := url.QueryUnescape(r.URL.RawQuery)
		b.keyInURL = b.keyInURL || strings.Contains(r.URL.String(), m.APIKey) || strings.Contains(query, m.APIKey)
		req := &sentRequest{method: r.Method, path: r.URL.Path, query: r.URL.Query(), sent: time.Now()}
		b.requests = append(b.requests, req)
		b.mu.Unlock()
		r = b.holdIfCut(r, req)
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		forward.ServeHTTP(sw, r)
		b.mu.Lock()
		req.status = sw.status
		b.mu.Unlock()
	}))
	t.Cleanup(srv.Close)
	b.proxy = srv.URL
	return b
}

// exec runs the product and returns what it left and the requests it
// sent. The key travels only in its header and is never printed (R2); a
// run that breaks that stops the check before its output reaches the log.
func (b *changeBed) exec(t *testing.T, args ...string) (result, []string) {
	t.Helper()
	return b.execTyped(t, "", args...)
}

// execTyped runs the product as exec does, with a terminal as its standard
// input, typed into, when typed is not empty.
func (b *changeBed) execTyped(t *testing.T, typed string, args ...string) (result, []string) {
	t.Helper()
	b.forget()
	r := runProduct(t, b.env(), typed, args)
	b.keyKept(t, args, r)
	b.mu.Lock()
	defer b.mu.Unlock()
	return r, b.sent()
}

// keyKept stops the check, before the run's output reaches the log, when
// the last run put the key in a URL or printed it (R2).
func (b *changeBed) keyKept(t *testing.T, args []string, r result) {
	t.Helper()
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.keyInURL || strings.Contains(r.stdout+r.stderr, b.APIKey) {
		t.Fatalf("%q put the API key in a URL or printed it", args)
	}
}

// env is the environment of each run: the proxy's address, the key, and
// the home a check set.
func (b *changeBed) env() runEnv {
	return runEnv{url: b.proxy, key: b.APIKey, home: b.home}
}

// forget clears what the proxy recorded, before a run.
func (b *changeBed) forget() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.requests, b.keyInURL = nil, false
}

// run runs the product and requires the exit code want.
func (b *changeBed) run(t *testing.T, want int, args ...string) result {
	t.Helper()
	r, _ := b.exec(t, args...)
	if r.code != want {
		t.Fatalf("%q: code %d, want %d; stdout %q, stderr %q", args, r.code, want, r.stdout, r.stderr)
	}
	return r
}

// sends reports unless the last run sent n changing requests.
func (b *changeBed) sends(t *testing.T, n int) {
	t.Helper()
	b.mu.Lock()
	defer b.mu.Unlock()
	if got := changes(b.sent()); len(got) != n {
		t.Errorf("the run sent %v, want %d changing requests", got, n)
	}
}

// sent returns "METHOD path" of each request of the last run; b.mu is held.
func (b *changeBed) sent() []string {
	var out []string
	for _, r := range b.requests {
		out = append(out, r.method+" "+r.path)
	}
	return out
}

// changes returns the requests of sent that are not reads.
func changes(sent []string) []string {
	var out []string
	for _, s := range sent {
		if !strings.HasPrefix(s, "GET ") {
			out = append(out, s)
		}
	}
	return out
}

// list returns what `list` with args prints.
func (b *changeBed) list(t *testing.T, args ...string) string {
	t.Helper()
	return b.run(t, 0, append([]string{"list"}, args...)...).stdout
}

// listLines returns the subtitle lines of `list` with args.
func (b *changeBed) listLines(t *testing.T, args ...string) []string {
	t.Helper()
	lines, _, _ := strings.Cut(b.list(t, args...), "\n\n")
	return strings.Split(lines, "\n")
}

// listLine returns the line `list` prints for s.
func (b *changeBed) listLine(t *testing.T, s bazarr.Subtitle) string {
	t.Helper()
	for _, l := range b.listLines(t) {
		if strings.HasSuffix(l, "  "+s.Path) {
			return l
		}
	}
	t.Fatalf("list prints no line for %s", s.Path)
	return ""
}

// listedPaths returns the path of every subtitle `list` prints.
func (b *changeBed) listedPaths(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, obj := range listJSON(t, b.Manifest) {
		out = append(out, obj.Path)
	}
	return out
}

// files reads every file the bed placed and every subtitle file Bazarr now
// shows; a file not there is absent.
func (b *changeBed) files(t *testing.T) map[string][]byte {
	t.Helper()
	out := map[string][]byte{}
	for _, p := range append(paths(b.Subtitles), pathsInAnswer(t, b.Manifest)...) {
		data, err := b.ReadFile(p)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		out[p] = data
	}
	return out
}

// unchanged reports each file of before or after, other than except, whose
// bytes or presence differ between the two.
func (b *changeBed) unchanged(t *testing.T, before, after map[string][]byte, except ...string) {
	t.Helper()
	seen := map[string]bool{}
	for _, files := range []map[string][]byte{before, after} {
		for p := range files {
			x, inBefore := before[p]
			y, inAfter := after[p]
			if !seen[p] && !slices.Contains(except, p) && (inBefore != inAfter || !bytes.Equal(x, y)) {
				t.Errorf("%s changed (there before %v, after %v)", p, inBefore, inAfter)
			}
			seen[p] = true
		}
	}
}

// needProvisioned stops the subtest unless each path holds the bytes the
// bed placed and Bazarr shows it.
func (b *changeBed) needProvisioned(t *testing.T, paths ...string) {
	t.Helper()
	var subs []bazarr.Subtitle
	for _, p := range paths {
		data, err := b.ReadFile(p)
		if err != nil {
			t.Fatalf("%v; run `go run ./testbed up` before these checks", err)
		}
		if sum := sha256.Sum256(data); hex.EncodeToString(sum[:]) != b.Placed[p] {
			t.Fatalf("%s is not as the bed placed it; run `go run ./testbed up` before these checks", p)
		}
		subs = append(subs, placedAt(b.Manifest, p)...)
	}
	b.needShown(t, subs...)
}

// needShown stops the subtest unless Bazarr shows each subtitle and its
// file is there.
func (b *changeBed) needShown(t *testing.T, subs ...bazarr.Subtitle) {
	t.Helper()
	shown := pathsInAnswer(t, b.Manifest)
	for _, s := range subs {
		if _, err := b.ReadFile(s.Path); err != nil || !slices.Contains(shown, s.Path) {
			t.Fatalf("Bazarr does not show %s, or its file is gone; run `go run ./testbed up` before these checks", s.Path)
		}
	}
}

// corrected checks that path held each planted text before and holds what
// the fix makes of it after, without the planted text.
func (b *changeBed) corrected(t *testing.T, planted []bed.Planted, before, after map[string][]byte, path string) {
	t.Helper()
	for _, p := range planted {
		if !bytes.Contains(before[p.Path], []byte(p.Before)) {
			t.Fatalf("%s did not hold %q before the fix", p.Path, p.Before)
		}
		if got := after[path]; bytes.Contains(got, []byte(p.Before)) || !bytes.Contains(got, []byte(p.After)) {
			t.Errorf("after %s, %s holds %q: %v, and %q: %v; want only the second", p.Fix, path,
				p.Before, bytes.Contains(got, []byte(p.Before)), p.After, bytes.Contains(got, []byte(p.After)))
		}
	}
}

// planted returns what the bed planted for fix.
func (b *changeBed) planted(fix string) []bed.Planted {
	var out []bed.Planted
	for _, p := range b.Planted {
		if p.Fix == fix {
			out = append(out, p)
		}
	}
	return out
}

func (b *changeBed) subtitle(t *testing.T, path string) bazarr.Subtitle {
	t.Helper()
	return b.one(t, path, func(s bazarr.Subtitle) bool { return s.Path == path })
}

// one returns the one placed subtitle that matches.
func (b *changeBed) one(t *testing.T, what string, matches func(bazarr.Subtitle) bool) bazarr.Subtitle {
	t.Helper()
	var found []bazarr.Subtitle
	for _, s := range b.Subtitles {
		if matches(s) {
			found = append(found, s)
		}
	}
	if len(found) != 1 {
		t.Fatalf("the bed placed %d subtitles that are %s, want 1", len(found), what)
	}
	return found[0]
}

func (b *changeBed) language(code string) []bazarr.Subtitle {
	var out []bazarr.Subtitle
	for _, s := range b.Subtitles {
		if s.Language == code {
			out = append(out, s)
		}
	}
	return out
}

// hiBesidePlain returns the hearing-impaired subtitle beside a plain one of
// its language on its video, and that plain one.
func (b *changeBed) hiBesidePlain(t *testing.T) (hi, plain bazarr.Subtitle) {
	t.Helper()
	plain = b.one(t, "plain beside a hearing-impaired subtitle", func(s bazarr.Subtitle) bool {
		return !s.HI && !s.Forced && slices.ContainsFunc(b.Subtitles, func(o bazarr.Subtitle) bool { return o.HI && sameVideoLanguage(o, s) })
	})
	hi = b.one(t, "hearing-impaired beside a plain one", func(s bazarr.Subtitle) bool { return s.HI && sameVideoLanguage(s, plain) })
	return hi, plain
}

// loneHI returns the hearing-impaired subtitle with no other subtitle of its
// language on its video.
func (b *changeBed) loneHI(t *testing.T) bazarr.Subtitle {
	t.Helper()
	return b.one(t, "hearing-impaired alone in its language", func(s bazarr.Subtitle) bool {
		return s.HI && !slices.ContainsFunc(b.Subtitles, func(o bazarr.Subtitle) bool { return o.Path != s.Path && sameVideoLanguage(o, s) })
	})
}

// selectAlone returns selection options that select s and nothing else, as
// `list` shows.
func (b *changeBed) selectAlone(t *testing.T, s bazarr.Subtitle) []string {
	t.Helper()
	args := []string{"--title", s.Title, "--language", s.Language, "--no-hi", "--no-forced"}
	if s.HI {
		args[4] = "--hi"
	}
	if s.Forced {
		args[5] = "--forced"
	}
	if s.Type == bazarr.TypeEpisode {
		args = append(args, "--season", strconv.Itoa(s.Season))
	}
	if got := listJSON(t, b.Manifest, args...); len(got) != 1 || got[0].Path != s.Path {
		t.Fatalf("%q does not select %s alone", args, s.Path)
	}
	return args
}

func sameVideoLanguage(a, b bazarr.Subtitle) bool {
	return a.Type == b.Type && a.MovieID == b.MovieID && a.EpisodeID == b.EpisodeID && a.Language == b.Language
}

// reports checks that a changing run printed, before its totals, one line
// opening with word for each list line of lines, in order, and the totals
// that count them under word (G14, P3).
func (b *changeBed) reports(t *testing.T, r result, word string, lines []string) {
	t.Helper()
	var want []string
	for _, l := range lines {
		want = append(want, word+"  "+l)
	}
	noun := "subtitles"
	if len(lines) == 1 {
		noun = "subtitle"
	}
	totals := fmt.Sprintf("Total: %d of %d selected %s\n%s %d\n", len(lines), len(lines), noun, word, len(lines))
	if got, gotTotals, _ := strings.Cut(r.stdout, "\n\n"); !slices.Equal(strings.Split(got, "\n"), want) || gotTotals != totals {
		t.Errorf("the run printed\n%s\nwant\n%s\n\n%s", r.stdout, strings.Join(want, "\n"), totals)
	}
}

// textLines returns the cue text lines of an SRT file, without line ends or
// surrounding space.
func textLines(data []byte) []string {
	var out []string
	for _, l := range strings.Split(string(data), "\n") {
		l = strings.TrimSpace(strings.TrimPrefix(l, "\ufeff"))
		if _, err := strconv.Atoi(l); l == "" || err == nil || strings.Contains(l, "-->") {
			continue
		}
		out = append(out, l)
	}
	return out
}

// live runs `sync` with args through the proxy, handing each line of its
// standard output to asReported and then to onLine as it comes, with a
// function that interrupts the run as Ctrl-C does. A real sync takes
// seconds, and a run waits five seconds between some of its reads, so the
// deadline is minutes.
func (b *changeBed) live(t *testing.T, args []string, onLine func(line string, stop func())) result {
	t.Helper()
	b.forget()
	run, err := onHost(t).start(b.env(), args)
	if err != nil {
		t.Fatalf("%q: %v", args, err)
	}
	timer := time.AfterFunc(5*time.Minute, run.kill)
	defer timer.Stop()
	stop := func() {
		if err := run.interrupt(); err != nil {
			t.Errorf("interrupting the run: %v", err)
		}
	}
	var stdout strings.Builder
	lines := bufio.NewScanner(run.stdout)
	for lines.Scan() {
		stdout.WriteString(lines.Text() + "\n")
		b.asReported(t, lines.Text())
		if onLine != nil {
			onLine(lines.Text(), stop)
		}
	}
	r, err := run.wait()
	if err != nil {
		t.Fatalf("%q: %v", args, err)
	}
	r.stdout = stdout.String() + r.stdout
	b.keyKept(t, args, r)
	return r
}

// syncsOfLastRun returns the sync requests of the last run, in the order
// it sent them.
func (b *changeBed) syncsOfLastRun() []*sentRequest {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []*sentRequest
	for _, r := range b.requests {
		if r.method == "PATCH" && r.query.Get("action") == "sync" {
			c := *r
			out = append(out, &c)
		}
	}
	return out
}

// awaitSyncs waits until the running run has had n sync requests answered.
func (b *changeBed) awaitSyncs(t *testing.T, n int) {
	for deadline := time.Now().Add(time.Minute); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		answered := 0
		for _, r := range b.syncsOfLastRun() {
			if r.status != 0 {
				answered++
			}
		}
		if answered >= n {
			return
		}
	}
	t.Errorf("the run had not sent %d syncs within a minute", n)
}

func describe(reqs []*sentRequest) string {
	var out []string
	for _, r := range reqs {
		out = append(out, fmt.Sprintf("%s %s %v -> %d", r.method, r.path, r.query, r.status))
	}
	return "[" + strings.Join(out, "; ") + "]"
}
