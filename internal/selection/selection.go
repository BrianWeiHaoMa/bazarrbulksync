// Package selection picks the subtitles a command acts on from the library
// Bazarr lists, by the selection options every command accepts (F6, G7), and
// names the options that match nothing in Bazarr (G8). It has no access to
// Bazarr.
package selection

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"unicode"

	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/bazarr"
)

// Flag is the choice a pair of options such as `--hi` and `--no-hi` makes.
type Flag int

const (
	Either  Flag = iota // neither option given
	Only                // `--hi`, `--forced`
	Without             // `--no-hi`, `--no-forced`
)

// Criteria are the selection options given. The zero value selects every
// external subtitle.
type Criteria struct {
	// Type is TypeMovie for `--movies`, TypeEpisode for `--series`, and empty
	// for both.
	Type bazarr.VideoType
	// Titles, MovieIDs, and SeriesIDs are one kind: a subtitle matching any
	// one of their values satisfies it.
	Titles    []string
	MovieIDs  []int
	SeriesIDs []int
	Seasons   []int
	Languages []string
	HI        Flag
	Forced    Flag
}

// NotFoundError names every `--title` value that matches no movie or series
// and every ID Bazarr does not have (G8).
type NotFoundError struct {
	Titles    []string
	MovieIDs  []int
	SeriesIDs []int
}

func (e *NotFoundError) Error() string {
	var parts []string
	for _, t := range e.Titles {
		parts = append(parts, fmt.Sprintf("no movie or series title contains %q", t))
	}
	for _, id := range e.MovieIDs {
		parts = append(parts, fmt.Sprintf("Bazarr has no movie with ID %d", id))
	}
	for _, id := range e.SeriesIDs {
		parts = append(parts, fmt.Sprintf("Bazarr has no series with ID %d", id))
	}
	return strings.Join(parts, "; ")
}

// Selected is a selected subtitle, with the other videos the library shows
// with a subtitle at its path, such as the other episodes of a
// multi-episode file, whose histories hold its events too.
type Selected struct {
	bazarr.Subtitle
	Others []bazarr.Video
}

// Select returns every subtitle of lib that c selects, in the order of P2:
// movies before episodes; movies and series in the library's order; a
// series' episodes by season, then episode number; one video's subtitles by
// path in byte order. Each comes with the other videos of the whole library,
// selected or not, that show a subtitle at its path. When a title or ID of
// c matches nothing in lib, it returns a *NotFoundError instead.
func Select(c Criteria, lib bazarr.Library) ([]Selected, error) {
	if err := notFound(c, lib); err != nil {
		return nil, err
	}
	var selected []Selected
	for _, m := range lib.Movies {
		selected = appendSelected(selected, c, m.Subtitles)
	}
	for _, s := range lib.Series {
		// Bazarr lists episodes newest first; P2 wants them by number.
		episodes := slices.Clone(s.Episodes)
		slices.SortStableFunc(episodes, func(a, b bazarr.Episode) int {
			return cmp.Or(cmp.Compare(a.Season, b.Season), cmp.Compare(a.Number, b.Number))
		})
		for _, e := range episodes {
			selected = appendSelected(selected, c, e.Subtitles)
		}
	}
	videosAt := map[string][]bazarr.Video{}
	for _, sub := range lib.Subtitles() {
		if v := sub.Video(); !slices.Contains(videosAt[sub.Path], v) {
			videosAt[sub.Path] = append(videosAt[sub.Path], v)
		}
	}
	for i := range selected {
		own := selected[i].Video()
		for _, v := range videosAt[selected[i].Path] {
			if v != own {
				selected[i].Others = append(selected[i].Others, v)
			}
		}
	}
	return selected, nil
}

// appendSelected appends the subtitles of one video that c selects, by path
// in byte order.
func appendSelected(selected []Selected, c Criteria, subs []bazarr.Subtitle) []Selected {
	start := len(selected)
	for _, sub := range subs {
		if c.selects(sub) {
			selected = append(selected, Selected{Subtitle: sub})
		}
	}
	slices.SortStableFunc(selected[start:], func(a, b Selected) int { return strings.Compare(a.Path, b.Path) })
	return selected
}

// selects says whether sub satisfies every kind of option given (G7).
func (c Criteria) selects(sub bazarr.Subtitle) bool {
	if c.Type != "" && sub.Type != c.Type {
		return false
	}
	if (len(c.Titles) > 0 || len(c.MovieIDs) > 0 || len(c.SeriesIDs) > 0) && !c.identifies(sub) {
		return false
	}
	if len(c.Seasons) > 0 && (sub.Type != bazarr.TypeEpisode || !slices.Contains(c.Seasons, sub.Season)) {
		return false
	}
	if len(c.Languages) > 0 && !slices.ContainsFunc(c.Languages, func(l string) bool { return strings.EqualFold(l, sub.Language) }) {
		return false
	}
	return c.HI.admits(sub.HI) && c.Forced.admits(sub.Forced)
}

// identifies says whether sub's movie or series matches one of the titles
// or IDs.
func (c Criteria) identifies(sub bazarr.Subtitle) bool {
	if slices.ContainsFunc(c.Titles, func(t string) bool { return titleContains(sub.Title, t) }) {
		return true
	}
	if sub.Type == bazarr.TypeMovie {
		return slices.Contains(c.MovieIDs, sub.MovieID)
	}
	return slices.Contains(c.SeriesIDs, sub.SeriesID)
}

func (f Flag) admits(set bool) bool {
	switch f {
	case Only:
		return set
	case Without:
		return !set
	}
	return true
}

// titleContains is F6's title match: the movie or series title contains
// text, ignoring case.
func titleContains(title, text string) bool {
	return strings.Contains(foldCase(title), foldCase(text))
}

// foldCase maps each letter to one member of its Unicode case class, as
// strings.EqualFold compares them: lowercasing alone keeps σ and ς apart.
func foldCase(s string) string {
	return strings.Map(func(r rune) rune {
		least := r
		for f := unicode.SimpleFold(r); f != r; f = unicode.SimpleFold(f) {
			least = min(least, f)
		}
		return least
	}, s)
}

// notFound judges G8 against every movie and series Bazarr lists, with or
// without subtitles, so that an ID of a video without any is not an error.
func notFound(c Criteria, lib bazarr.Library) error {
	var e NotFoundError
	for _, t := range c.Titles {
		found := slices.ContainsFunc(lib.Movies, func(m bazarr.Movie) bool { return titleContains(m.Title, t) }) ||
			slices.ContainsFunc(lib.Series, func(s bazarr.Series) bool { return titleContains(s.Title, t) })
		if !found {
			e.Titles = append(e.Titles, t)
		}
	}
	for _, id := range c.MovieIDs {
		if !slices.ContainsFunc(lib.Movies, func(m bazarr.Movie) bool { return m.ID == id }) {
			e.MovieIDs = append(e.MovieIDs, id)
		}
	}
	for _, id := range c.SeriesIDs {
		if !slices.ContainsFunc(lib.Series, func(s bazarr.Series) bool { return s.ID == id }) {
			e.SeriesIDs = append(e.SeriesIDs, id)
		}
	}
	if len(e.Titles) == 0 && len(e.MovieIDs) == 0 && len(e.SeriesIDs) == 0 {
		return nil
	}
	return &e
}
