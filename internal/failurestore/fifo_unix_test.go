//go:build unix

package failurestore_test

import (
	"os"
	"path/filepath"
	"syscall"
)

func makeFIFO(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return syscall.Mkfifo(path, 0o600)
}
