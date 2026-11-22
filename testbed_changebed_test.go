package main_test

import (
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
	sent     []string // "METHOD path" of each request of the last run
	keyInURL bool     // a request of the last run carried the key in its URL
}

func newChangeBed(t *testing.T, m bed.Manifest) *changeBed {
	target, err := url.Parse(m.URL)
	if err != nil {
		t.Fatal(err)
	}
	b := &changeBed{Manifest: m}
	forward := httputil.NewSingleHostReverseProxy(target)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b.mu.Lock()
		b.sent = append(b.sent, r.Method+" "+r.URL.Path)
		b.keyInURL = b.keyInURL || strings.Contains(r.URL.String(), m.APIKey)
		b.mu.Unlock()
		forward.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	b.proxy = srv.URL
	return b
}

// exec runs the executable and returns what it left and the requests it
// sent. The key travels only in its header and is never printed (R2); a
// run that breaks that stops the check before its output reaches the log.
func (b *changeBed) exec(t *testing.T, args ...string) (result, []string) {
	t.Helper()
	b.mu.Lock()
	b.sent, b.keyInURL = nil, false
	b.mu.Unlock()
	r := runExecutable(t, b.proxy, b.APIKey, args...)
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.keyInURL || strings.Contains(r.stdout+r.stderr, b.APIKey) {
		t.Fatalf("%q put the API key in a URL or printed it", args)
	}
	return r, slices.Clone(b.sent)
}

// run runs the executable and requires the exit code want.
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
	if got := changes(b.sent); len(got) != n {
		t.Errorf("the run sent %v, want %d changing requests", got, n)
	}
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
		subs = append(subs, b.subtitle(t, p))
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
