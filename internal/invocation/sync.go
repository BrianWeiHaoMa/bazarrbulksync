package invocation

import (
	"errors"
	"runtime"
	"strings"
	"unicode"

	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/bazarr"
)

// CommandSync is `sync` (G15–G23).
const CommandSync Command = "sync"

// StateFile is the default state file's path. The container image's build
// sets it to /data/state.json (F9) with
// `-ldflags "-X github.com/BrianWeiHaoMa/bazarrbulksync/internal/invocation.StateFile=/data/state.json"`;
// a build that sets nothing uses P1's path under the user's configuration
// directory.
var StateFile string

// SyncOptions are the options only `sync` accepts (F8).
type SyncOptions struct {
	Resync      bool
	RetryFailed bool
	// Concurrency is at least 1; 1 when not given.
	Concurrency int
	// StateFile is the state file's path: --state-file as given, else the
	// default. It is empty when the default cannot be resolved, and
	// StateFileProblem then says why.
	StateFile        string
	StateFileProblem string
	// Bazarr holds the sync options a request carries. Its Reference is
	// "a:N" or "s:N" as given, and empty for "lang:CODE", which
	// ReferenceLanguage holds instead: the command resolves that reference
	// for each subtitle.
	Bazarr            bazarr.SyncOptions
	ReferenceLanguage string
}

var (
	errAtLeastOne = errors.New("a whole number of at least 1")
	errReference  = errors.New("a:N or s:N with N from 0 to 9, or lang:CODE")
)

var syncCommand = command{
	name:    CommandSync,
	summary: "have Bazarr synchronize the selected external subtitles",
	about: "Asks Bazarr to synchronize each selected external subtitle that is not forced, not\n" +
		"currently synced, and without an earlier failure, and prints each outcome, then the\n" +
		"totals. A failed sync is remembered in the state file and skipped by later runs.",
	section: "Sync",
	options: append(append([]option{}, changeOptions...),
		option{name: "resync", help: "also sync subtitles that are currently synced",
			set: func(r *Request, _ string) error { r.Sync.Resync = true; return nil }},
		option{name: "retry-failed", help: "also sync subtitles whose earlier sync failed",
			set: func(r *Request, _ string) error { r.Sync.RetryFailed = true; return nil }},
		option{name: "concurrency", arg: "N", help: "keep at most N of the run's syncs in Bazarr at once (default: 1)",
			set: func(r *Request, v string) error { return setAtLeastOne(&r.Sync.Concurrency, v) }},
		option{name: "state-file", arg: "PATH", help: "the state file remembering failed syncs (default: below)",
			set: func(r *Request, v string) error {
				if v == "" {
					return errText
				}
				r.Sync.StateFile = v
				return nil
			}},
		option{name: "max-offset-seconds", arg: "N", help: "the largest offset Bazarr may find, in seconds",
			set: func(r *Request, v string) error { return setAtLeastOne(&r.Sync.Bazarr.MaxOffsetSeconds, v) }},
		option{name: "no-fix-framerate", help: "do not let Bazarr correct the frame rate",
			set: func(r *Request, _ string) error { r.Sync.Bazarr.NoFixFramerate = true; return nil }},
		option{name: "gss", help: "use Bazarr's golden-section search",
			set: func(r *Request, _ string) error { r.Sync.Bazarr.GSS = true; return nil }},
		option{name: "reference", arg: "REF", help: "a:N (audio track N), s:N (embedded subtitle track N), N from 0 to 9,\n" +
			"                         or lang:CODE (the video's non-forced subtitle in language CODE)",
			set: setReference},
	),
	notes: stateFileNote,
}

// setAtLeastOne stores v, a whole number of at least 1.
func setAtLeastOne(to *int, v string) error {
	var n []int
	if appendWholeNumber(&n, v) != nil || n[0] < 1 {
		return errAtLeastOne
	}
	*to = n[0]
	return nil
}

// setReference stores F8's REF; the last one given counts.
func setReference(r *Request, v string) error {
	if code, ok := strings.CutPrefix(v, "lang:"); ok {
		// No language code holds a space or a control character.
		if code == "" || strings.ContainsFunc(code, func(c rune) bool { return unicode.IsSpace(c) || unicode.IsControl(c) }) {
			return errReference
		}
		r.Sync.Bazarr.Reference, r.Sync.ReferenceLanguage = "", code
		return nil
	}
	if len(v) == 3 && (v[0] == 'a' || v[0] == 's') && v[1] == ':' && v[2] >= '0' && v[2] <= '9' {
		r.Sync.Bazarr.Reference, r.Sync.ReferenceLanguage = v, ""
		return nil
	}
	return errReference
}

// finishSync completes a sync request once every option is read: the
// concurrency's default and the state file's default path.
func finishSync(r *Request, getenv func(string) string) {
	if r.Sync.Concurrency == 0 {
		r.Sync.Concurrency = 1
	}
	if r.Sync.StateFile == "" {
		r.Sync.StateFile, r.Sync.StateFileProblem = defaultStateFile(runtime.GOOS, getenv)
	}
}

// stateFileNote ends `sync --help` with the default state file's path, or
// why it cannot be resolved (P1).
func stateFileNote(getenv func(string) string) string {
	path, problem := defaultStateFile(runtime.GOOS, getenv)
	if path == "" {
		return "The default state file cannot be resolved: " + problem + ".\n" +
			"Without --state-file, sync runs as if no failure were recorded.\n"
	}
	return "The default state file is " + path + "\n"
}

// defaultStateFile resolves the default state file's path for the system
// goos from the environment alone, opening nothing (P1): StateFile when a
// build set it, else bazarrbulksync/state.json under the user's
// configuration directory as P1 names it for each system. It returns "" and the reason when the directory cannot be
// resolved.
func defaultStateFile(goos string, getenv func(string) string) (path, problem string) {
	if StateFile != "" {
		return StateFile, ""
	}
	sep := "/"
	var dir string
	switch goos {
	case "windows":
		sep = `\`
		if dir = getenv("AppData"); dir == "" {
			return "", "%AppData% is not set"
		}
	case "darwin":
		if dir = getenv("HOME"); !strings.HasPrefix(dir, "/") {
			return "", "$HOME is not set to an absolute path"
		}
		dir = strings.TrimRight(dir, "/") + "/Library/Application Support"
	default:
		// P1 names ~/.config, and XDG_CONFIG_HOME is not read: a scheduler's
		// environment often lacks it, and the path must be the same on
		// every run (L2).
		if dir = getenv("HOME"); !strings.HasPrefix(dir, "/") {
			return "", "$HOME is not set to an absolute path"
		}
		dir = strings.TrimRight(dir, "/") + "/.config"
	}
	return strings.TrimRight(dir, sep) + sep + "bazarrbulksync" + sep + "state.json", ""
}
