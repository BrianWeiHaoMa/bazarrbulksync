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

func commandHelp(cmd command) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Usage: bazarrbulksync %s [options]\n\n%s\n", cmd.name, cmd.about)
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
	return b.String()
}

func writeOptions(b *strings.Builder, title string, options []option) {
	fmt.Fprintf(b, "\n%s\n", title)
	for _, o := range options {
		fmt.Fprintf(b, "  %-22s %s\n", strings.TrimSpace("--"+o.name+" "+o.arg), o.help)
	}
}
