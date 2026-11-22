package invocation

import "github.com/BrianWeiHaoMa/bazarrbulksync/internal/bazarr"

// CommandFix is `fix` (G24, G25).
const CommandFix Command = "fix"

// fixNames are F10's fix names, in its order, each with the fix it names.
// No other name is accepted, so no other modification Bazarr has can be
// asked for (N3).
var fixNames = []struct {
	name string
	fix  bazarr.Fix
	help string
}{
	{"remove-hi", bazarr.FixRemoveHI, "remove hearing-impaired cues"},
	{"remove-tags", bazarr.FixRemoveTags, "remove style tags"},
	{"ocr-fixes", bazarr.FixOCR, "correct OCR errors"},
	{"common-fixes", bazarr.FixCommon, "apply common corrections"},
	{"fix-uppercase", bazarr.FixUppercase, "change upper-case text to normal case"},
	{"remove-emoji", bazarr.FixEmoji, "remove emoji"},
}

// FixName returns the name F10 gives fix.
func FixName(fix bazarr.Fix) string {
	for _, n := range fixNames {
		if n.fix == fix {
			return n.name
		}
	}
	return ""
}

func fixWords() []option {
	words := make([]option, len(fixNames))
	for i, n := range fixNames {
		fix := n.fix
		words[i] = option{name: n.name, help: n.help, set: func(r *Request, _ string) error { r.Fix = fix; return nil }}
	}
	return words
}

var fixCommand = command{
	name:    CommandFix,
	summary: "have Bazarr apply one fix to the selected external subtitles",
	about: "Asks Bazarr to apply the fix FIX to each selected external subtitle, one request at a\n" +
		"time, and prints each outcome, then the totals. Fixed files cannot be restored.\n" +
		"remove-hi skips a subtitle whose fix would have Bazarr replace another subtitle.",
	word:    &word{arg: "FIX", noun: "fix", values: fixWords()},
	section: "Changes",
	options: changeOptions,
}
