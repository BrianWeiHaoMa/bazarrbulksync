package invocation

import (
	"errors"
	"strconv"

	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/bazarr"
	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/selection"
)

// The environment variables of F4, and the options that override them.
const (
	envURL    = "BAZARR_URL"
	envAPIKey = "BAZARR_API_KEY"
	optURL    = "url"
	optAPIKey = "api-key"
)

// command is one command of F5 with its own options; the connection and
// selection options below are added to every command.
type command struct {
	name Command
	// summary is the command's line in the program's help.
	summary string
	// about opens the command's help.
	about string
	// section titles the command's own options in its help.
	section string
	options []option
	// needsSelection makes a command line without a selection option a
	// usage error (G26).
	needsSelection bool
}

// option is one option of a command. An option without arg is a flag.
type option struct {
	name string // without the leading "--"
	arg  string // the value's placeholder in help, such as "N"
	help string
	// set stores the value in the request; its error completes "the option
	// --name needs …".
	set func(r *Request, value string) error
}

var (
	errWholeNumber = errors.New("a whole number")
	errText        = errors.New("a non-empty value")
)

// selectionOptions are F6's, accepted by every command; N9 admits no other.
var selectionOptions = []option{
	{name: "movies", help: "only movie subtitles", set: func(r *Request, _ string) error {
		r.Selection.Type = bazarr.TypeMovie
		return nil
	}},
	{name: "series", help: "only episode subtitles", set: func(r *Request, _ string) error {
		r.Selection.Type = bazarr.TypeEpisode
		return nil
	}},
	{name: "title", arg: "TEXT", help: "movies and series whose title contains TEXT, ignoring case (repeatable)",
		set: func(r *Request, v string) error { return appendText(&r.Selection.Titles, v) }},
	{name: "movie-id", arg: "N", help: "the movie with that Radarr ID, as Bazarr shows it (repeatable)",
		set: func(r *Request, v string) error { return appendWholeNumber(&r.Selection.MovieIDs, v) }},
	{name: "series-id", arg: "N", help: "the series with that Sonarr ID, as Bazarr shows it (repeatable)",
		set: func(r *Request, v string) error { return appendWholeNumber(&r.Selection.SeriesIDs, v) }},
	{name: "season", arg: "N", help: "only episode subtitles of that season; specials are season 0 (repeatable)",
		set: func(r *Request, v string) error { return appendWholeNumber(&r.Selection.Seasons, v) }},
	{name: "language", arg: "CODE", help: "subtitles in that language, by the code list prints, ignoring case (repeatable)",
		set: func(r *Request, v string) error { return appendText(&r.Selection.Languages, v) }},
	{name: "hi", help: "only hearing-impaired subtitles", set: func(r *Request, _ string) error {
		r.Selection.HI = selection.Only
		return nil
	}},
	{name: "no-hi", help: "no hearing-impaired subtitles", set: func(r *Request, _ string) error {
		r.Selection.HI = selection.Without
		return nil
	}},
	{name: "forced", help: "only forced subtitles", set: func(r *Request, _ string) error {
		r.Selection.Forced = selection.Only
		return nil
	}},
	{name: "no-forced", help: "no forced subtitles", set: func(r *Request, _ string) error {
		r.Selection.Forced = selection.Without
		return nil
	}},
}

// changeOptions are F7's, accepted by every command that changes Bazarr.
var changeOptions = []option{
	{name: "yes", help: "do not ask for confirmation", set: func(r *Request, _ string) error {
		r.Change.Yes = true
		return nil
	}},
	{name: "dry-run", help: "print what would be done, and change nothing", set: func(r *Request, _ string) error {
		r.Change.DryRun = true
		return nil
	}},
}

// pairs are the options G1 forbids together.
var pairs = [][2]string{{"movies", "series"}, {"hi", "no-hi"}, {"forced", "no-forced"}}

// connectionOptions override the environment (F4). Given twice, the last
// value counts: G1 does not make a repeated option an error.
var connectionOptions = []option{
	{name: optURL, arg: "URL", help: "Bazarr's address, including any base path (default: $" + envURL + ")",
		set: func(r *Request, v string) error { r.Connection.URL = v; return nil }},
	{name: optAPIKey, arg: "KEY", help: "Bazarr's API key (default: $" + envAPIKey + ")",
		set: func(r *Request, v string) error { r.Connection.APIKey = v; return nil }},
}

// findOption returns cmd's option of that name, or nil.
func findOption(cmd command, name string) *option {
	for _, group := range [][]option{cmd.options, selectionOptions, connectionOptions} {
		for i := range group {
			if group[i].name == name {
				return &group[i]
			}
		}
	}
	return nil
}

// appendWholeNumber appends v, which must be digits only: no sign, point,
// or space.
func appendWholeNumber(to *[]int, v string) error {
	if v == "" {
		return errWholeNumber
	}
	for _, c := range v {
		if c < '0' || c > '9' {
			return errWholeNumber
		}
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return errWholeNumber
	}
	*to = append(*to, n)
	return nil
}

// appendText appends v, which must not be empty: an empty title would
// select every movie and series, and an empty language code none.
func appendText(to *[]string, v string) error {
	if v == "" {
		return errText
	}
	*to = append(*to, v)
	return nil
}
