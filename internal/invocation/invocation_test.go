package invocation_test

import (
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/bazarr"
	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/invocation"
	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/selection"
)

// env is an environment holding only the given variables.
func env(vars map[string]string) func(string) string {
	return func(name string) string { return vars[name] }
}

var noEnv = env(nil)

func parse(t *testing.T, getenv func(string) string, args ...string) invocation.Result {
	t.Helper()
	res, err := invocation.Parse(args, getenv)
	if err != nil {
		t.Fatalf("Parse(%q): %v", args, err)
	}
	return res
}

func request(t *testing.T, getenv func(string) string, args ...string) invocation.Request {
	t.Helper()
	res := parse(t, getenv, args...)
	if res.Request == nil {
		t.Fatalf("Parse(%q) gave text %q, want a request", args, res.Text)
	}
	if res.Text != "" {
		t.Errorf("Parse(%q) gave both a request and text %q", args, res.Text)
	}
	return *res.Request
}

func usageError(t *testing.T, getenv func(string) string, args ...string) *invocation.UsageError {
	t.Helper()
	res, err := invocation.Parse(args, getenv)
	var ue *invocation.UsageError
	if !errors.As(err, &ue) {
		t.Errorf("Parse(%q) = %+v, %v; want a usage error", args, res, err)
		return nil
	}
	if res.Request != nil || res.Text != "" {
		t.Errorf("Parse(%q) gave a usage error and %+v", args, res)
	}
	if ue.Problem == "" || strings.Contains(ue.Problem, "\n") || ue.Hint() == "" || strings.Contains(ue.Hint(), "\n") {
		t.Errorf("Parse(%q): usage error %q, hint %q; want one line each", args, ue.Problem, ue.Hint())
	}
	return ue
}

func TestUsageErrors(t *testing.T) {
	for _, args := range [][]string{
		// No command, and the commands this program does not have.
		{},
		{"sync"}, {"fix", "remove-hi"}, {"lst"}, {"help"}, {"LIST"},
		// A stray word anywhere after the command.
		{"list", "extra"},
		{"list", "extra", "--language", "en"},
		{"list", "--language", "en", "extra"},
		{"list", "--language", "en", "extra", "--movies"},
		{"list", "--movies", "en"},
		{"list", "--json", "list"},
		// Unknown options, before or after the command.
		{"list", "--bogus"}, {"list", "-x"}, {"list", "-"}, {"list", "--"}, {"list", "--", "--movies"},
		{"list", "-json"}, {"list", "--JSON"}, {"list", "--version"}, {"list", "-h"},
		{"--bogus"}, {"--url", "http://h", "list"}, {"--movies", "list"}, {"-h"},
		{"--help", "list"}, {"--version", "list"}, {"--help", "--version"},
		// --help does not excuse the rest of the line.
		{"list", "--help", "--bogus"}, {"list", "--help", "extra"}, {"list", "--movies", "--series", "--help"},
		{"list", "--help", "--season", "spring"}, {"list", "--title", "--help"},
		// N6, N9: no other selection, and no configuration file.
		{"list", "--tag", "x"}, {"list", "--profile", "x"}, {"list", "--monitored"}, {"list", "--config", "x"},
		{"list", "--audio-language", "en"}, {"list", "--format", "srt"},
		// Options of other commands.
		{"list", "--yes"}, {"list", "--dry-run"}, {"list", "--state-file", "/tmp/x"},
		// A flag given a value.
		{"list", "--json=1"}, {"list", "--movies=true"}, {"list", "--no-hi="},
		// A known option missing its value.
		{"list", "--title"}, {"list", "--language"}, {"list", "--season"}, {"list", "--movie-id"},
		{"list", "--series-id"}, {"list", "--url"}, {"list", "--api-key"},
		{"list", "--language", "--json"}, {"list", "--title", "--movies", "x"}, {"list", "--season", "--season", "1"},
		// Malformed values.
		{"list", "--movie-id", "abc"}, {"list", "--movie-id", "1.5"}, {"list", "--movie-id", ""},
		{"list", "--movie-id=-1"}, {"list", "--movie-id", "+1"}, {"list", "--movie-id", " 1"},
		{"list", "--movie-id", "99999999999999999999999"},
		{"list", "--series-id", "abc"}, {"list", "--series-id", "1.5"}, {"list", "--series-id="},
		{"list", "--season", "abc"}, {"list", "--season", "1.5"}, {"list", "--season", ""}, {"list", "--season", "-1"},
		{"list", "--season", "1e2"}, {"list", "--season", "١"},
		{"list", "--title", ""}, {"list", "--language="},
		// Both options of a pair, in either order and apart.
		{"list", "--movies", "--series"}, {"list", "--series", "--movies"},
		{"list", "--hi", "--no-hi"}, {"list", "--no-hi", "--hi"},
		{"list", "--forced", "--no-forced"}, {"list", "--no-forced", "--json", "--forced"},
	} {
		usageError(t, noEnv, args...)
	}
}

