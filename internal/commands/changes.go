package commands

import (
	"context"
	"errors"

	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/bazarr"
	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/invocation"
	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/reporter"
	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/selection"
)

// changeCommand is what a command that changes Bazarr gives the run it
// shares with the others: its words, its request, and its skip rule.
type changeCommand struct {
	// done is the outcome of an accepted request, wouldDo its word under
	// --dry-run (P3).
	done, wouldDo reporter.Outcome
	// question asks for confirmation of the selected subtitles, such as
	// "Delete them" (G12).
	question string
	// send asks Bazarr once to make the change to sub.
	send func(ctx context.Context, c *bazarr.Client, sub bazarr.Subtitle) (bazarr.ChangeResult, error)
	// skip, when set, makes the run's skip rule from the library as read.
	// The rule is asked for each selected subtitle, in order, just before
	// the run acts on it or reports it under --dry-run, and returns why the
	// subtitle is skipped, or "" to act on it. It may remember the subtitles
	// it let through.
	skip func(lib bazarr.Library) func(bazarr.Subtitle) reporter.SkipReason
}

var (
	// errNotConfirmed: the user answered other than yes (G12).
	errNotConfirmed = errors.New("not confirmed; nothing changed")
	// errNeedsYes: standard input is not a terminal, so nobody can confirm (G12).
	errNeedsYes = errors.New("standard input is not a terminal: pass --yes to go ahead, or --dry-run to see what would be done; nothing changed")
	// errSomeNotDone: a subtitle was reported failed or unconfirmed (G6);
	// its line and the totals already say so.
	errSomeNotDone = errors.New("some subtitles were not changed")
)

// runChanges checks the connection, reads the library, selects, confirms,
// and then acts on each selected subtitle in P2's order, one request at a
// time, reporting each outcome as it is known (G12–G14). A stop, or Bazarr
// unreachable, at any point ends the run with the totals for what finished
// (G3, G4).
func runChanges(ctx context.Context, conn bazarr.Connection, req *invocation.Request, rep *reporter.Reporter, cmd changeCommand) error {
	err := changes(ctx, conn, req, rep, cmd)
	var unreachable *bazarr.UnreachableError
	isUnreachable := errors.As(err, &unreachable)
	if err != nil && !errors.Is(err, errSomeNotDone) && !isUnreachable && ctx.Err() != nil {
		// A stop wins over whatever else ended the run at the same moment,
		// an empty selection or an answer other than yes included.
		err = bazarr.ErrStopped
	}
	switch {
	case err == nil, errors.Is(err, errSomeNotDone), errors.Is(err, errEmptySelection),
		errors.Is(err, bazarr.ErrStopped), isUnreachable:
		rep.Totals()
	}
	// A failed connection check (G2), an unmatched title or ID (G8), and a
	// confirmation not given (G12) end the run before it starts: no totals.
	return err
}

func changes(ctx context.Context, conn bazarr.Connection, req *invocation.Request, rep *reporter.Reporter, cmd changeCommand) error {
	client, err := bazarr.Connect(ctx, conn)
	if err != nil {
		return err
	}
	lib, _, err := client.Library(ctx)
	if err != nil {
		return err
	}
	subs, err := selection.Select(req.Selection, lib)
	if err != nil {
		return err
	}
	if len(subs) == 0 {
		return errEmptySelection
	}
	dryRun := req.Change.DryRun
	if !dryRun && !req.Change.Yes {
		switch rep.Confirm(ctx, len(subs), cmd.question) {
		case reporter.NotTerminal:
			return errNeedsYes
		case reporter.No:
			return errNotConfirmed
		case reporter.Stopped:
			return bazarr.ErrStopped
		}
	}
	rep.Selected(len(subs))
	var skip func(bazarr.Subtitle) reporter.SkipReason
	if cmd.skip != nil {
		skip = cmd.skip(lib)
	}
	notDone := false
	for _, sub := range subs {
		// A stop is noticed between subtitles too, with no request in flight (G4).
		if ctx.Err() != nil {
			return bazarr.ErrStopped
		}
		if skip != nil {
			if reason := skip(sub); reason != "" {
				rep.Skip(sub, reason, dryRun)
				continue
			}
		}
		if dryRun {
			rep.Result(cmd.wouldDo, sub, "")
			continue
		}
		res, err := cmd.send(ctx, client, sub)
		switch res.Outcome {
		case bazarr.Accepted:
			rep.Result(cmd.done, sub, "")
		case bazarr.Refused:
			rep.Result(reporter.Failed, sub, res.Reason)
			notDone = true
		case bazarr.Unanswered, bazarr.Stopped:
			// Bazarr may have made the change: never sent again (G24, G27).
			rep.Result(reporter.Unconfirmed, sub, "")
			notDone = true
		}
		// NotSent: a stop came before the request; the subtitle has no outcome.
		if err != nil {
			return err
		}
	}
	if notDone {
		return errSomeNotDone
	}
	return nil
}
