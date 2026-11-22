package reporter

import (
	"bufio"
	"context"
	"fmt"
	"strings"
)

// Answer is how the confirmation of G12 ended.
type Answer int

const (
	// Yes: the user typed y or yes, in any case (P3).
	Yes Answer = iota + 1
	// No: any other answer, or the end of input.
	No
	// NotTerminal: standard input is not a terminal, so nobody was asked.
	NotTerminal
	// Stopped: a stop ended the prompt before an answer came (G4).
	Stopped
)

// Confirm asks, on standard error, whether to go on with the selected
// subtitles: "3 subtitles selected. <question>? [y/N] ". It asks only when
// standard input is a terminal, and a stop (ctx ending) ends the wait
// without a keystroke.
func (r *Reporter) Confirm(ctx context.Context, selected int, question string) Answer {
	if !r.inTerminal {
		return NotTerminal
	}
	noun := "subtitles"
	if selected == 1 {
		noun = "subtitle"
	}
	r.writeErr(fmt.Sprintf("%d %s selected. %s? [y/N] ", selected, noun, question))
	answer := make(chan Answer, 1)
	go func() {
		// The read cannot be interrupted; a stop leaves it waiting until the
		// process exits.
		line, err := bufio.NewReader(r.in).ReadString('\n')
		// Only the line's end is dropped: " y" is not y (P3, G12).
		switch word := strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r"); {
		case err != nil:
			answer <- No // the end of input, even after a partial line
		case strings.EqualFold(word, "y") || strings.EqualFold(word, "yes"):
			answer <- Yes
		default:
			answer <- No
		}
	}()
	select {
	case a := <-answer:
		return a
	case <-ctx.Done():
		r.writeErr("\n")
		return Stopped
	}
}
