package bazarr

import (
	"context"
	"net/url"
	"strconv"
)

// Video is a movie, by its Radarr ID, or an episode, by its Sonarr episode ID.
type Video struct {
	Type VideoType
	ID   int
}

// Video is the video sub belongs to.
func (s Subtitle) Video() Video {
	if s.Type == TypeEpisode {
		return Video{Type: TypeEpisode, ID: s.EpisodeID}
	}
	return Video{Type: TypeMovie, ID: s.MovieID}
}

// VideoNow is a video as Bazarr shows it now.
type VideoNow struct {
	// Listed is false when Bazarr no longer lists the video.
	Listed bool
	// Subtitles are its external subtitles now, possibly none.
	Subtitles []Subtitle
}

// VideoNow reads sub's video again, a read under G3. The video as read
// replaces what the selection gave: each subtitle it shows carries the
// movie's title, or the episode's series ID, season, and episode, as Bazarr
// now gives them, and its own language, flags, and path. An episode's
// answer has only the episode's own title, so the series' title stays the
// selected one. A video Bazarr no longer has is a successful read that
// shows it unlisted.
func (c *Client) VideoNow(ctx context.Context, sub Subtitle) (VideoNow, Retried, error) {
	v := sub.Video()
	if v.Type == TypeEpisode {
		var answer *episodesAnswer
		retried, err := c.read(ctx, "episodes", url.Values{"episodeid[]": {strconv.Itoa(v.ID)}},
			func() any { answer = &episodesAnswer{}; return answer })
		if err != nil {
			return VideoNow{}, retried, err
		}
		for _, e := range *answer.Data {
			if *e.SonarrEpisodeID == v.ID {
				video := Subtitle{Type: TypeEpisode, SeriesID: *e.SonarrSeriesID, EpisodeID: v.ID,
					Title: sub.Title, Season: e.Season, Episode: e.Episode}
				return VideoNow{Listed: true, Subtitles: externalSubtitles(e.Subtitles, video)}, retried, nil
			}
		}
		return VideoNow{}, retried, nil
	}
	var answer *moviesAnswer
	retried, err := c.read(ctx, "movies", url.Values{"radarrid[]": {strconv.Itoa(v.ID)}},
		func() any { answer = &moviesAnswer{}; return answer })
	if err != nil {
		return VideoNow{}, retried, err
	}
	for _, m := range *answer.Data {
		if *m.RadarrID == v.ID {
			video := Subtitle{Type: TypeMovie, MovieID: v.ID, Title: m.Title}
			return VideoNow{Listed: true, Subtitles: externalSubtitles(m.Subtitles, video)}, retried, nil
		}
	}
	return VideoNow{}, retried, nil
}
