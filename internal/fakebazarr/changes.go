package fakebazarr

import (
	"encoding/json"
	"net/http"
	"net/url"
	"path"
	"slices"
	"strconv"
)

// change is how the fake answers one changing call: its method, and the
// status and body Bazarr 1.6.2 answers, empty with 204 and otherwise mostly
// its reason as a JSON string.
type change struct {
	method string
	answer func(*Server, url.Values) (int, string)
}

// changes are the changing calls the fake has, by call. They read their
// parameters from the query only: Bazarr also reads a form or JSON body
// (seen on the test bed), but the client sends the query, and a client that
// moved them would fail here rather than pass.
var changes = map[string]change{
	"subtitles":          {http.MethodPatch, (*Server).patchSubtitles},
	"movies/subtitles":   {http.MethodDelete, (*Server).deleteMovieSubtitles},
	"episodes/subtitles": {http.MethodDelete, (*Server).deleteEpisodeSubtitles},
}

// param describes one of a call's parameters, as Bazarr's request parser
// does: missing, a required one is refused with Bazarr's 400 answer.
type param struct {
	name, help string
	integer    bool
}

// parse returns the parameters, or Bazarr's 400 answer for the first one
// that is missing or, for an integer, does not parse.
func parse(q url.Values, params ...param) (map[string]string, string) {
	got := map[string]string{}
	for _, p := range params {
		v, ok := q[p.name]
		if !ok || len(v) == 0 {
			return nil, inputError(p, "Missing required parameter in the JSON body or the post body or the query string")
		}
		if _, err := strconv.Atoi(v[0]); p.integer && err != nil {
			return nil, inputError(p, "invalid literal for int() with base 10: '"+v[0]+"'")
		}
		got[p.name] = v[0]
	}
	return got, ""
}

// inputError is Bazarr's answer body for a parameter its parser refuses,
// spaced as Python's json module writes it.
func inputError(p param, why string) string {
	return `{"errors": {` + quote(p.name) + `: ` + quote(p.help+" "+why) + `}, "message": "Input payload validation failed"}` + "\n"
}

// reason is Bazarr's body for a refusal: its text as a JSON string.
func reason(text string) string { return quote(text) + "\n" }

func quote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// patchSubtitles answers PATCH subtitles for a fix, as Bazarr 1.6.2 does:
// every action word is taken as a modification's name and accepted for an
// existing SRT file, even a word Bazarr has no modification for, which it
// then applies nothing for. The sync form lands with sync.
func (s *Server) patchSubtitles(q url.Values) (int, string) {
	got, bad := parse(q,
		param{name: "action", help: `Action from ["sync", "translate", "extract" or mods name]`},
		param{name: "language", help: "Language code2"},
		param{name: "type", help: `Media type from ["episode", "movie"]`},
		param{name: "id", help: "Media ID (episodeId, radarrId)", integer: true})
	if bad != "" {
		return http.StatusBadRequest, bad
	}
	subPath := q.Get("path")
	if subPath != "" && !s.hasSubtitle(subPath) {
		return http.StatusInternalServerError, reason("Subtitles file not found. Path mapping issue?")
	}
	if subPath == "" {
		return 501, reason("Path not provided and is required with this action: " + got["action"])
	}
	id, _ := strconv.Atoi(got["id"])
	s.mu.Lock()
	defer s.mu.Unlock()
	if got["type"] == "episode" {
		if _, e := s.episode(id); e == nil {
			return http.StatusNotFound, reason("Episode not found")
		}
	} else if s.movie(id) == nil {
		return http.StatusNotFound, reason("Movie not found")
	}
	if path.Ext(subPath) != ".srt" {
		return http.StatusConflict, reason("Unable to take action on subtitles file. Check logs.")
	}
	return http.StatusNoContent, ""
}

