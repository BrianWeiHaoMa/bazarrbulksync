package reporter_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/bazarr"
	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/reporter"
)

var (
	movieSub   = bazarr.Subtitle{Type: bazarr.TypeMovie, MovieID: 7, Title: "Sintel", Language: "en", Path: "/m/film.en.srt"}
	episodeSub = bazarr.Subtitle{Type: bazarr.TypeEpisode, SeriesID: 3, EpisodeID: 101, Season: 1, Episode: 2,
		Title: "Fawlty Towers", Language: "de", HI: true, Path: "/tv/S01E02.de.hi.srt"}
)

// TestProgress: progress is redrawn in place on a terminal and erased before
// any other line on either stream; elsewhere it is plain lines (G14, P3, G5).
func TestProgress(t *testing.T) {
	var out, errOut bytes.Buffer
	rep := reporter.FromStreams(reporter.Streams{Out: &out, Err: &errOut, ErrTerminal: true})
	rep.Selected(2)
	rep.Result(reporter.Deleted, movieSub, "")
	rep.Result(reporter.Failed, episodeSub, "no such file")
	rep.Totals()
	rep.Error("done")
	erase := "\r" + strings.Repeat(" ", len("0 of 2 subtitles done")) + "\r"
	want := "0 of 2 subtitles done" + erase + "1 of 2 subtitles done" + erase +
		"2 of 2 subtitles done" + erase + "bazarrbulksync: done\n"
	if got := errOut.String(); got != want {
		t.Errorf("standard error %q,\nwant %q", got, want)
	}
	if strings.ContainsAny(out.String(), "\r\x1b") || strings.Contains(errOut.String(), "\x1b") {
		t.Errorf("standard output %q holds cursor movement, or standard error an escape sequence (G5)", out.String())
	}

	out.Reset()
	errOut.Reset()
	rep = reporter.New(&out, &errOut)
	rep.Selected(250)
	for range 250 {
		rep.Result(reporter.WouldDelete, movieSub, "")
	}
	lines := strings.Split(strings.TrimSuffix(errOut.String(), "\n"), "\n")
	if len(lines) != 125 || lines[0] != "2 of 250 subtitles done" || lines[124] != "250 of 250 subtitles done" {
		t.Errorf("%d progress lines, first %q, last %q; want every second count and the last", len(lines), lines[0], lines[len(lines)-1])
	}
	if strings.ContainsAny(errOut.String(), "\r\x1b") {
		t.Errorf("standard error not a terminal holds cursor movement (P3)")
	}

	// A run stopped between two sampled counts still shows where it got to.
	errOut.Reset()
	rep = reporter.New(&out, &errOut)
	rep.Selected(250)
	rep.Result(reporter.Unconfirmed, movieSub, "")
	rep.Totals()
	if got := errOut.String(); got != "1 of 250 subtitles done\n" {
		t.Errorf("standard error %q after a stop at the first subtitle, want its count", got)
	}
}

// TestTotals: the totals count each outcome and skip reason that occurred,
// adding up to the subtitles the run got to; a skip has a line only under
// --dry-run (G14, P3).
func TestTotals(t *testing.T) {
	var out, errOut bytes.Buffer
	rep := reporter.New(&out, &errOut)
	rep.Selected(5)
	rep.Result(reporter.Deleted, movieSub, "")
	rep.Skip(episodeSub, "some reason", false)
	rep.Result(reporter.Unconfirmed, movieSub, "")
	rep.Skip(movieSub, "some reason", true)
	rep.Totals()
	want := "deleted  " + "Sintel [movie 7]  en  /m/film.en.srt\n" +
		"unconfirmed  Sintel [movie 7]  en  /m/film.en.srt\n" +
		"would skip  Sintel [movie 7]  en  /m/film.en.srt  (some reason)\n" +
		"\nTotal: 4 of 5 selected subtitles\ndeleted 1, skipped (some reason) 2, unconfirmed 1\n"
	if got := out.String(); got != want {
		t.Errorf("standard output\n%s\nwant\n%s", got, want)
	}
	if !strings.Contains(errOut.String(), "4 of 5") {
		t.Errorf("standard error %q, want skips counted as done", errOut.String())
	}

	out.Reset()
	rep = reporter.New(&out, &errOut)
	rep.Totals()
	if got := out.String(); got != "\nTotal: 0 subtitles\n" {
		t.Errorf("totals before the selection %q, want every count zero (G4)", got)
	}
}

// TestResultLine: the subtitle is named as `list` names it, control
// characters from Bazarr escaped (P3, G5).
func TestResultLine(t *testing.T) {
	var out, listed, errOut bytes.Buffer
	odd := episodeSub
	odd.Path = "/tv/a\x1b[2Jb\n.srt"
	reporter.New(&listed, &errOut).Subtitle(odd)
	reporter.New(&out, &errOut).Result(reporter.Failed, odd, "line one\x1b")
	want := "failed  " + strings.TrimSuffix(listed.String(), "\n") + `  (line one\x1b)` + "\n"
	if got := out.String(); got != want || strings.ContainsAny(got[:len(got)-1], "\n\x1b") {
		t.Errorf("result line %q, want %q", got, want)
	}
}

// TestRedaction: a quoted value holding the key is hidden; the address and
// the program's own words keep a short key's text (R2).
func TestRedaction(t *testing.T) {
	var out, errOut bytes.Buffer
	rep := reporter.New(&out, &errOut)
	rep.Redact("6767")
	rep.Error(`Bazarr at http://host:6767 rejected the API key; no title contains "x6767y"; 6767 of 6767 done`)
	want := "bazarrbulksync: Bazarr at http://host:6767 rejected the API key; no title contains (a value holding the API key); 6767 of 6767 done\n"
	if got := errOut.String(); got != want {
		t.Errorf("standard error %q,\nwant %q", got, want)
	}
	errOut.Reset()
	rep = reporter.New(&out, &errOut)
	rep.Redact(`a"b`)
	rep.Error(`the option --title needs a value, not "a\"b" or "c"`)
	if got := errOut.String(); strings.Contains(got, `a\"b`) || !strings.Contains(got, `"c"`) {
		t.Errorf("standard error %q, want the escaped key hidden and the rest kept", got)
	}
}
