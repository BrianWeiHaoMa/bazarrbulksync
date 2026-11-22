package commands

import (
	"context"

	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/bazarr"
	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/invocation"
	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/reporter"
	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/selection"
)

// list prints the selected subtitles, one line each, then the totals; with
// --json, one object each and no totals (G10, G11). It changes nothing.
func list(ctx context.Context, client *bazarr.Client, req *invocation.Request, rep *reporter.Reporter) error {
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
	for _, s := range subs {
		// A stop while printing a long list ends it there (G4).
		if ctx.Err() != nil {
			return bazarr.ErrStopped
		}
		if req.List.JSON {
			rep.SubtitleJSON(s)
		} else {
			rep.Subtitle(s)
		}
	}
	if !req.List.JSON {
		rep.ListTotals(subs)
	}
	return nil
}
