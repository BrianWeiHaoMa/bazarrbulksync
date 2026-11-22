package main

import (
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// Every image the bed starts names one exact release, never `latest`.
func TestImagesPinned(t *testing.T) {
	pinned := regexp.MustCompile(`^[a-z0-9./-]+:[0-9]+\.[0-9]+\.[0-9]+$`)
	images := []string{bazarrImage}
	for _, s := range services {
		images = append(images, s.image)
	}
	for _, image := range images {
		if !pinned.MatchString(image) {
			t.Errorf("image %q is not pinned to a release", image)
		}
	}
	if args := prepareArgs(); !slices.Contains(args, bazarrImage) {
		t.Errorf("the library is not placed from the Bazarr image the bed runs")
	}
}

// The library holds what every later check against the bed relies on.
func TestLibraryPlan(t *testing.T) {
	if len(movies) < 2 {
		t.Errorf("%d movies, want at least 2", len(movies))
	}
	seasons := map[int]bool{}
	for _, e := range theSeries.episodes {
		seasons[e.season] = true
	}
	if len(seasons) < 2 {
		t.Errorf("the series has episodes in %d seasons, want at least 2", len(seasons))
	}

	downloaded := map[string]bool{}
	for _, d := range downloads {
		downloaded[d.name] = true
	}
	madeVideos := map[string]bool{}
	for _, p := range preparedVideos {
		madeVideos[p.name] = true
	}
	var nonForced, forced, hiWithSibling, hiAlone, embedded int
	languages := map[string]bool{}
	paths := map[string]bool{}
	for _, v := range videos() {
		if !madeVideos[v.source] {
			t.Errorf("%s comes from %q, which nothing prepares", v.path, v.source)
		}
		if v.embedded {
			embedded++
		}
		for _, s := range v.subs {
			p := subtitlePath(v, s)
			if paths[p] {
				t.Errorf("%s placed twice", p)
			}
			paths[p] = true
			if !downloaded[s.source] || !strings.HasSuffix(p, ".srt") {
				t.Errorf("%s is not an SRT file from a download", p)
			}
			if s.forced {
				forced++
				continue
			}
			nonForced++
			languages[s.language] = true
			if s.hi {
				if slices.ContainsFunc(v.subs, func(o subtitleFile) bool {
					return o.language == s.language && !o.hi && o.forced == s.forced
				}) {
					hiWithSibling++
				} else {
					hiAlone++
				}
			}
		}
	}
	if nonForced < 12 {
		t.Errorf("%d external SRT subtitles that are not forced, want at least 12", nonForced)
	}
	if len(languages) < 3 || !languages["en"] || !languages["es"] {
		t.Errorf("languages %v, want at least three, en and es among them", languages)
	}
	if forced < 1 || hiWithSibling < 1 || hiAlone < 1 {
		t.Errorf("forced %d, HI beside a non-HI of its language %d, HI alone %d; want at least one each",
			forced, hiWithSibling, hiAlone)
	}
	if embedded < 1 {
		t.Errorf("no video with an embedded subtitle track")
	}
	if !slices.ContainsFunc(movies, func(m movie) bool { return len(m.video.subs) == 0 }) {
		t.Errorf("every movie has an external subtitle; want one without")
	}
}

// The text planted for the checks of fix: something for each fix of F10, a
// style tag in every file of the series and in a file of another title,
// hearing-impaired cues in the lone hearing-impaired file, and edits of cue
// text only, each placing what its fix must change.
func TestPlantedText(t *testing.T) {
	planted := map[string]int{}
	for _, v := range videos() {
		for _, s := range v.subs {
			for _, e := range s.edits {
				planted[e.fix]++
				for _, text := range []string{e.line, e.text, e.before, e.after} {
					if strings.TrimSpace(text) == "" || strings.ContainsAny(text, "\r\n") || strings.Contains(text, "-->") {
						t.Errorf("%s: %q is not one line of cue text", subtitlePath(v, s), text)
					}
				}
				if _, err := strconv.Atoi(e.line); err == nil {
					t.Errorf("%s: an edit of the cue number %q", subtitlePath(v, s), e.line)
				}
				placed := e.text
				if s.upper {
					placed = strings.ToUpper(placed)
				}
				if !strings.Contains(placed, e.before) || e.before == e.after {
					t.Errorf("%s: %q does not place %q for %s to change", subtitlePath(v, s), placed, e.before, e.fix)
				}
			}
		}
	}
	for _, fix := range []string{"remove-hi", "remove-tags", "ocr-fixes", "common-fixes", "fix-uppercase", "remove-emoji"} {
		if planted[fix] == 0 {
			t.Errorf("nothing planted for %s", fix)
		}
	}
	tagged := func(s subtitleFile) bool {
		return slices.ContainsFunc(s.edits, func(e edit) bool { return e.fix == "remove-tags" })
	}
	for _, e := range theSeries.episodes {
		for _, s := range e.video.subs {
			if !tagged(s) {
				t.Errorf("%s holds no style tag", subtitlePath(e.video, s))
			}
		}
	}
	var taggedMovie, hiAlone bool
	for _, m := range movies {
		for _, s := range m.video.subs {
			taggedMovie = taggedMovie || tagged(s)
			if s.hi && !slices.ContainsFunc(m.video.subs, func(o subtitleFile) bool { return o.language == s.language && o.suffix != s.suffix }) {
				hiAlone = slices.ContainsFunc(s.edits, func(e edit) bool { return e.fix == "remove-hi" })
			}
		}
	}
	if !taggedMovie || !hiAlone {
		t.Errorf("a style tag outside the series %v, hearing-impaired cues in the lone HI file %v; want both", taggedMovie, hiAlone)
	}
}
