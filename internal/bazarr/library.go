package bazarr

import (
	"context"
	"net/url"
	"strconv"
)

// VideoType is the type of video a subtitle belongs to; its values are the
// ones `list --json` prints (F7).
type VideoType string

const (
	TypeMovie   VideoType = "movie"
	TypeEpisode VideoType = "episode"
)

// A Subtitle is an external subtitle: an entry of a video's subtitles whose
// path is not null. Path and video ID together identify it.
type Subtitle struct {
	Type VideoType
	// MovieID is the Radarr ID; zero for an episode's subtitle.
	MovieID int
	// SeriesID and EpisodeID are the Sonarr IDs; zero for a movie's subtitle.
	SeriesID  int
	EpisodeID int
	// Title is the movie's or the series' title, never an episode's own.
	Title string
	// Season and Episode number an episode; zero for a movie's subtitle,
	// where they do not apply (season 0 is a real season of a series).
	Season  int
	Episode int
	// Language is Bazarr's code2.
	Language string
	HI       bool
	Forced   bool
	// Path is byte for byte as Bazarr gave it.
	Path string
}

// Movie is a movie Bazarr lists, with its external subtitles.
type Movie struct {
	ID        int // Radarr ID
	Title     string
	Subtitles []Subtitle
}

// Series is a series Bazarr lists, with its episodes.
type Series struct {
	ID       int // Sonarr series ID
	Title    string
	Episodes []Episode
}

// Episode is an episode of a series, with its external subtitles.
type Episode struct {
	ID        int // Sonarr episode ID
	Season    int
	Number    int
	Subtitles []Subtitle
}

// Library is every movie and series Bazarr lists, each in the order
// Bazarr's answer gives, with or without external subtitles.
type Library struct {
	Movies []Movie
	Series []Series
}

// Subtitles returns every external subtitle of the library: movies first,
// then episodes, each in the library's order.
func (l Library) Subtitles() []Subtitle {
	var all []Subtitle
	for _, m := range l.Movies {
		all = append(all, m.Subtitles...)
	}
	for _, s := range l.Series {
		for _, e := range s.Episodes {
			all = append(all, e.Subtitles...)
		}
	}
	return all
}

// subtitleEntry is one entry of a video's `subtitles` in Bazarr's answers.
type subtitleEntry struct {
	Path   *string `json:"path"`
	Code2  string  `json:"code2"`
	Forced bool    `json:"forced"`
	HI     bool    `json:"hi"`
}

// externalSubtitles returns the entries whose path is not null, each as a
// Subtitle of the video that video describes.
func externalSubtitles(entries []subtitleEntry, video Subtitle) []Subtitle {
	var subs []Subtitle
	for _, e := range entries {
		if e.Path == nil {
			continue // an embedded track
		}
		sub := video
		sub.Language, sub.HI, sub.Forced, sub.Path = e.Code2, e.HI, e.Forced, *e.Path
		subs = append(subs, sub)
	}
	return subs
}

type moviesAnswer struct {
	Data *[]struct {
		RadarrID  *int            `json:"radarrId"`
		Title     string          `json:"title"`
		Subtitles []subtitleEntry `json:"subtitles"`
	} `json:"data"`
}

func (a *moviesAnswer) complete() bool {
	if a.Data == nil {
		return false
	}
	for _, m := range *a.Data {
		if m.RadarrID == nil {
			return false
		}
	}
	return true
}

type seriesAnswer struct {
	Data *[]struct {
		SonarrSeriesID *int   `json:"sonarrSeriesId"`
		Title          string `json:"title"`
	} `json:"data"`
}

func (a *seriesAnswer) complete() bool {
	if a.Data == nil {
		return false
	}
	for _, s := range *a.Data {
		if s.SonarrSeriesID == nil {
			return false
		}
	}
	return true
}

type episodesAnswer struct {
	Data *[]struct {
		SonarrSeriesID  *int            `json:"sonarrSeriesId"`
		SonarrEpisodeID *int            `json:"sonarrEpisodeId"`
		Season          int             `json:"season"`
		Episode         int             `json:"episode"`
		Subtitles       []subtitleEntry `json:"subtitles"`
	} `json:"data"`
}

func (a *episodesAnswer) complete() bool {
	if a.Data == nil {
		return false
	}
	for _, e := range *a.Data {
		if e.SonarrSeriesID == nil || e.SonarrEpisodeID == nil {
			return false
		}
	}
	return true
}

// episodesBatch is how many series one episodes read asks for, which keeps
// each request's URL and Bazarr's work for it bounded on a large library.
const episodesBatch = 25

// Library reads every movie and series Bazarr lists, with the external
// subtitles of each movie and episode. Each request is a read under G3.
func (c *Client) Library(ctx context.Context) (Library, Retried, error) {
	var lib Library
	var anyRetried Retried

	var movies *moviesAnswer
	retried, err := c.read(ctx, "movies", nil, func() any { movies = &moviesAnswer{}; return movies })
	anyRetried = anyRetried || retried
	if err != nil {
		return Library{}, anyRetried, err
	}
	for _, m := range *movies.Data {
		lib.Movies = append(lib.Movies, Movie{ID: *m.RadarrID, Title: m.Title,
			Subtitles: externalSubtitles(m.Subtitles, Subtitle{Type: TypeMovie, MovieID: *m.RadarrID, Title: m.Title})})
	}

	var series *seriesAnswer
	retried, err = c.read(ctx, "series", nil, func() any { series = &seriesAnswer{}; return series })
	anyRetried = anyRetried || retried
	if err != nil {
		return Library{}, anyRetried, err
	}
	index := make(map[int]int, len(*series.Data))
	for _, s := range *series.Data {
		index[*s.SonarrSeriesID] = len(lib.Series)
		lib.Series = append(lib.Series, Series{ID: *s.SonarrSeriesID, Title: s.Title})
	}

	for start := 0; start < len(lib.Series); start += episodesBatch {
		query := url.Values{}
		for _, s := range lib.Series[start:min(start+episodesBatch, len(lib.Series))] {
			query.Add("seriesid[]", strconv.Itoa(s.ID))
		}
		var episodes *episodesAnswer
		retried, err = c.read(ctx, "episodes", query, func() any { episodes = &episodesAnswer{}; return episodes })
		anyRetried = anyRetried || retried
		if err != nil {
			return Library{}, anyRetried, err
		}
		for _, e := range *episodes.Data {
			i, ok := index[*e.SonarrSeriesID]
			if !ok {
				continue
			}
			s := &lib.Series[i]
			s.Episodes = append(s.Episodes, Episode{ID: *e.SonarrEpisodeID, Season: e.Season, Number: e.Episode,
				Subtitles: externalSubtitles(e.Subtitles, Subtitle{Type: TypeEpisode, SeriesID: s.ID,
					EpisodeID: *e.SonarrEpisodeID, Title: s.Title, Season: e.Season, Episode: e.Episode})})
		}
	}
	return lib, anyRetried, nil
}
