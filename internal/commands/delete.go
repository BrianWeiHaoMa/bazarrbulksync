package commands

import (
	"context"

	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/bazarr"
	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/reporter"
)

// deleteCommand asks Bazarr to delete each selected subtitle (G27); it
// skips none.
var deleteCommand = changeCommand{
	done:     reporter.Deleted,
	wouldDo:  reporter.WouldDelete,
	question: "Delete them",
	send: func(ctx context.Context, c *bazarr.Client, sub bazarr.Subtitle) (bazarr.ChangeResult, error) {
		return c.Delete(ctx, sub)
	},
}
