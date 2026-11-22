package invocation

// CommandDelete is `delete` (G26, G27).
const CommandDelete Command = "delete"

var deleteCommand = command{
	name:    CommandDelete,
	summary: "have Bazarr delete the selected external subtitles",
	about: "Asks Bazarr to delete each selected external subtitle, one request at a time, and prints\n" +
		"each outcome, then the totals. Deleted files cannot be restored. At least one selection\n" +
		"option is required.",
	section:        "Changes",
	options:        changeOptions,
	needsSelection: true,
}
