package commands

import (
	"cmp"
	"context"
	"slices"
	"strings"

	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/bazarr"
	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/failurestore"
	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/invocation"
	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/reporter"
	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/selection"
)

// judgment is what judging one subtitle found: its verdict, and what the
// run needs of the same reads, so that nothing is read or decided twice
// between the judging and a submission.
type judgment struct {
	// Subtitle is the subtitle as its video now shows it, or as selected
	// when the video no longer shows it (Shown false).
	Subtitle bazarr.Subtitle
	Shown    bool
	// Skip is why the subtitle is not submitted now, the first reason that
	// applies in P3's order, or "" to submit it. SyncInBazarr means the
	// queue showed a sync job of it (G18).
	Skip reporter.SkipReason
	// Options are the sync options its request carries, with a lang:CODE
	// reference resolved to the reference subtitle's path.
	Options bazarr.SyncOptions
	// Baseline is Bazarr's start time and the identities of the subtitle's
	// events of kinds 1 to 6, newest first, as read for this verdict.
	Baseline baseline
	// Queues are the queue reads the judging made that a five-second rule
	// can still consult, in order, those of a pass it started again
	// included.
	Queues []bazarr.Queue
	// Lapsed is set when the path had a recorded failure that the events
	// show lapsed (G21).
	Lapsed bool
}

// baseline is what G19 compares a sync's outcome against.
type baseline struct {
	StartTime  string
	Identities []string
}

// judge judges sel from Bazarr as read now (G15, G18): it reads, in this
// order, the video, the queue, the subtitle's events, and Bazarr's start
// time, and decides from those reads alone. When a read needed a retry and
// the reads together took longer than the read limit, which every read
// carries, it starts again from the video. store answers the recorded
// failures; an unusable one has none (G23).
func judge(ctx context.Context, c *bazarr.Client, clock bazarr.Clock, sel selection.Selected,
	opts invocation.SyncOptions, store *failurestore.Store) (judgment, error) {
	var j judgment
	for {
		start := clock.Now()
		video, retried, err := c.VideoNow(ctx, sel.Subtitle)
		if err != nil {
			return j, err
		}
		queue, r, err := c.Queue(ctx)
		retried = retried || r
		if err != nil {
			return j, err
		}
		j.Queues = append(j.Queues, queue)
		j.Queues = slices.Delete(j.Queues, 0, unconsulted(j.Queues))
		events, r, err := c.Events(ctx, sel.Subtitle, sel.Others)
		retried = retried || r
		if err != nil {
			return j, err
		}
		startTime, r, err := c.StartTime(ctx)
		retried = retried || r
		if err != nil {
			return j, err
		}
		if retried && clock.Now().Sub(start) > c.ReadLimit() {
			continue
		}
		j.Subtitle, j.Shown = sel.Subtitle, false
		if i := slices.IndexFunc(video.Subtitles, func(s bazarr.Subtitle) bool { return s.Path == sel.Path }); i >= 0 {
			j.Subtitle, j.Shown = video.Subtitles[i], true
		}
		j.Baseline = baseline{StartTime: startTime, Identities: events.Identities()}
		j.Options = opts.Bazarr
		recorded, hasRecord := store.Lookup(sel.Path)
		j.Lapsed = hasRecord && slices.ContainsFunc(j.Baseline.Identities, func(id string) bool {
			return !slices.Contains(recorded, id)
		})
		var reference string
		if opts.ReferenceLanguage != "" {
			reference = referenceSubtitle(video.Subtitles, opts.ReferenceLanguage)
			j.Options.Reference = reference
		}
		j.Skip = ""
		switch {
		case queue.HasSyncOf(sel.Path):
			j.Skip = reporter.SyncInBazarr
		case j.Subtitle.Forced:
			j.Skip = reporter.Forced
		case events.CurrentlySynced() && !opts.Resync:
			j.Skip = reporter.CurrentlySynced
		case hasRecord && !j.Lapsed && !opts.RetryFailed:
			j.Skip = reporter.EarlierFailure
		case opts.ReferenceLanguage != "" && strings.EqualFold(j.Subtitle.Language, opts.ReferenceLanguage):
			j.Skip = reporter.ReferenceLanguage
		case opts.ReferenceLanguage != "" && reference == "":
			j.Skip = reporter.NoReference
		}
		return j, nil
	}
}

// referenceSubtitle is F8's reference subtitle for lang:CODE among the
// external subtitles a video shows: one in language code, ignoring case as
// F6's codes are, that is not forced; the one that is not hearing-impaired
// when both exist; among several such, the first path in byte order. It
// returns that subtitle's path, or "" when the video has none.
func referenceSubtitle(subs []bazarr.Subtitle, code string) string {
	var best *bazarr.Subtitle
	for i, s := range subs {
		if s.Forced || !strings.EqualFold(s.Language, code) {
			continue
		}
		if best == nil || cmp.Or(compareHI(s.HI, best.HI), strings.Compare(s.Path, best.Path)) < 0 {
			best = &subs[i]
		}
	}
	if best == nil {
		return ""
	}
	return best.Path
}

// compareHI orders a subtitle that is not hearing-impaired first.
func compareHI(a, b bool) int {
	switch {
	case a == b:
		return 0
	case !a:
		return -1
	}
	return 1
}
