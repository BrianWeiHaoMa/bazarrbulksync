package bazarr

import (
	"context"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"time"
)

// EventKind is an event's `action` in Bazarr's history, for the kinds that
// are events of a subtitle; every other action is ignored.
type EventKind int

const (
	EventDownload EventKind = iota + 1
	EventManualDownload
	EventUpgrade
	EventUpload
	EventSync
	EventTranslation
)

// Event is one event of a subtitle.
type Event struct {
	Kind EventKind
	// Time is Bazarr's `parsed_timestamp` as given: the local event time to
	// the second, "MM/DD/YY HH:MM:SS" (S4).
	Time string
	at   time.Time
}

// Identity is the event's identity: its action and `parsed_timestamp`
// together, written "<action> <parsed_timestamp>". Bazarr's history gives
// events no identifier (S4); entries with the same identity are one event.
func (e Event) Identity() string { return strconv.Itoa(int(e.Kind)) + " " + e.Time }

// Events are the events of a subtitle, newest first, one per identity.
type Events []Event

// CurrentlySynced reports whether the newest event is a sync.
func (es Events) CurrentlySynced() bool { return len(es) > 0 && es[0].Kind == EventSync }

// Identities returns each event's identity, newest first.
func (es Events) Identities() []string {
	ids := make([]string, len(es))
	for i, e := range es {
		ids[i] = e.Identity()
	}
	return ids
}

// timestampLayout is `parsed_timestamp`'s form, Python's "%x %X" in the C
// locale; Go's two-digit year takes 69 to 99 as 19xx, as Python does.
const timestampLayout = "01/02/06 15:04:05"

type historyAnswer struct {
	Data *[]struct {
		Action          *int    `json:"action"`
		ParsedTimestamp *string `json:"parsed_timestamp"`
		SubtitlesPath   *string `json:"subtitles_path"`
	} `json:"data"`
}

// complete requires every entry's action, and a time that parses for each
// event of a subtitle's kinds, which ordering needs.
func (a *historyAnswer) complete() bool {
	if a.Data == nil {
		return false
	}
	for _, e := range *a.Data {
		if e.Action == nil {
			return false
		}
		if isEventKind(*e.Action) {
			if e.ParsedTimestamp == nil {
				return false
			}
			if _, err := time.Parse(timestampLayout, *e.ParsedTimestamp); err != nil {
				return false
			}
		}
	}
	return true
}

func isEventKind(action int) bool {
	return action >= int(EventDownload) && action <= int(EventTranslation)
}

// Events reads the events of sub: those whose `subtitles_path` equals its
// path, byte for byte, in the whole history of its video and of each of
// others, such as the other episodes of a multi-episode file. Each history
// is one read under G3.
func (c *Client) Events(ctx context.Context, sub Subtitle, others []Video) (Events, Retried, error) {
	videos := []Video{sub.Video()}
	for _, v := range others {
		if !slices.Contains(videos, v) {
			videos = append(videos, v)
		}
	}
	var anyRetried Retried
	histories := make([]Events, 0, len(videos))
	for _, v := range videos {
		events, retried, err := c.history(ctx, v, sub.Path)
		anyRetried = anyRetried || retried
		if err != nil {
			return nil, anyRetried, err
		}
		histories = append(histories, events)
	}
	return mergeHistories(histories), anyRetried, nil
}

// history reads the whole history of v (no paging parameters: Bazarr then
// returns every event) and keeps the events at path, in Bazarr's order.
func (c *Client) history(ctx context.Context, v Video, path string) (Events, Retried, error) {
	if v.ID <= 0 {
		// Bazarr reads a missing or zero ID as every video's history.
		return nil, false, fmt.Errorf("history of %s %d: not a video ID", v.Type, v.ID)
	}
	call, param := "movies/history", "radarrid"
	if v.Type == TypeEpisode {
		call, param = "episodes/history", "episodeid"
	}
	var answer *historyAnswer
	retried, err := c.read(ctx, call, url.Values{param: {strconv.Itoa(v.ID)}},
		func() any { answer = &historyAnswer{}; return answer })
	if err != nil {
		return nil, retried, err
	}
	var events Events
	for _, e := range *answer.Data {
		if !isEventKind(*e.Action) || e.SubtitlesPath == nil || *e.SubtitlesPath != path {
			continue
		}
		at, _ := time.Parse(timestampLayout, *e.ParsedTimestamp) // complete checked it
		events = append(events, Event{Kind: EventKind(*e.Action), Time: *e.ParsedTimestamp, at: at})
	}
	return events, retried, nil
}

// mergeHistories makes one newest-first list of the events of several
// histories, each newest first as Bazarr returned it: each history keeps its
// order, and across histories the later time is the newer, where of two
// events in the same second a sync is not the newer. An identity already
// taken is one event (S4).
func mergeHistories(histories []Events) Events {
	var merged Events
	seen := map[string]bool{}
	next := make([]int, len(histories))
	for {
		pick := -1
		for h, events := range histories {
			if next[h] == len(events) {
				continue
			}
			if pick < 0 || newer(events[next[h]], histories[pick][next[pick]]) {
				pick = h
			}
		}
		if pick < 0 {
			return merged
		}
		e := histories[pick][next[pick]]
		next[pick]++
		if !seen[e.Identity()] {
			seen[e.Identity()] = true
			merged = append(merged, e)
		}
	}
}

// newer reports whether a, from one history, is newer than b, from another.
func newer(a, b Event) bool {
	if !a.at.Equal(b.at) {
		return a.at.After(b.at)
	}
	return a.Kind != EventSync && b.Kind == EventSync
}
