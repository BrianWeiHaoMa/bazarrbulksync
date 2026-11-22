package reporter

import (
	"os"
	"syscall"
)

// isTerminal asks the operating system whether f is a console: the console
// mode can be read only from one. NUL and pipes are not consoles.
func isTerminal(f *os.File) bool {
	var mode uint32
	return syscall.GetConsoleMode(syscall.Handle(f.Fd()), &mode) == nil
}