func TestUsageErrorNamesTheProblem(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want []string
	}{
		{[]string{"list", "--language", "en", "extra"}, []string{`"extra"`}},
		{[]string{"sync"}, []string{`"sync"`}},
		{[]string{"list", "--tag", "x"}, []string{`"--tag"`}},
		{[]string{"list", "--season", "1.5"}, []string{"--season", `"1.5"`}},
		{[]string{"list", "--title"}, []string{"--title"}},
		{[]string{"list", "--hi", "--no-hi"}, []string{"--hi", "--no-hi"}},
	} {
		if ue := usageError(t, noEnv, tc.args...); ue != nil {
			for _, w := range tc.want {
				if !strings.Contains(ue.Problem, w) {
					t.Errorf("Parse(%q): %q does not name %s", tc.args, ue.Problem, w)
				}
			}
		}
	}
	if ue := usageError(t, noEnv, "list", "x"); ue != nil && !strings.Contains(ue.Hint(), "list --help") {
		t.Errorf("hint %q does not point at list's help", ue.Hint())
	}
	if ue := usageError(t, noEnv); ue != nil && !strings.Contains(ue.Hint(), "bazarrbulksync --help") {
		t.Errorf("hint %q does not point at the program's help", ue.Hint())
	}
}

func TestRequest(t *testing.T) {
	got := request(t, noEnv, "list")
	want := invocation.Request{Command: invocation.CommandList}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("plain list: %+v, want %+v", got, want)
	}

	got = request(t, noEnv, "list",
		"--title", "breaking", "--title=Bad Show", "--language", "en", "--language=PT",
		"--season", "0", "--season=10", "--movie-id", "3", "--movie-id=007", "--series-id", "12", "--series-id", "4",
		"--title", "-dash", "--title=--dashes", "--title", "-- And Now", "--no-hi", "--forced", "--series", "--json")
	want = invocation.Request{
		Command: invocation.CommandList,
		Selection: selection.Criteria{
			Type:      bazarr.TypeEpisode,
			Titles:    []string{"breaking", "Bad Show", "-dash", "--dashes", "-- And Now"},
			MovieIDs:  []int{3, 7},
			SeriesIDs: []int{12, 4},
			Seasons:   []int{0, 10},
			Languages: []string{"en", "PT"},
			HI:        selection.Without,
			Forced:    selection.Only,
		},
		List: invocation.ListOptions{JSON: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("every option:\n got %+v\nwant %+v", got, want)
	}

	for _, tc := range []struct {
		args []string
		want selection.Criteria
	}{
		{[]string{"--movies"}, selection.Criteria{Type: bazarr.TypeMovie}},
		{[]string{"--hi"}, selection.Criteria{HI: selection.Only}},
		{[]string{"--no-forced"}, selection.Criteria{Forced: selection.Without}},
		{[]string{"--movies", "--movies"}, selection.Criteria{Type: bazarr.TypeMovie}},
	} {
		if got := request(t, noEnv, append([]string{"list"}, tc.args...)...); !reflect.DeepEqual(got.Selection, tc.want) || got.List.JSON {
			t.Errorf("list %q: %+v, want %+v", tc.args, got, tc.want)
		}
	}
}

