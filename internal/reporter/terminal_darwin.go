package reporter

import (
	"os"
	"syscall"
	"unsafe"
)

// isTerminal asks the operating system whether f is a terminal: the
// TIOCGETA query succeeds only on one. A file type never decides it, since
// /dev/null is a character device and not a terminal.
func isTerminal(f *os.File) bool {
	var t syscall.Termios
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), syscall.TIOCGETA, uintptr(unsafe.Pointer(&t)))
	return errno == 0
}
