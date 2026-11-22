package main_test

import (
	"errors"
	"io/fs"
	"strings"
	"testing"

	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/bazarr"
)

// TestBedPlatform checks against the test bed what only the system the
// product runs on can show: the confirmation at a terminal, which rests on
// the system's terminal query (G12), and the default state file's path (P1).
// The product runs where testbed_host_test.go says. On a fresh bed:
//
//	BAZARRBULKSYNC_TESTBED=$PWD/testbed/run/bed.json BAZARRBULKSYNC_TESTBED_PLATFORM=1 \
//	go test -count=1 . -run TestBedPlatform
//
// It deletes one Spanish subtitle and leaves a failed sync in Bazarr.
const platformVar = "BAZARRBULKSYNC_TESTBED_PLATFORM"

func TestBedPlatform(t *testing.T) {
	m := changingBed(t, platformVar)
	b := newChangeBed(t, m)
	h := onHost(t)

	t.Run("prompt", func(t *testing.T) {
		sub := b.one(t, "the Spanish subtitle of Sintel", func(s bazarr.Subtitle) bool {
			return s.Type == bazarr.TypeMovie && s.Title == "Sintel" && s.Language == "es" && !s.HI && !s.Forced
		})
		b.needProvisioned(t, sub.Path)
		args := append([]string{"delete"}, b.selectAlone(t, sub)...)
		line := b.listLine(t, sub)
		const asked, question = "1 subtitle selected. ", "? [y/N] "

		// Standard input not a terminal: nobody can confirm.
		r, _ := b.exec(t, args...)
		if r.code == 0 || r.stdout != "" || !strings.Contains(r.stderr, "--yes") || strings.Contains(r.stderr, asked) {
			t.Errorf("stdin not a terminal: code %d, stdout %q, stderr %q; want --yes required without asking, nonzero", r.code, r.stdout, r.stderr)
		}
		b.sends(t, 0)

		for _, answer := range []string{"n\n", "\n", "yy\n"} {
			r, _ := b.execTyped(t, answer, args...)
			if r.code == 0 || !strings.Contains(r.stderr, asked) || !strings.Contains(r.stderr, question) || strings.Contains(r.stdout, "deleted") {
				t.Errorf("answer %q at a terminal: code %d, stdout %q, stderr %q; want asked, nothing deleted, nonzero", answer, r.code, r.stdout, r.stderr)
			}
			b.sends(t, 0)
		}
		if _, err := b.ReadFile(sub.Path); err != nil {
			t.Fatalf("%s after the answers other than yes: %v", sub.Path, err)
		}

		r, _ = b.execTyped(t, "y\n", args...)
		if r.code != 0 || !strings.Contains(r.stderr, asked) || !strings.Contains(r.stderr, question) {
			t.Errorf("answer y at a terminal: code %d, stderr %q; want asked, exit 0", r.code, r.stderr)
		}
		b.sends(t, 1)
		b.reports(t, r, "deleted", []string{line})
		if _, err := b.ReadFile(sub.Path); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("%s after yes: %v; want it deleted", sub.Path, err)
		}
	})

	t.Run("default state file", func(t *testing.T) {
		// P1's configuration directory, under a home of the check's own.
		under := map[string][]string{
			"linux":  {".config"},
			"darwin": {"Library", "Application Support"},
		}[h.system]
		if under == nil {
			t.Fatalf("no P1 path for %s here", h.system)
		}
		b.home = h.folder(t)
		defer func() { b.home = "" }()
		want := h.join(append(append([]string{b.home}, under...), "bazarrbulksync", "state.json")...)

		if r := b.run(t, 0, "sync", "--help"); !strings.Contains(r.stdout, "The default state file is "+want+"\n") {
			t.Errorf("sync --help prints %q; want the default state file %s (P1)", r.stdout, want)
		}
		noFile(t, h.join(b.home, under[0]))
		// recordFailureAt requires the failure recorded at want.
		_, args, _ := b.recordFailureAt(t, want)
		r := b.live(t, args, nil)
		if r.code != 0 || r.stdout != "\nTotal: 1 of 1 selected subtitle\nskipped (earlier failure) 1\n" {
			t.Errorf("the next run: code %d, stdout %q, stderr %q; want it skipped for its earlier failure, exit zero", r.code, r.stdout, r.stderr)
		}
		b.sends(t, 0)
	})
}