func TestConnection(t *testing.T) {
	both := env(map[string]string{"BAZARR_URL": "http://env:6767", "BAZARR_API_KEY": "envkey"})
	for _, tc := range []struct {
		name   string
		getenv func(string) string
		args   []string
		want   bazarr.Connection
	}{
		{"variables only", both, nil, bazarr.Connection{URL: "http://env:6767", APIKey: "envkey"}},
		{"options override", both, []string{"--url", "https://opt/bazarr", "--api-key=optkey"},
			bazarr.Connection{URL: "https://opt/bazarr", APIKey: "optkey"}},
		{"one option each way", both, []string{"--api-key", "optkey"},
			bazarr.Connection{URL: "http://env:6767", APIKey: "optkey"}},
		{"options only", noEnv, []string{"--url=http://o", "--api-key", "k"}, bazarr.Connection{URL: "http://o", APIKey: "k"}},
		{"neither: passed on empty", noEnv, nil, bazarr.Connection{}},
		{"an empty option overrides", both, []string{"--url="}, bazarr.Connection{APIKey: "envkey"}},
		{"not an address is still passed on", noEnv, []string{"--url", "host:6767"}, bazarr.Connection{URL: "host:6767"}},
	} {
		got := request(t, tc.getenv, append([]string{"list"}, tc.args...)...)
		if got.Connection != tc.want {
			t.Errorf("%s: %+v, want %+v", tc.name, got.Connection, tc.want)
		}
	}
}

func TestAPIKeyNeverInText(t *testing.T) {
	const key = "s3cr3t-K3Y-value"
	withKey := env(map[string]string{"BAZARR_API_KEY": key, "BAZARR_URL": "http://h:6767"})
	lines := [][]string{
		{}, {key}, {"--" + key}, {"list", key}, {"list", "--" + key}, {"list", "--" + key + "=x"},
		{"list", "--apikey=" + key}, {"list", "--api_key", key}, {"list", "--api-key", key, "extra"},
		{"list", "--api-key=" + key, "--season", "x"}, {"list", "--season", key}, {"list", "--movie-id=" + key},
		{"list", "--json=" + key}, {"list", "--api-key", key, "--hi", "--no-hi"}, {"list", "x" + key + "y"},
		{"--help", key}, {"--version", key}, {key, "list"}, {"--api-key", key, "list"},
		{"list", "--api-key", key, "--help"}, {"--help"}, {"--version"}, {"list", "--help"},
		// The key given by option and repeated where the parse stops, before or after it.
		{"list", "--api-key", key, "--season", key}, {"list", "--api-key=" + key, key}, {"list", key, "--api-key", key},
		{"list", "--movie-id=" + key, "--api-key=" + key}, {key, "--api-key", key}, {"list", "--api-key", key, "--" + key},
	}
	// A key that quoting an argument would spell out: a tab becomes `\t`.
	tabKey := `tab\tkey`
	for _, args := range [][]string{{"list", "--api-key", tabKey, "--season", "tab\tkey"}, {"list", "--season", "tab\tkey"}} {
		getenv := env(map[string]string{"BAZARR_API_KEY": tabKey})
		if text := returnedText(t, args, getenv); strings.Contains(text, tabKey) {
			t.Errorf("Parse(%q) returned the API key: %q", args, text)
		}
	}
	for _, getenv := range []func(string) string{withKey, noEnv} {
		for _, args := range lines {
			// Without the variable, a word is the key only where the command
			// line gives it as one, or tried to.
			if getenv("BAZARR_API_KEY") == "" && !slices.ContainsFunc(args, func(a string) bool { return strings.HasPrefix(a, "--api") }) {
				continue
			}
			if text := returnedText(t, args, getenv); strings.Contains(text, key) || strings.Contains(text, "3cr3t") {
				t.Errorf("Parse(%q) returned the API key: %q", args, text)
			}
		}
	}
}

// returnedText is every text Parse returns for args: the usage error with
// its hint, or the help or version text.
func returnedText(t *testing.T, args []string, getenv func(string) string) string {
	t.Helper()
	res, err := invocation.Parse(args, getenv)
	if err == nil {
		return res.Text
	}
	var ue *invocation.UsageError
	if !errors.As(err, &ue) {
		t.Errorf("Parse(%q): %v", args, err)
		return ""
	}
	return ue.Error() + "\n" + ue.Hint()
}
