package commands

import (
	"context"
	"slices"
	"time"

	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/bazarr"
	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/failurestore"
	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/invocation"
	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/reporter"
	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/selection"
)

// queueGap is how far apart the queue reads the five-second rules compare
// must be: a job Bazarr moves from its pending to its running queue is
// shown in neither for less than that (S2, E3).
const queueGap = 5 * time.Second

// syncRun is one real `sync` run: one loop, at most one request in flight,
// that fills the run's slots with submissions, lets subtitles wait under
// G18 without a slot, follows each submitted sync to its outcome
// (G15 to G20), and keeps the state file's records (G21 to G23).
type syncRun struct {
	c     *bazarr.Client
	clock bazarr.Clock
	opts  invocation.SyncOptions
	store *failurestore.Store
	rep   *reporter.Reporter
	subs  []selection.Selected
	// next is the first selected subtitle not yet judged.
	next int
	// waiting are the subtitles to judge again, as indexes of subs in P2's
	// order: those waiting under G18, and those skipped for an earlier
	// failure once the store cannot be used. They hold no slot.
	waiting []int
	// skippedForFailure are the subtitles skipped for an earlier failure,
	// as indexes of subs, until the store becomes unusable (G23).
	skippedForFailure []int
	// followed are the submitted syncs, each holding a slot until its
	// outcome is reported (G17).
	followed []*followedSync
	// queues are the run's queue reads that a five-second rule can still
	// consult, in order: the judging's, and those made while waiting and
	// following (keep).
	queues  []bazarr.Queue
	notDone bool
}

// followedSync is a submitted sync the run follows to its outcome.
type followedSync struct {
	sel selection.Selected
	// sub is the subtitle as judged just before the submission.
	sub bazarr.Subtitle
	// base is what Bazarr showed just before the submission (State).
	base baseline
	// accepted is false when Bazarr never answered the request.
	accepted bool
	// from is the index in the run's queue reads of the first read sent
	// after the request returned: only such reads can tell that its sync
	// has ended.
	from int
}

// runSyncs submits the selected subtitles, in P2's order, as slots allow,
// and reports each outcome as it is known. It returns ErrStopped on a stop
// and *bazarr.UnreachableError when Bazarr stayed silent through G3; a sync
// still in Bazarr then gets no line (G4).
func runSyncs(ctx context.Context, c *bazarr.Client, clock bazarr.Clock, opts invocation.SyncOptions,
	store *failurestore.Store, rep *reporter.Reporter, subs []selection.Selected) error {
	r := &syncRun{c: c, clock: clock, opts: opts, store: store, rep: rep, subs: subs}
	if err := r.loop(ctx); err != nil {
		return err
	}
	if r.notDone {
		return errSomeNotDone
	}
	return nil
}

func (r *syncRun) loop(ctx context.Context) error {
	for {
		if ctx.Err() != nil {
			return bazarr.ErrStopped
		}
		if err := r.reportEnded(ctx); err != nil {
			return err
		}
		if r.next == len(r.subs) && len(r.waiting) == 0 && len(r.followed) == 0 {
			return nil
		}
		if len(r.followed) < r.opts.Concurrency {
			if i, ok := r.candidate(); ok {
				moved, err := r.handle(ctx, i)
				if err != nil {
					return err
				}
				if moved {
					continue
				}
			}
		}
		// Nothing can move until Bazarr's queue is read again, at least
		// five seconds after the last read was answered.
		if d := r.queues[len(r.queues)-1].Answered.Add(queueGap).Sub(r.clock.Now()); d > 0 {
			if err := r.clock.Sleep(ctx, d); err != nil {
				return bazarr.ErrStopped
			}
		}
		if len(r.followed) < r.opts.Concurrency && slices.ContainsFunc(r.waiting, r.ready) {
			continue // its judging reads the queue
		}
		q, _, err := r.c.Queue(ctx)
		if err != nil {
			return err
		}
		r.keep(q)
	}
}

// keep adds the queue reads qs to the run's, and drops those no
// five-second rule will consult again.
func (r *syncRun) keep(qs ...bazarr.Queue) {
	r.queues = append(r.queues, qs...)
	n := unconsulted(r.queues)
	if n == 0 {
		return
	}
	r.queues = slices.Delete(r.queues, 0, n)
	for _, f := range r.followed {
		f.from = max(f.from-n, 0)
	}
}

