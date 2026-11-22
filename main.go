// bazarrbulksync has Bazarr sync, fix, delete, or list the external
// subtitles of a whole library through Bazarr's HTTP API.

// On macOS and Windows, keep the platform's certificate verifier even when
// SSL_CERT_FILE or SSL_CERT_DIR is set, so no file on disk decides trust (F3).
//
//go:debug x509sslcertoverrideplatform=0
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/commands"
	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/reporter"
)

func main() {
	// Ctrl-C and SIGTERM (a container stop) end ctx, which every request is
	// bound to; the command then exits with its own code (G4, S8).
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := commands.Run(ctx, os.Args[1:], commands.Env{Getenv: os.Getenv, Reporter: reporter.Standard()})
	stop()
	os.Exit(code)
}
