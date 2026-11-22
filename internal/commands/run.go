// Package commands is the composed program: it turns the command line into
// a request, checks the connection, runs the command, and reports why the
// command stopped, returning the exit code (G6). Each command is one file
// beside list.go and one case in Run.
package commands

import (
	"context"
	"errors"
	"fmt"

	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/bazarr"
	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/invocation"
	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/reporter"
)

// Exit codes (G6).
const (
	exitOK    = 0
	exitFail  = 1
	exitUsage = 2
)

// Env is what a run takes from outside the command line.
type Env struct {
	Getenv   func(string) string
	Reporter *reporter.Reporter
	// Clock and Limits default to the client's; tests set them.
	Clock  bazarr.Clock
	Limits bazarr.Limits
}

// errEmptySelection marks a selection that matched no external subtitle (G9).
var errEmptySelection = errors.New("no external subtitle matches the selection")

// Run runs the command line args, the words after the program's name, and
// returns the exit code. ctx is the stop: when it ends, the request in
// flight is abandoned and the command stops (G4).
func Run(ctx context.Context, args []string, env Env) int {
	rep := env.Reporter
	for _, key := range invocation.APIKeys(args, env.Getenv) {
		rep.Redact(key)
	}
	code := run(ctx, args, env)
	if code != exitOK {
		return code
	}
	if ctx.Err() != nil {
		// A stop while the last lines were written still ends the command
		// nonzero (G4).
		rep.Error("stopped")
		return exitFail
	}
	if err := rep.OutputError(); err != nil {
		rep.Error(fmt.Sprintf("cannot write standard output: %v", err))
		return exitFail
	}
	return exitOK
}

func run(ctx context.Context, args []string, env Env) int {
	rep := env.Reporter
	res, err := invocation.Parse(args, env.Getenv)
	var usage *invocation.UsageError
	if errors.As(err, &usage) {
		rep.UsageError(usage.Error(), usage.Hint())
		return exitUsage
	}
	if err != nil {
		rep.Error(err.Error())
		return exitUsage
	}
	if res.Request == nil {
		rep.Text(res.Text)
		return exitOK
	}
	req := res.Request
	conn := req.Connection
	conn.Clock, conn.Limits = env.Clock, env.Limits
	switch req.Command {
	case invocation.CommandList:
		err = list(ctx, conn, req, rep)
	case invocation.CommandSync:
		err = runSync(ctx, conn, req, rep)
	case invocation.CommandFix:
		err = runChanges(ctx, conn, req, rep, fixCommand(req.Fix))
	case invocation.CommandDelete:
		err = runChanges(ctx, conn, req, rep, deleteCommand)
	default:
		err = fmt.Errorf("the command %s is not built", req.Command)
	}
	return finish(err, rep)
}

// finish reports why the command stopped, and returns the exit code.
func finish(err error, rep *reporter.Reporter) int {
	switch {
	case err == nil:
		return exitOK
	case errors.Is(err, errEmptySelection):
		rep.Note(err.Error())
		return exitOK
	case errors.Is(err, errSomeNotDone):
		return exitFail
	case errors.Is(err, bazarr.ErrStopped):
		rep.Error("stopped")
	default:
		// The client's and the selection's errors name the address, the
		// cause, or the unmatched values themselves (G2, G3, G8).
		rep.Error(err.Error())
	}
	return exitFail
}