// deleteMovieSubtitles answers DELETE movies/subtitles: the subtitle at path
// of that movie leaves the library.
func (s *Server) deleteMovieSubtitles(q url.Values) (int, string) {
	got, bad := parse(q,
		param{name: "radarrid", help: "Movie ID", integer: true},
		param{name: "language", help: "Language code2"},
		param{name: "forced", help: "Forced true/false as string"},
		param{name: "hi", help: "HI true/false as string"},
		param{name: "path", help: "Path of the subtitles file"})
	if bad != "" {
		return http.StatusBadRequest, bad
	}
	id, _ := strconv.Atoi(got["radarrid"])
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.movie(id)
	if m == nil {
		return http.StatusNotFound, reason("Movie not found")
	}
	if !removeSubtitle(&m.Subtitles, got["path"]) {
		return http.StatusInternalServerError, reason("Subtitles file not found or permission issue.")
	}
	return http.StatusNoContent, ""
}

// deleteEpisodeSubtitles answers DELETE episodes/subtitles: the subtitle at
// path of that episode leaves the library when seriesid is the episode's
// series.
func (s *Server) deleteEpisodeSubtitles(q url.Values) (int, string) {
	got, bad := parse(q,
		param{name: "seriesid", help: "Series ID", integer: true},
		param{name: "episodeid", help: "Episode ID", integer: true},
		param{name: "language", help: "Language code2"},
		param{name: "forced", help: "Forced true/false as string"},
		param{name: "hi", help: "HI true/false as string"},
		param{name: "path", help: "Path of the subtitles file"})
	if bad != "" {
		return http.StatusBadRequest, bad
	}
	seriesID, _ := strconv.Atoi(got["seriesid"])
	episodeID, _ := strconv.Atoi(got["episodeid"])
	s.mu.Lock()
	defer s.mu.Unlock()
	sr, e := s.episode(episodeID)
	if e == nil {
		return http.StatusNotFound, reason("Episode not found")
	}
	if sr.SonarrSeriesID != seriesID || !removeSubtitle(&e.Subtitles, got["path"]) {
		return http.StatusInternalServerError, reason("Subtitles file not found or permission issue.")
	}
	return http.StatusNoContent, ""
}

// movie returns the movie with that Radarr ID; the caller holds mu.
func (s *Server) movie(radarrID int) *Movie {
	for i := range s.cfg.Library.Movies {
		if s.cfg.Library.Movies[i].RadarrID == radarrID {
			return &s.cfg.Library.Movies[i]
		}
	}
	return nil
}

// episode returns the episode with that Sonarr ID and its series; the
// caller holds mu.
func (s *Server) episode(episodeID int) (*Series, *Episode) {
	for i := range s.cfg.Library.Series {
		sr := &s.cfg.Library.Series[i]
		for j := range sr.Episodes {
			if sr.Episodes[j].SonarrEpisodeID == episodeID {
				return sr, &sr.Episodes[j]
			}
		}
	}
	return nil, nil
}

// hasSubtitle reports whether any video has an external subtitle at path,
// which stands in for Bazarr finding the file on disk.
func (s *Server) hasSubtitle(subPath string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	is := func(sub Subtitle) bool { return !sub.Embedded && sub.Path == subPath }
	for _, m := range s.cfg.Library.Movies {
		if slices.ContainsFunc(m.Subtitles, is) {
			return true
		}
	}
	for _, sr := range s.cfg.Library.Series {
		for _, e := range sr.Episodes {
			if slices.ContainsFunc(e.Subtitles, is) {
				return true
			}
		}
	}
	return false
}

// removeSubtitle removes the external subtitle at path from subs.
func removeSubtitle(subs *[]Subtitle, subPath string) bool {
	n := len(*subs)
	*subs = slices.DeleteFunc(*subs, func(sub Subtitle) bool { return !sub.Embedded && sub.Path == subPath })
	return len(*subs) < n
}

// cloneLibrary copies lib down to its subtitles, so that a delete changes
// only the fake's library.
func cloneLibrary(lib Library) Library {
	out := Library{Movies: slices.Clone(lib.Movies), Series: slices.Clone(lib.Series)}
	for i := range out.Movies {
		out.Movies[i].Subtitles = slices.Clone(out.Movies[i].Subtitles)
	}
	for i := range out.Series {
		out.Series[i].Episodes = slices.Clone(out.Series[i].Episodes)
		for j := range out.Series[i].Episodes {
			out.Series[i].Episodes[j].Subtitles = slices.Clone(out.Series[i].Episodes[j].Subtitles)
		}
	}
	return out
}
