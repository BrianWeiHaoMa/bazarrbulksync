package invocation_test

import (
	"strings"
	"testing"

	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/invocation"
)

func TestHelp(t *testing.T) {
	program := parse(t, noEnv, "--help")
	if program.Request != nil || !strings.Contains(program.Text, "list") || !strings.HasSuffix(program.Text, "\n") {
		t.Errorf("program help: %+v", program)
	}
	for _, cmd := range []string{"sync", "delete", "fix"} {
		if !strings.Contains(program.Text, "  "+cmd+" ") {
			t.Errorf("program help does not list %s (F5):\n%s", cmd, program.Text)
		}
	}
	for _, other := range []string{"upload"} {
		if strings.Contains(program.Text, "  "+other+" ") {
			t.Errorf("program help offers %s, which this program does not have:\n%s", other, program.Text)
		}
	}
	for _, args := range [][]string{
		{"list", "--help"}, {"list", "--language", "en", "--help"}, {"list", "--help", "--json"},
		{"list", "--help", "--help"},
	} {
		res := parse(t, noEnv, args...)
		if res.Request != nil {
			t.Fatalf("Parse(%q) gave a request", args)
		}
		for _, opt := range []string{"--movies", "--series", "--title TEXT", "--movie-id N", "--series-id N",
			"--season N", "--language CODE", "--hi", "--no-hi", "--forced", "--no-forced", "--json",
			"--url URL", "--api-key KEY", "BAZARR_URL", "BAZARR_API_KEY"} {
			if !strings.Contains(res.Text, opt) {
				t.Errorf("Parse(%q): help does not name %s:\n%s", args, opt, res.Text)
			}
		}
		if res.Text == program.Text {
			t.Errorf("Parse(%q) gave the program's help, not list's", args)
		}
	}
	// Help comes from what the command line asks, never from its values.
	withEnv := env(map[string]string{"BAZARR_URL": "http://secret-host:1"})
	if res := parse(t, withEnv, "list", "--help"); strings.Contains(res.Text, "secret-host") {
		t.Errorf("list help shows the environment: %q", res.Text)
	}
}

func TestVersion(t *testing.T) {
	saved := invocation.Version
	t.Cleanup(func() { invocation.Version = saved })

	invocation.Version = ""
	res := parse(t, noEnv, "--version")
	if res.Request != nil || !strings.Contains(res.Text, "development build") || strings.TrimSpace(res.Text) == "" {
		t.Errorf("version with none set: %+v", res)
	}
	invocation.Version = "1.4.2"
	res = parse(t, noEnv, "--version")
	if res.Text != "bazarrbulksync 1.4.2\n" {
		t.Errorf("version 1.4.2 set: %q", res.Text)
	}
}
