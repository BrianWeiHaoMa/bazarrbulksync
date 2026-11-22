// Package invocation turns a command line and the environment into exactly
// one of: a validated request, a usage error (G1), or the help or version
// text to print (F5). It contacts nothing, opens no file, and writes to no
// stream: the caller prints what it returns.
package invocation

import (
	"fmt"
	"slices"
	"strings"

	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/bazarr"
	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/selection"
)

// Command names a command of F5.
type Command string

// commands is every command the program accepts, in the order its help
// lists them. A new command is one file beside list.go and one entry here.
var commands = []command{listCommand, syncCommand, fixCommand, deleteCommand}

// Request is a validated command line. The connection and the selection are
// shared by every command; each command's own options sit in its field.
type Request struct {
	Command Command
	// Connection holds the address and API key, each from its option or else
	// its environment variable (F4), possibly empty: the client's connection
	// check names a missing one (G2). Clock and Limits are left zero.
	Connection bazarr.Connection
	Selection  selection.Criteria
	List       ListOptions
	Change     ChangeOptions
	Sync       SyncOptions
	// Fix is the fix `fix` applies (F10).
	Fix bazarr.Fix
}

// ChangeOptions are the options of the commands that change Bazarr (F7).
type ChangeOptions struct {
	// Yes proceeds without asking for confirmation (G12).
	Yes bool
	// DryRun prints what would be done and sends no changing request (G13).
	DryRun bool
}

// Result is what a command line asks for: a request, or, when Request is
// nil, Text (help or version text, ending in a newline) for standard output.
type Result struct {
	Request *Request
	Text    string
}

// UsageError is G1's usage error. Its text never holds the API key (R2).
type UsageError struct {
	// Problem says what is wrong, in one line.
	Problem string
	// Command is the command whose usage applies, or empty for the program's.
	Command Command
}

func (e *UsageError) Error() string { return e.Problem }

// Hint tells the user where the usage is, in one line.
func (e *UsageError) Hint() string {
	if e.Command == "" {
		return "Run 'bazarrbulksync --help' for usage."
	}
	return fmt.Sprintf("Run 'bazarrbulksync %s --help' for usage.", e.Command)
}

// Parse reads args, the command line after the program's name, and the
// environment through getenv. It returns a *UsageError for every command
// line G1 names.
func Parse(args []string, getenv func(string) string) (Result, error) {
	p := parser{keys: APIKeys(args, getenv)}
	if len(args) == 0 {
		return Result{}, p.usage("", "no command given")
	}
	switch first := args[0]; {
	case first == "--help" || first == "--version":
		if len(args) > 1 {
			return Result{}, p.usage("", "unexpected argument %s after %s", p.quote(args[1]), first)
		}
		if first == "--help" {
			return Result{Text: programHelp()}, nil
		}
		return Result{Text: versionText()}, nil
	case strings.HasPrefix(first, "-"):
		name, _, _ := strings.Cut(first, "=")
		for _, cmd := range commands {
			if findOption(cmd, strings.TrimPrefix(name, "--")) != nil {
				return Result{}, p.usage("", "the option %s goes after the command", p.quote(name))
			}
		}
		return Result{}, p.usage("", "unknown option %s", p.quote(name))
	}
	for _, cmd := range commands {
		if args[0] == string(cmd.name) {
			return p.parseCommand(cmd, args[1:], getenv)
		}
	}
	return Result{}, p.usage("", "unknown command %s", p.quote(args[0]))
}

// parser carries what every message must leave out: the API key's values.
type parser struct {
	keys []string
}

// APIKeys returns every value the API key may have on this command line:
// the environment's and each value given to --api-key, so that no message
// quotes one, wherever the parse stops, and whichever of them is used (R2).
func APIKeys(args []string, getenv func(string) string) []string {
	keys := []string{getenv(envAPIKey)}
	for i, a := range args {
		if v, ok := strings.CutPrefix(a, "--"+optAPIKey+"="); ok {
			keys = append(keys, v)
		} else if a == "--"+optAPIKey && i+1 < len(args) {
			keys = append(keys, args[i+1])
		}
	}
	return keys
}

