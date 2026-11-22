package reporter

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/bazarr"
)

// Outcome is the word a result line opens with (P3).
type Outcome string

// The outcomes of `delete` (G27, G13).
const (
	Deleted     Outcome = "deleted"
	Failed      Outcome = "failed"
	Unconfirmed Outcome = "unconfirmed"
	WouldDelete Outcome = "would delete"
)

// The outcomes of `fix` besides failed and unconfirmed (G24, G13).
const (
	Applied    Outcome = "applied"
	WouldApply Outcome = "would apply"
)

// The outcomes of `sync` besides failed and unconfirmed (G19, G13).
const (
	Synced    Outcome = "synced"
	WouldSync Outcome = "would sync"
)

// SkipReason is why a run skips a subtitle (P3).
type SkipReason string

// The skip reasons of `sync`, in P3's order, which is the order a sync's
// judging tries them in (G15, G18).
const (
	// SyncInBazarr: Bazarr's queue shows a sync of the subtitle; under
	// --dry-run only, since a real run waits for it.
	SyncInBazarr      SkipReason = "sync in Bazarr"
	Forced            SkipReason = "forced"
	CurrentlySynced   SkipReason = "currently synced"
	EarlierFailure    SkipReason = "earlier failure"
	ReferenceLanguage SkipReason = "reference language"
	NoReference       SkipReason = "no reference subtitle"
)

// ReplacesNonHI: `fix remove-hi` skips a subtitle whose fix would have
// Bazarr replace another subtitle (G25).
const ReplacesNonHI SkipReason = "replaces non-HI subtitle"

// wouldSkip opens the line of a subtitle a dry run would skip (P3).
const wouldSkip = "would skip"

// tally is what a run of a changing command has reported so far.
type tally struct {
	// selected is the number of subtitles selected, or -1 before the
	// selection is known.
	selected int
	done     int
	// labels are the outcomes and skip reasons counted, in the order each
	// first occurred; counts holds each one's count.
	labels []string
	counts map[string]int
	// drawn is the progress text on a terminal's standard error, which must
	// be erased before anything else is written; empty when none is.
	drawn string
	// shown is the done count the last progress line showed.
	shown int
}

// Selected starts the run's progress: n subtitles are selected (G14).
func (r *Reporter) Selected(n int) {
	r.run.selected = n
	r.drawProgress()
}

// Result writes the line of a subtitle the run acted on, or under
// --dry-run would act on: the outcome, the subtitle as `list` prints it, and
// the reason when there is one (P3, G14). It counts the subtitle as done.
func (r *Reporter) Result(o Outcome, s bazarr.Subtitle, reason string) {
	r.writeOut(resultLine(string(o), s, reason))
	r.count(string(o))
}

// Skip counts a subtitle the run skipped, under its reason, as done (P3).
// Under --dry-run it also writes the subtitle's `would skip` line; without
// it, a skip has no line.
func (r *Reporter) Skip(s bazarr.Subtitle, reason SkipReason, dryRun bool) {
	if dryRun {
		r.writeOut(resultLine(wouldSkip, s, string(reason)))
	}
	r.count("skipped (" + string(reason) + ")")
}

func resultLine(word string, s bazarr.Subtitle, reason string) string {
	line := word + "  " + subtitleLine(s)
	if reason != "" {
		line += "  (" + plain(reason) + ")"
	}
	return line
}

func (r *Reporter) count(label string) {
	t := &r.run
	if t.counts == nil {
		t.counts = map[string]int{}
	}
	if t.counts[label] == 0 {
		t.labels = append(t.labels, label)
	}
	t.counts[label]++
	t.done++
	r.drawProgress()
}

// Totals writes the run's totals: how many subtitles it got to, of those
// selected, then the count of each outcome and skip reason that occurred
// (G14). Before the selection is known, every count is zero (G4).
func (r *Reporter) Totals() {
	t := &r.run
	if !r.errTerminal && t.selected > 0 && t.shown != t.done {
		// A stop can come between two sampled lines: the count still shows.
		r.writeProgressLine()
	}
	noun := "subtitles"
	if t.selected == 1 || (t.selected < 0 && t.done == 1) {
		noun = "subtitle"
	}
	r.writeOut("")
	if t.selected < 0 {
		r.writeOut(fmt.Sprintf("Total: %d %s", t.done, noun))
	} else {
		r.writeOut(fmt.Sprintf("Total: %d of %d selected %s", t.done, t.selected, noun))
	}
	if len(t.labels) > 0 {
		counts := make([]string, len(t.labels))
		for i, l := range t.labels {
			counts[i] = plain(l) + " " + strconv.Itoa(t.counts[l])
		}
		r.writeOut(strings.Join(counts, ", "))
	}
}

// progressSteps bounds how many progress lines a standard error that is not
// a terminal receives in one run.
const progressSteps = 100

// drawProgress shows how many subtitles are done out of those selected
// (G14): redrawn in place when standard error is a terminal, otherwise as a
// plain line at most progressSteps times a run, and always for the last (P3).
func (r *Reporter) drawProgress() {
	t := &r.run
	if t.selected <= 0 {
		return
	}
	if r.errTerminal {
		r.clearProgress()
		text := progressText(t)
		_, _ = io.WriteString(r.errOut, text)
		t.drawn, t.shown = text, t.done
		return
	}
	step := max(1, t.selected/progressSteps)
	if t.done > 0 && (t.done%step == 0 || t.done == t.selected) {
		r.writeProgressLine()
	}
}

func (r *Reporter) writeProgressLine() {
	r.writeErr(progressText(&r.run) + "\n")
	r.run.shown = r.run.done
}

func progressText(t *tally) string { return fmt.Sprintf("%d of %d subtitles done", t.done, t.selected) }

// clearProgress erases the progress line from a terminal, so that the next
// line written to either stream starts at its left edge. It overwrites the
// line with spaces, which every terminal shows as they are; an erase escape
// sequence needs a mode the Windows console leaves off by default.
func (r *Reporter) clearProgress() {
	if text := r.run.drawn; text != "" {
		r.run.drawn = ""
		_, _ = io.WriteString(r.errOut, "\r"+strings.Repeat(" ", len(text))+"\r")
	}
}
