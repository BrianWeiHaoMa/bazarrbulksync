package invocation

import (
	"fmt"
	"strings"
)

// Version is the program's version. The release build sets it with
// `-ldflags "-X github.com/BrianWeiHaoMa/bazarrbulksync/internal/invocation.Version=<version>"`;
// a build that sets nothing is a development build.
var Version string

func versionText() string {
	if Version == "" {
		return "bazarrbulksync (development build)\n"
	}
	return "bazarrbulksync " + Version + "\n"
}

const connectionNote = "The connection is read from the environment variables " + envURL + " (Bazarr's address,\n" +
	"including any base path, such as https://host/bazarr) and " + envAPIKey + ";\n" +
	"the options --url and --api-key override them.\n"

func programHelp() string {
	var b strings.Builder
	b.WriteString("bazarrbulksync acts on the external subtitles of a whole Bazarr library at once.\n\n")
	b.WriteString("Usage:\n  bazarrbulksync <command> [options]\n  bazarrbulksync --help\n  bazarrbulksync --version\n\n")
	b.WriteString("Commands:\n")
	for _, cmd := range commands {
		fmt.Fprintf(&b, "  %-8s %s\n", cmd.name, cmd.summary)
	}
	b.WriteString("\nRun 'bazarrbulksync <command> --help' for a command's options.\n\n")
	b.WriteString(connectionNote)
	return b.String()
}

func commandHelp(cmd command, getenv func(string) string) string {
	var b strings.Builder
	usage := string(cmd.name)
	if cmd.word != nil {
		usage += " " + cmd.word.arg
	}
	fmt.Fprintf(&b, "Usage: bazarrbulksync %s [options]\n\n%s\n", usage, cmd.about)
	if w := cmd.word; w != nil {
		fmt.Fprintf(&b, "\n%s is exactly one of:\n", w.arg)
		for _, v := range w.values {
			fmt.Fprintf(&b, "  %-22s %s\n", v.name, v.help)
		}
	}
	writeOptions(&b, cmd.section+":", cmd.options)
	selectionTitle := "Selection (with none, every external subtitle is selected):"
	if cmd.needsSelection {
		selectionTitle = "Selection (at least one is required):"
	}
	writeOptions(&b, selectionTitle, selectionOptions)
	b.WriteString("  A subtitle is selected when it satisfies every kind of option given;\n" +
		"  --title, --movie-id, and --series-id are one kind, which any one value satisfies.\n")
	writeOptions(&b, "Connection:", connectionOptions)
	b.WriteString("\n  --help                 show this help\n")
	if cmd.notes != nil {
		b.WriteString("\n" + cmd.notes(getenv))
	}
	return b.String()
}

func writeOptions(b *strings.Builder, title string, options []option) {
	fmt.Fprintf(b, "\n%s\n", title)
	for _, o := range options {
		fmt.Fprintf(b, "  %-22s %s\n", strings.TrimSpace("--"+o.name+" "+o.arg), o.help)
	}
}
