package reporter

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/bazarr"
)

// Subtitle writes one line naming the subtitle (G10).
func (r *Reporter) Subtitle(s bazarr.Subtitle) { r.writeOut(subtitleLine(s)) }

// subtitleLine names a subtitle in one line: its movie or series title and
// ID, an episode's season and episode, its language with HI and forced when
// they hold, and its path, last because it may hold spaces. Result lines of
// the changing commands name a subtitle the same way (P3).
func subtitleLine(s bazarr.Subtitle) string {
	var b strings.Builder
	if s.Type == bazarr.TypeEpisode {
		fmt.Fprintf(&b, "%s [series %d] S%02dE%02d", plain(s.Title), s.SeriesID, s.Season, s.Episode)
	} else {
		fmt.Fprintf(&b, "%s [movie %d]", plain(s.Title), s.MovieID)
	}
	b.WriteString("  " + plain(s.Language))
	if s.HI {
		b.WriteString(" HI")
	}
	if s.Forced {
		b.WriteString(" forced")
	}
	b.WriteString("  " + plain(s.Path))
	return b.String()
}

// plain escapes the control characters of text as \xNN or \uNNNN, so that a
// title or path from Bazarr stays on its line and carries no escape sequence
// to a terminal (G5).
func plain(text string) string {
	if !strings.ContainsFunc(text, unicode.IsControl) {
		return text
	}
	var b strings.Builder
	for _, c := range text {
		switch {
		case !unicode.IsControl(c):
			b.WriteRune(c)
		case c < 0x100:
			fmt.Fprintf(&b, `\x%02x`, c)
		default:
			fmt.Fprintf(&b, `\u%04x`, c)
		}
	}
	return b.String()
}

// subtitleObject is a subtitle as `list --json` prints it: exactly F7's
// fields, in F7's order, a field that does not apply being null.
type subtitleObject struct {
	Type      bazarr.VideoType `json:"type"`
	Title     string           `json:"title"`
	MovieID   *int             `json:"movie_id"`
	SeriesID  *int             `json:"series_id"`
	EpisodeID *int             `json:"episode_id"`
	Season    *int             `json:"season"`
	Episode   *int             `json:"episode"`
	Language  string           `json:"language"`
	HI        bool             `json:"hi"`
	Forced    bool             `json:"forced"`
	Path      string           `json:"path"`
}

// SubtitleJSON writes the subtitle as one JSON object on one line (G11, F7).
func (r *Reporter) SubtitleJSON(s bazarr.Subtitle) {
	obj := subtitleObject{Type: s.Type, Title: s.Title, Language: s.Language, HI: s.HI, Forced: s.Forced, Path: s.Path}
	if s.Type == bazarr.TypeEpisode {
		// Season 0 is a real season (specials), so zero is written, never null.
		obj.SeriesID, obj.EpisodeID, obj.Season, obj.Episode = &s.SeriesID, &s.EpisodeID, &s.Season, &s.Episode
	} else {
		obj.MovieID = &s.MovieID
	}
	var b strings.Builder
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(obj) // a struct of strings, numbers, and booleans always encodes
	r.writeOut(strings.TrimSuffix(b.String(), "\n"))
}

// ListTotals writes `list`'s totals: the count of subs, then the count per
// language code, HI and forced subtitles counted in their language (G10).
func (r *Reporter) ListTotals(subs []bazarr.Subtitle) {
	perLanguage := map[string]int{}
	for _, s := range subs {
		perLanguage[s.Language]++
	}
	var counts []string
	for _, lang := range slices.Sorted(maps.Keys(perLanguage)) {
		counts = append(counts, plain(lang)+" "+strconv.Itoa(perLanguage[lang]))
	}
	r.writeOut("")
	noun := "subtitles"
	if len(subs) == 1 {
		noun = "subtitle"
	}
	r.writeOut(fmt.Sprintf("Total: %d %s", len(subs), noun))
	r.writeOut("By language: " + strings.Join(counts, ", "))
}
