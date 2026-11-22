// Package reporter is the only part of bazarrbulksync that writes standard
// output or standard error or reads standard input (R1). Result lines and
// totals go to standard output; prompts, progress, errors, and notes go to
// standard error (G5). Neither carries color or an escape sequence;
// standard error carries a carriage return only to redraw progress on a
// terminal.
package reporter

import (
	"io"
	"os"
	"strconv"
	"strings"
)

// Reporter writes what a run reports, and reads the answer to its prompt.
type Reporter struct {
	in          io.Reader
	out         io.Writer
	errOut      io.Writer
	inTerminal  bool
	errTerminal bool
	// secrets are values no message may show, such as the API key (R2).
	secrets []string
	// outErr is the first failed write to standard output.
	outErr error
	run    tally
}

// Streams are the standard streams a Reporter uses, and whether standard
// input and standard error are terminals.
type Streams struct {
	In          io.Reader
	Out, Err    io.Writer
	InTerminal  bool
	ErrTerminal bool
}

// Standard reports on the process's standard streams, asking the operating
// system which of them are terminals.
func Standard() *Reporter {
	return FromStreams(Streams{In: os.Stdin, Out: os.Stdout, Err: os.Stderr,
		InTerminal: isTerminal(os.Stdin), ErrTerminal: isTerminal(os.Stderr)})
}

// New reports on out (standard output) and errOut (standard error), neither
// a terminal, with standard input not a terminal.
func New(out, errOut io.Writer) *Reporter { return FromStreams(Streams{Out: out, Err: errOut}) }

// FromStreams reports on s.
func FromStreams(s Streams) *Reporter {
	return &Reporter{in: s.In, out: s.Out, errOut: s.Err, inTerminal: s.InTerminal, errTerminal: s.ErrTerminal,
		run: tally{selected: -1}}
}

// Redact makes every later message on standard error show secret as a
// placeholder, whatever part of the program put it in the message.
func (r *Reporter) Redact(secret string) {
	if secret != "" {
		r.secrets = append(r.secrets, secret)
	}
}

// Text writes help or version text to standard output as it is.
func (r *Reporter) Text(text string) { r.write(text) }

// OutputError returns the first failed write to standard output, after
// which nothing more was written there; nil when every write succeeded.
func (r *Reporter) OutputError() error { return r.outErr }

// UsageError writes G1's usage error and where the usage is.
func (r *Reporter) UsageError(problem, hint string) {
	r.message(problem)
	r.writeErr(hint + "\n")
}

// Error writes a message saying why the command stopped.
func (r *Reporter) Error(message string) { r.message(message) }

// Note writes a message that is not a result, such as an empty selection.
func (r *Reporter) Note(message string) { r.message(message) }

func (r *Reporter) message(message string) {
	r.writeErr("bazarrbulksync: " + plain(message) + "\n")
}

func (r *Reporter) writeErr(text string) {
	r.clearProgress()
	_, _ = io.WriteString(r.errOut, r.hideSecrets(text))
}

// hideSecrets replaces each double-quoted value in text that holds a secret.
// Only quoted values come from outside the program's own words (an argument
// or a title quoted back), so the address and the program's words stay
// readable even when a short key's text occurs in them; Bazarr's answers are
// cleared of the key where the client reads them.
func (r *Reporter) hideSecrets(text string) string {
	if len(r.secrets) == 0 || !strings.Contains(text, `"`) {
		return text
	}
	var b strings.Builder
	for {
		start := strings.IndexByte(text, '"')
		if start < 0 {
			b.WriteString(text)
			return b.String()
		}
		end := quotedEnd(text, start)
		b.WriteString(text[:start])
		if quoted := text[start:end]; r.holdsSecret(quoted) {
			b.WriteString("(a value holding the API key)")
		} else {
			b.WriteString(quoted)
		}
		text = text[end:]
	}
}

// quotedEnd returns the index just past the quote closing the Go-quoted
// value that opens at start, or len(text) when it is not closed.
func quotedEnd(text string, start int) int {
	for i := start + 1; i < len(text); i++ {
		switch text[i] {
		case '\\':
			i++
		case '"':
			return i + 1
		}
	}
	return len(text)
}

func (r *Reporter) holdsSecret(quoted string) bool {
	unquoted, err := strconv.Unquote(quoted)
	if err != nil {
		unquoted = quoted
	}
	for _, s := range r.secrets {
		if strings.Contains(quoted, s) || strings.Contains(unquoted, s) {
			return true
		}
	}
	return false
}

func (r *Reporter) writeOut(line string) {
	r.clearProgress()
	r.write(line + "\n")
}

func (r *Reporter) write(text string) {
	if r.outErr == nil {
		_, r.outErr = io.WriteString(r.out, text)
	}
}