// quote quotes an argument for a message, unless the argument or its quoted
// form holds the API key (R2): quoting turns a tab into the two characters
// of `\t`, which a key may hold.
func (p parser) quote(arg string) string {
	quoted := fmt.Sprintf("%q", arg)
	for _, k := range p.keys {
		if k != "" && (strings.Contains(arg, k) || strings.Contains(quoted, k)) {
			return "(an argument holding the API key)"
		}
	}
	return quoted
}

func (p parser) usage(cmd Command, format string, a ...any) *UsageError {
	return &UsageError{Problem: fmt.Sprintf(format, a...), Command: cmd}
}

// parseCommand reads the arguments after cmd's name. Every argument must be
// one of cmd's options, the value of the option before it, or the one word
// cmd takes: a stray word anywhere is an error, never the end of the
// options (G1). --help gives the command's help only when the rest of the
// line is valid, since G1 makes no exception for it, but without the word
// or a selection a command needs.
func (p parser) parseCommand(cmd command, args []string, getenv func(string) string) (Result, error) {
	req := &Request{Command: cmd.name}
	given := map[string]bool{}
	gotWord := false
	help := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--help" {
			help = true
			continue
		}
		if !strings.HasPrefix(arg, "--") || arg == "--" {
			if strings.HasPrefix(arg, "-") {
				return Result{}, p.usage(cmd.name, "unknown option %s", p.quote(arg))
			}
			if w := cmd.word; w != nil && !gotWord {
				v := w.find(arg)
				if v == nil {
					return Result{}, p.usage(cmd.name, "unknown %s %s: %s is one of %s", w.noun, p.quote(arg), w.arg, w.list())
				}
				if err := v.set(req, arg); err != nil {
					return Result{}, p.usage(cmd.name, "%s %s: %v", w.noun, p.quote(arg), err)
				}
				gotWord = true
				continue
			}
			if cmd.word != nil {
				return Result{}, p.usage(cmd.name, "unexpected argument %s: %s takes one %s", p.quote(arg), cmd.name, cmd.word.arg)
			}
			return Result{}, p.usage(cmd.name, "unexpected argument %s", p.quote(arg))
		}
		name, value, hasValue := strings.Cut(strings.TrimPrefix(arg, "--"), "=")
		opt := findOption(cmd, name)
		if opt == nil {
			return Result{}, p.usage(cmd.name, "unknown option %s", p.quote("--"+name))
		}
		if opt.arg == "" {
			if hasValue {
				return Result{}, p.usage(cmd.name, "the option --%s takes no value", name)
			}
		} else if !hasValue {
			// An option word in a value's place means the value was left out;
			// such a word is still a value when given as --name=value.
			if i+1 == len(args) || optionShaped(args[i+1]) {
				return Result{}, p.usage(cmd.name, "the option --%s needs a value (%s)", name, opt.arg)
			}
			i++
			value = args[i]
		}
		if err := opt.set(req, value); err != nil {
			return Result{}, p.usage(cmd.name, "the option --%s needs %s, not %s", name, err, p.quote(value))
		}
		given[name] = true
	}
	for _, pair := range pairs {
		if given[pair[0]] && given[pair[1]] {
			return Result{}, p.usage(cmd.name, "the options --%s and --%s cannot be given together", pair[0], pair[1])
		}
	}
	if help {
		return Result{Text: commandHelp(cmd, getenv)}, nil
	}
	if w := cmd.word; w != nil && !gotWord {
		return Result{}, p.usage(cmd.name, "%s needs %s, one of %s", cmd.name, w.arg, w.list())
	}
	if cmd.needsSelection && !slices.ContainsFunc(selectionOptions, func(o option) bool { return given[o.name] }) {
		return Result{}, p.usage(cmd.name, "%s needs at least one selection option, such as --language or --title", cmd.name)
	}
	if !given[optURL] {
		req.Connection.URL = getenv(envURL)
	}
	if !given[optAPIKey] {
		req.Connection.APIKey = getenv(envAPIKey)
	}
	if cmd.name == CommandSync {
		finishSync(req, getenv)
	}
	return Result{Request: req}, nil
}

// optionShaped says whether word reads as an option: "--", then a name of
// ASCII letters, digits, and hyphens, then optionally "=" and a value. A
// title such as "-- And Now" is not one.
func optionShaped(word string) bool {
	rest, ok := strings.CutPrefix(word, "--")
	if !ok {
		return false
	}
	name, _, _ := strings.Cut(rest, "=")
	for _, c := range name {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
			return false
		}
	}
	return true
}
