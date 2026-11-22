package commands

import (
	"context"

	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/bazarr"
	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/failurestore"
	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/invocation"
	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/reporter"
	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/selection"
)

// runSync runs `sync` in the run the changing commands share. Without
// --dry-run it hands the selected subtitles to Bazarr as its slots allow
// (runSyncs). With --dry-run it judges each selected subtitle in P2's
// order from Bazarr as read just then and reports whether it would be
// submitted or why not (G13), sending no changing request and writing no
// state file.
func runSync(ctx context.Context, conn bazarr.Connection, req *invocation.Request, rep *reporter.Reporter) error {
	clock := conn.Clock
	if clock == nil {
		clock = bazarr.SystemClock{}
	}
	return runChanges(ctx, conn, req, rep, changeCommand{
		question: "Sync them",
		run: func(ctx context.Context, c *bazarr.Client, subs []selection.Selected) error {
			store := loadStore(req.Sync, conn.URL, rep)
			if !req.Change.DryRun {
				return runSyncs(ctx, c, clock, req.Sync, store, rep, subs)
			}
			for _, sel := range subs {
				// A stop is noticed between subtitles too (G4).
				if ctx.Err() != nil {
					return bazarr.ErrStopped
				}
				j, err := judge(ctx, c, clock, sel, req.Sync, store)
				if err != nil {
					return err
				}
				if j.Skip != "" {
					rep.Skip(j.Subtitle, j.Skip, true)
				} else {
					rep.Result(reporter.WouldSync, j.Subtitle, "")
				}
			}
			return nil
		},
	})
}

// loadStore loads the recorded failures of address from the state file,
// and says once when the store cannot be used, after which it answers that
// no subtitle has a record (G23). Loading writes nothing (G13).
func loadStore(opts invocation.SyncOptions, address string, rep *reporter.Reporter) *failurestore.Store {
	store := failurestore.Load(opts.StateFile, address)
	switch {
	case opts.StateFile == "":
		rep.Error("the state file cannot be used: its default path cannot be resolved (" + opts.StateFileProblem +
			"); going on as if no failure were recorded")
	case store.Err() != nil:
		rep.Error("the state file cannot be used: " + store.Err().Error() + "; going on as if no failure were recorded")
	}
	return store
}