// unconsulted is how many of the queue reads qs, oldest first, no
// five-second rule will consult again: those before the latest read
// answered queueGap before the newest was sent. Every rule asks about a
// time no earlier than that and looks back no further than such a read.
func unconsulted(qs []bazarr.Queue) int {
	if len(qs) == 0 {
		return 0
	}
	newest := qs[len(qs)-1].Sent
	return max(slices.IndexFunc(qs, func(q bazarr.Queue) bool { return !gapBefore(q, newest) })-1, 0)
}

// gapBefore reports whether the queue read q was answered at least
// queueGap before at.
func gapBefore(q bazarr.Queue, at time.Time) bool {
	return !q.Answered.After(at.Add(-queueGap))
}

// candidate takes the next subtitle to judge, as an index of subs: the
// first waiting one that a queue read showing no sync of it would let
// through, else the next one not yet judged.
func (r *syncRun) candidate() (int, bool) {
	if w := slices.IndexFunc(r.waiting, r.ready); w >= 0 {
		i := r.waiting[w]
		r.waiting = slices.Delete(r.waiting, w, w+1)
		return i, true
	}
	if r.next < len(r.subs) {
		r.next++
		return r.next - 1, true
	}
	return 0, false
}

// ready reports whether the waiting subtitle subs[i] would be submitted
// were a queue read sent now to show no sync of it.
func (r *syncRun) ready(i int) bool {
	path := r.subs[i].Path
	if r.following(path) {
		return false
	}
	clear, _ := r.clearOf(path, 0, r.clock.Now())
	return clear
}

// following reports whether the run follows a sync of the subtitle at
// path: then no other subtitle at that path is submitted, whatever a queue
// read shows (G18; the episodes of a multi-episode file share one path).
func (r *syncRun) following(path string) bool {
	return slices.ContainsFunc(r.followed, func(f *followedSync) bool { return f.sel.Path == path })
}

// clearOf applies the five-second rule to the run's queue reads from the
// one at index from on: clear when those from one answered at least
// queueGap before at up to the latest all show no sync job of path. seen is
// set when one of the reads it looked at showed one.
func (r *syncRun) clearOf(path string, from int, at time.Time) (clear, seen bool) {
	for _, q := range slices.Backward(r.queues[from:]) {
		if q.HasSyncOf(path) {
			return false, true
		}
		if gapBefore(q, at) {
			return true, false
		}
	}
	return false, false
}

// handle judges subs[i] from Bazarr as read just now and acts on the
// verdict (act); a record the judging found lapsed is then removed.
func (r *syncRun) handle(ctx context.Context, i int) (moved bool, err error) {
	sel := r.subs[i]
	if r.following(sel.Path) {
		r.wait(i)
		return true, nil
	}
	j, err := judge(ctx, r.c, r.clock, sel, r.opts, r.store)
	r.keep(j.Queues...)
	if err != nil {
		return false, err
	}
	moved, err = r.act(ctx, i, j)
	if j.Lapsed {
		// Removed at once (State), but after the act: nothing may come
		// between the judging's reads and a submission (G15, G18).
		r.wrote(r.store.Forget(sel.Path))
	}
	return moved, err
}

// act skips subs[i], submits it, or lets it wait under G18, as the judgment
// j says. moved is false when the subtitle waits only for the five-second
// rule, which no other subtitle can meet sooner, so the run lets time pass.
func (r *syncRun) act(ctx context.Context, i int, j judgment) (moved bool, err error) {
	sel := r.subs[i]
	switch j.Skip {
	case "":
	case reporter.SyncInBazarr:
		r.wait(i)
		return true, nil
	default:
		r.rep.Skip(j.Subtitle, j.Skip, false)
		if j.Skip == reporter.EarlierFailure {
			r.skippedForFailure = append(r.skippedForFailure, i)
		}
		return true, nil
	}
	clear, seen := r.clearOf(sel.Path, 0, j.Queues[len(j.Queues)-1].Sent)
	if !clear {
		r.wait(i)
		return seen, nil
	}
	return true, r.submit(ctx, sel, j)
}

// wait puts subs[i] among the waiting subtitles, in P2's order.
func (r *syncRun) wait(i int) {
	w, _ := slices.BinarySearch(r.waiting, i)
	r.waiting = slices.Insert(r.waiting, w, i)
}

