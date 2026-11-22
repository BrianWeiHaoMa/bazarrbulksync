package invocation

// CommandList is `list` (G10, G11).
const CommandList Command = "list"

// ListOptions are the options only `list` accepts (F7).
type ListOptions struct {
	// JSON prints one JSON object per subtitle and no totals.
	JSON bool
}

var listCommand = command{
	name:    CommandList,
	summary: "list the selected external subtitles and their totals",
	about: "Lists the selected external subtitles, one line each, then the total count and the\n" +
		"count per language. Changes nothing.",
	section: "Output",
	options: []option{
		{name: "json", help: "print one JSON object per subtitle, one per line, and no totals",
			set: func(r *Request, _ string) error { r.List.JSON = true; return nil }},
	},
}
