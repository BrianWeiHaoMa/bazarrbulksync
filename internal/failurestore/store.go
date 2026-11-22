// Package failurestore is the memory of failed syncs (G21): the state file
// of one user, holding for each Bazarr address the subtitle paths whose
// sync Bazarr ran and ended without a sync event, each with the event
// identities of the baseline read before that sync was submitted. It is
// the only code that opens a file, and it prints nothing: a caller is
// handed the cause that made the store unusable and says it once.
package failurestore

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Store is the records of one address in one state file, as loaded. One
// run at a time uses a state file (E2); a Store is not safe for concurrent
// use.
type Store struct {
	path    string
	address string
	// servers is the whole state: records keyed by address, then by path.
	servers map[string]map[string][]string
	// pending is set when the state file was missing and its temporary
	// file held a complete state, which the next write renames into place
	// before it writes anything else.
	pending bool
	err     error
}

// Load reads the records of address, as the user gave it less its trailing
// slashes, from the state file at path. It never creates, writes, renames,
// or removes anything, so a dry run that loads leaves the disk as it was.
// A state file that cannot be read or is not a state file gives an
// unusable store (Err).
func Load(path, address string) *Store {
	s := &Store{path: path, address: strings.TrimRight(address, "/"), servers: map[string]map[string][]string{}}
	if path == "" {
		s.fail(errors.New("no path"))
		return s
	}
	b, err := readRegular(path)
	if errors.Is(err, fs.ErrNotExist) {
		// While there is no state file, a complete temporary file is the
		// state: a kill can fall between its close and its rename.
		if b, err := readRegular(s.tmp()); err == nil {
			if servers, err := decode(b); err == nil {
				s.servers, s.pending = servers, true
			}
		}
		return s
	}
	if err != nil {
		s.fail(fmt.Errorf("cannot read it: %w", unwrapPath(err)))
		return s
	}
	servers, err := decode(b)
	if err != nil {
		s.fail(fmt.Errorf("not a state file: %w", err))
		return s
	}
	s.servers = servers
	return s
}

// readRegular reads the regular file at path, refusing anything else
// before opening it: opening a named pipe would wait for a writer.
func readRegular(path string) ([]byte, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, errors.New("not a regular file")
	}
	return os.ReadFile(path)
}

// Err is nil while the store is usable, and otherwise the cause that made
// it unusable, naming the state file.
func (s *Store) Err() error { return s.err }

// Lookup returns path's record, the identities of the failed sync's
// baseline (possibly none), and true; or false when path has no record or
// the store is unusable.
func (s *Store) Lookup(path string) ([]string, bool) {
	ids, ok := s.servers[s.address][path]
	return slices.Clone(ids), ok
}

// Record records a failure of the subtitle at path with the baseline's
// event identities, replacing any earlier record of path, and writes the
// state file. On an unusable store it writes nothing and returns Err; a
// write that fails makes the store unusable and returns the cause.
func (s *Store) Record(path string, ids []string) error {
	if s.err != nil {
		return s.err
	}
	if s.servers[s.address] == nil {
		s.servers[s.address] = map[string][]string{}
	}
	s.servers[s.address][path] = append([]string{}, ids...)
	return s.write()
}

// Forget removes path's record, one its caller found lapsed, and writes
// the state file; with no record it writes nothing. It fails as Record
// fails.
func (s *Store) Forget(path string) error {
	if s.err != nil {
		return s.err
	}
	if _, ok := s.servers[s.address][path]; !ok {
		return nil
	}
	delete(s.servers[s.address], path)
	if len(s.servers[s.address]) == 0 {
		delete(s.servers, s.address)
	}
	return s.write()
}

func (s *Store) tmp() string { return s.path + ".tmp" }

// fail makes the store unusable: from now on no path has a record and
// nothing is written.
func (s *Store) fail(cause error) {
	s.err = fmt.Errorf("state file %s: %w", s.path, cause)
	s.servers = nil
	s.pending = false
}

// write replaces the state file with the whole state: written to the
// temporary file, closed, then renamed over the state file, so that a kill
// at any moment leaves the old state or the new one (G22). Nothing is
// flushed to the device; E4 excludes power loss.
func (s *Store) write() error {
	content := encode(s.servers)
	if s.pending {
		// The temporary file is the state until it is in place: writing
		// over it first would lose its records to a kill mid-write.
		if err := os.Rename(s.tmp(), s.path); err != nil {
			return s.writeFailed(err)
		}
		s.pending = false
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return s.writeFailed(err)
	}
	// A temporary file still here is never the state (Load or the rename
	// above took it), and writing through it could reach the state file by
	// a link: it is replaced, never truncated.
	if fi, err := os.Lstat(s.tmp()); err == nil && !fi.IsDir() {
		os.Remove(s.tmp())
	}
	f, err := os.OpenFile(s.tmp(), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return s.writeFailed(err)
	}
	_, err = f.Write(content)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(s.tmp())
		return s.writeFailed(err)
	}
	// T3: kills spread over this write and both renames left a complete state file, or none and a complete temporary
	// file, in TestKilledWhileRecording on Linux and on Windows build 26300, NTFS and exFAT (2026-11-22).
	if err := os.Rename(s.tmp(), s.path); err != nil {
		return s.writeFailed(err)
	}
	return nil
}

func (s *Store) writeFailed(err error) error {
	s.fail(fmt.Errorf("cannot write it: %w", err))
	return s.err
}

// unwrapPath drops the path from an error about the state file itself,
// which the store's cause already names.
func unwrapPath(err error) error {
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return pe.Err
	}
	return err
}