// submit sends the subtitle's one sync request (G16). The slot is taken
// before the request is sent: the loop judges only while one is free.
func (r *syncRun) submit(ctx context.Context, sel selection.Selected, j judgment) error {
	res, err := r.c.Sync(ctx, j.Subtitle, j.Options)
	switch res.Outcome {
	case bazarr.Refused:
		// Refused, not run: never recorded (G21).
		r.rep.Result(reporter.Failed, j.Subtitle, res.Reason)
		r.notDone = true
	case bazarr.Accepted, bazarr.Unanswered:
		if err == nil {
			r.followed = append(r.followed, &followedSync{sel: sel, sub: j.Subtitle, base: j.Baseline,
				accepted: res.Outcome == bazarr.Accepted, from: len(r.queues)})
		}
	}
	// Stopped and NotSent, and a stop or an unreachable Bazarr after the
	// request: the sync, if any, is left to Bazarr without a line (G4).
	return err
}

// reportEnded reports the outcome of each followed sync that has ended:
// two queue reads sent since its submission, the later sent at least five
// seconds after the earlier was answered, and every read between, show no
// sync job of it. It then reads the events, then the start time (R4), and
// reports the first outcome that applies (G19, G20).
func (r *syncRun) reportEnded(ctx context.Context) error {
	if len(r.queues) == 0 {
		return nil
	}
	last := r.queues[len(r.queues)-1]
	for i := 0; i < len(r.followed); {
		f := r.followed[i]
		if clear, _ := r.clearOf(f.sel.Path, f.from, last.Sent); !clear {
			i++
			continue
		}
		events, _, err := r.c.Events(ctx, f.sel.Subtitle, f.sel.Others)
		if err != nil {
			return err
		}
		startTime, _, err := r.c.StartTime(ctx)
		if err != nil {
			return err
		}
		synced, restarted := newSync(events, f.base), bazarr.Restarted(f.base.StartTime, startTime)
		if synced || restarted || !f.accepted {
			// Not a failure Bazarr ran, which records the path anew: the
			// events just read may show the path's record lapsed (State).
			r.forgetIfLapsed(f.sel.Path, events.Identities())
		}
		switch {
		case synced:
			r.rep.Result(reporter.Synced, f.sub, "")
		case restarted:
			r.rep.Result(reporter.Unconfirmed, f.sub, "Bazarr restarted")
			r.notDone = true
		case f.accepted:
			// Bazarr ran the sync and recorded none (G19's second case),
			// unlike a refusal, which submit reports. The failure is on disk
			// before its line, so that no kill after the line loses it (G22).
			if r.store.Err() == nil {
				r.wrote(r.store.Record(f.sel.Path, f.base.Identities))
			}
			r.rep.Result(reporter.Failed, f.sub, "Bazarr recorded no sync")
			r.notDone = true
		default:
			r.rep.Result(reporter.Unconfirmed, f.sub, "Bazarr never answered the request")
			r.notDone = true
		}
		r.followed = slices.Delete(r.followed, i, i+1)
	}
	return nil
}

// forgetIfLapsed removes the record of path when the events' identities
// show it lapsed (G21).
func (r *syncRun) forgetIfLapsed(path string, identities []string) {
	if recorded, ok := r.store.Lookup(path); ok && lapsed(recorded, identities) {
		r.wrote(r.store.Forget(path))
	}
}

// wrote takes the result of a write of the state file. When the write
// failed, the store has become unusable: the run says so, once, and from
// then on runs as if no failure were recorded, judging again each subtitle
// it skipped for an earlier failure, which is no longer counted as done
// (G23, Failure store).
func (r *syncRun) wrote(err error) {
	if err == nil {
		return
	}
	storeUnusable(r.rep, err.Error())
	for _, i := range r.skippedForFailure {
		r.rep.Unskip(reporter.EarlierFailure)
		r.wait(i)
	}
	r.skippedForFailure = nil
}

// newSync reports whether events hold a sync event that base does not.
func newSync(events bazarr.Events, base baseline) bool {
	return slices.ContainsFunc(events, func(e bazarr.Event) bool {
		return e.Kind == bazarr.EventSync && !slices.Contains(base.Identities, e.Identity())
	})
}
