// Package reporter is the only part of bazarrbulksync that writes standard
// output or standard error (R1). Result lines and totals go to standard
// output; errors and notes go to standard error (G5). Nothing it writes
// holds color or cursor movement.
package reporter

import (
	"io"
	"os"
	"strings"
)

// Reporter writes what a run reports.
type Reporter struct {
	out    io.Writer
	errOut io.Writer
	// secrets are values no message may show, such as the API key (R2).
	secrets []string
	// outErr is the first failed write to standard output.
	outErr error
}

// Standard reports on the process's standard output and standard error.
func Standard() *Reporter { return New(os.Stdout, os.Stderr) }

// New reports on out (standard output) and errOut (standard error).
func New(out, errOut io.Writer) *Reporter { return &Reporter{out: out, errOut: errOut} }

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
	for _, s := range r.secrets {
		text = strings.ReplaceAll(text, s, "(the API key)")
	}
	_, _ = io.WriteString(r.errOut, text)
}

func (r *Reporter) writeOut(line string) { r.write(line + "\n") }

func (r *Reporter) write(text string) {
	if r.outErr == nil {
		_, r.outErr = io.WriteString(r.out, text)
	}
}
