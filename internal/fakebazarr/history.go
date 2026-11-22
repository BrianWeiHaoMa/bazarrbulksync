package fakebazarr

import (
	"maps"
	"slices"
	"strconv"
)

// Event is one entry of a video's history.
type Event struct {
	// Action is Bazarr's action: 1 download, 2 manual download, 3 upgrade,
	// 4 upload, 5 sync, 6 translation; others are Bazarr's other records.
	Action int
	// Time is the `parsed_timestamp`, "MM/DD/YY HH:MM:SS".
	Time string
	// Path is the `subtitles_path`; empty gives null.
	Path string
}

// AddMovieEvent adds e to the movie's history as its newest event.
func (s *Server) AddMovieEvent(radarrID int, e Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.addEvent(videoKey{"movie", radarrID}, e)
}

// AddEpisodeEvent adds e to the episode's history as its newest event.
func (s *Server) AddEpisodeEvent(episodeID int, e Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.addEvent(videoKey{"episode", episodeID}, e)
}

// RepeatEvents makes each history entry appear once per external subtitle
// of its video, as Bazarr 1.6.0 does (S4); 1.6.2 does not.
func (s *Server) RepeatEvents(on bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.repeatEvents = on
}

// addEvent appends e to the video's history; the caller holds mu.
func (s *Server) addEvent(v videoKey, e Event) {
	if s.histories == nil {
		s.histories = map[videoKey][]Event{}
	}
	s.histories[v] = append(s.histories[v], e)
}

// history answers GET movies/history or episodes/history: the video's
// events newest first, every event (Bazarr pages only when asked to), or
// every video's events of that type without an ID, as Bazarr does; the
// caller holds mu.
func (s *Server) history(typ, id string) any {
	want, err := strconv.Atoi(id)
	all := id == "" || err != nil || want == 0
	videos := slices.SortedFunc(maps.Keys(s.histories), func(a, b videoKey) int { return a.id - b.id })
	data := []map[string]any{}
	for _, v := range videos {
		if v.typ != typ || (!all && v.id != want) {
			continue
		}
		events := s.histories[v]
		repeat := 1
		if s.repeatEvents {
			repeat = max(1, s.externalSubtitles(v))
		}
		for _, e := range slices.Backward(events) {
			for range repeat {
				data = append(data, s.historyEntry(v, e))
			}
		}
	}
	return map[string]any{"data": data, "total": len(data)}
}

// historyEntry is one entry as Bazarr 1.6.2 gives it, without an event
// identifier.
func (s *Server) historyEntry(v videoKey, e Event) map[string]any {
	var subPath any
	if e.Path != "" {
		subPath = e.Path
	}
	entry := map[string]any{
		"action": e.Action, "parsed_timestamp": e.Time, "subtitles_path": subPath,
		"timestamp": "a minute ago", "description": "English subtitles synchronization ended with an offset of 0 seconds.",
		"language":  map[string]any{"name": "English", "code2": "en", "code3": "eng", "forced": false, "hi": false},
		"monitored": false, "tags": []any{}, "score": nil, "subs_id": nil, "provider": nil,
		"upgradable": false, "blacklisted": false, "matches": []any{}, "dont_matches": []any{},
	}
	if v.typ == "movie" {
		entry["radarrId"], entry["title"], entry["path"] = v.id, "", nil
	} else {
		entry["sonarrEpisodeId"], entry["sonarrSeriesId"], entry["seriesTitle"] = v.id, 0, ""
		entry["episode_number"], entry["episodeTitle"] = "", ""
	}
	return entry
}

// externalSubtitles counts the video's external subtitles; the caller
// holds mu.
func (s *Server) externalSubtitles(v videoKey) int {
	var subs []Subtitle
	if v.typ == "movie" {
		if m := s.movie(v.id); m != nil {
			subs = m.Subtitles
		}
	} else if _, e := s.episode(v.id); e != nil {
		subs = e.Subtitles
	}
	n := 0
	for _, sub := range subs {
		if !sub.Embedded {
			n++
		}
	}
	return n
}
