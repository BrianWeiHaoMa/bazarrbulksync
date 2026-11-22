//go:build !linux

package main_test

import (
	"errors"
	"time"
)

// runAtTerminalHere needs a pseudo-terminal, which these checks open only
// on Linux, where the bed runs.
func runAtTerminalHere(string, runEnv, string, []string, time.Duration) (result, error) {
	return result{}, errors.New("a run at a terminal on this machine needs Linux")
}
