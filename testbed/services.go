package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/bazarr"
	"github.com/BrianWeiHaoMa/bazarrbulksync/testbed/bed"
)

// How long each step of provisioning may take before up fails.
const (
	startTimeout  = 3 * time.Minute  // a service answering its API
	importTimeout = 5 * time.Minute  // Sonarr and Radarr importing the files on disk
	indexTimeout  = 10 * time.Minute // Bazarr showing every placed subtitle
	pollInterval  = 2 * time.Second
	// After this long without every subtitle shown, up asks Bazarr to index
	// every file on disk again, in case its first sync ran before the
	// languages were enabled.
	rescanAfter = time.Minute
)

// provision adds the library to Sonarr and Radarr, has Bazarr sync with
// both and index the subtitle files, and returns what the bed placed with
// the IDs the services gave it.
func provision(ctx context.Context, keys map[string]string) (bed.Manifest, error) {
	sonarr := arr{sonarrService, keys["sonarr"]}
	radarr := arr{radarrService, keys["radarr"]}
	for _, a := range []arr{sonarr, radarr} {
		if err := until(ctx, startTimeout, a.name+" answering", func() (bool, error) {
			return a.do(ctx, "GET", "system/status", nil, nil) == nil, nil
		}); err != nil {
			return bed.Manifest{}, err
		}
	}
	m := bed.Manifest{URL: bazarrService.url(), APIKey: keys["bazarr"], BazarrImage: bazarrImage}
	if err := addMovies(ctx, radarr, &m); err != nil {
		return bed.Manifest{}, err
	}
	if err := addSeries(ctx, sonarr, &m); err != nil {
		return bed.Manifest{}, err
	}
	if err := indexInBazarr(ctx, m); err != nil {
		return bed.Manifest{}, err
	}
	return m, nil
}

// addMovies adds each movie to Radarr at its folder and waits until Radarr
// has imported its file. The movies are added last first, so that Radarr's
// IDs run against Bazarr's order and a check of that order cannot pass on a
// client that sorts by ID.
func addMovies(ctx context.Context, radarr arr, m *bed.Manifest) error {
	if err := radarr.do(ctx, "POST", "rootfolder", map[string]any{"path": moviesRoot}, nil); err != nil {
		return err
	}
	added := make([]bed.Video, len(movies))
	for i := len(movies) - 1; i >= 0; i-- {
		id, title, err := addMovie(ctx, radarr, movies[i])
		if err != nil {
			return err
		}
		added[i] = bed.Video{ID: id, Title: title}
	}
	for i, mv := range movies {
		m.Movies = append(m.Movies, added[i])
		addVideo(m, mv.video, bazarr.Subtitle{Type: bazarr.TypeMovie, MovieID: added[i].ID, Title: added[i].Title})
	}
	return nil
}

func addMovie(ctx context.Context, radarr arr, mv movie) (int, string, error) {
	var found map[string]any
	if err := radarr.do(ctx, "GET", fmt.Sprintf("movie/lookup/tmdb?tmdbId=%d", mv.tmdbID), nil, &found); err != nil {
		return 0, "", err
	}
	found["qualityProfileId"] = 1
	found["rootFolderPath"] = moviesRoot
	found["path"] = mv.folder
	found["monitored"] = false
	found["minimumAvailability"] = "released"
	found["addOptions"] = map[string]any{"searchForMovie": false}
	var added struct {
		ID    int    `json:"id"`
		Title string `json:"title"`
	}
	if err := radarr.do(ctx, "POST", "movie", found, &added); err != nil {
		return 0, "", err
	}
	err := until(ctx, importTimeout, "Radarr importing "+mv.video.path, func() (bool, error) {
		var got struct {
			HasFile bool `json:"hasFile"`
		}
		err := radarr.do(ctx, "GET", fmt.Sprintf("movie/%d", added.ID), nil, &got)
		return err == nil && got.HasFile, nil
	})
	return added.ID, added.Title, err
}

// addSeries adds the series to Sonarr at its folder and waits until Sonarr
// has imported every episode file.
func addSeries(ctx context.Context, sonarr arr, m *bed.Manifest) error {
	if err := sonarr.do(ctx, "POST", "rootfolder", map[string]any{"path": tvRoot}, nil); err != nil {
		return err
	}
	var found []map[string]any
	if err := sonarr.do(ctx, "GET", fmt.Sprintf("series/lookup?term=tvdb:%d", theSeries.tvdbID), nil, &found); err != nil {
		return err
	}
	if len(found) == 0 {
		return fmt.Errorf("no series with TVDB ID %d in Sonarr's lookup", theSeries.tvdbID)
	}
	s := found[0]
	s["qualityProfileId"] = 1
	s["rootFolderPath"] = tvRoot
	s["path"] = theSeries.folder
	s["monitored"] = false
	s["seasonFolder"] = true
	s["addOptions"] = map[string]any{"searchForMissingEpisodes": false, "monitor": "none"}
	var added struct {
		ID    int    `json:"id"`
		Title string `json:"title"`
	}
	if err := sonarr.do(ctx, "POST", "series", s, &added); err != nil {
		return err
	}
	type sonarrEpisode struct {
		ID            int  `json:"id"`
		SeasonNumber  int  `json:"seasonNumber"`
		EpisodeNumber int  `json:"episodeNumber"`
		HasFile       bool `json:"hasFile"`
	}
	ids := map[[2]int]int{}
	if err := until(ctx, importTimeout, "Sonarr importing the episodes", func() (bool, error) {
		var eps []sonarrEpisode
		if err := sonarr.do(ctx, "GET", fmt.Sprintf("episode?seriesId=%d", added.ID), nil, &eps); err != nil {
			return false, nil
		}
		for _, e := range eps {
			if e.HasFile {
				ids[[2]int{e.SeasonNumber, e.EpisodeNumber}] = e.ID
			}
		}
		for _, e := range theSeries.episodes {
			if ids[[2]int{e.season, e.number}] == 0 {
				return false, nil
			}
		}
		return true, nil
	}); err != nil {
		return err
	}
	m.Series = append(m.Series, bed.Video{ID: added.ID, Title: added.Title})
	for _, e := range theSeries.episodes {
		addVideo(m, e.video, bazarr.Subtitle{Type: bazarr.TypeEpisode, SeriesID: added.ID,
			EpisodeID: ids[[2]int{e.season, e.number}], Title: added.Title, Season: e.season, Episode: e.number})
	}
	return nil
}

// addVideo records v's subtitle files, each as the subtitle of the video
// that of describes.
func addVideo(m *bed.Manifest, v video, of bazarr.Subtitle) {
	for _, s := range v.subs {
		sub := of
		sub.Language, sub.HI, sub.Forced, sub.Path = s.language, s.hi, s.forced, subtitlePath(v, s)
		m.Subtitles = append(m.Subtitles, sub)
	}
	if v.embedded {
		m.Embedded = append(m.Embedded, v.path)
	}
}

// indexInBazarr enables the library's languages in Bazarr, which indexes
// only subtitle files of enabled languages, has it sync with Sonarr and
// Radarr, and waits until its API shows exactly the bed's library: every
// placed subtitle with its attributes, nothing else, every movie and series.
func indexInBazarr(ctx context.Context, m bed.Manifest) error {
	conn := bazarr.Connection{URL: m.URL, APIKey: m.APIKey, Limits: bazarr.Limits{Read: 30 * time.Second}}
	var client *bazarr.Client
	var checkErr error
	if err := until(ctx, startTimeout, "Bazarr answering", func() (bool, error) {
		client, checkErr = bazarr.Connect(ctx, conn)
		return checkErr == nil, nil
	}); err != nil {
		return fmt.Errorf("%w; last connection check: %v", err, checkErr)
	}
	languages := url.Values{}
	seen := map[string]bool{}
	for _, s := range m.Subtitles {
		if !seen[s.Language] {
			seen[s.Language] = true
			languages.Add("languages-enabled", s.Language)
		}
	}
	if err := bazarrPost(ctx, m, "system/settings", languages); err != nil {
		return err
	}
	for _, task := range []string{"update_series", "update_movies"} {
		if err := bazarrPost(ctx, m, "system/tasks", url.Values{"taskid": {task}}); err != nil {
			return err
		}
	}
	start := time.Now()
	rescanned := false
	var diffs []string
	err := until(ctx, indexTimeout, "Bazarr showing the library", func() (bool, error) {
		lib, _, err := client.Library(ctx)
		if err != nil {
			return false, err
		}
		diffs = m.Differences(lib)
		if len(diffs) > 0 && !rescanned && time.Since(start) > rescanAfter {
			rescanned = true
			for _, task := range []string{"series_full_scan_subtitles", "movies_full_scan_subtitles"} {
				if err := bazarrPost(ctx, m, "system/tasks", url.Values{"taskid": {task}}); err != nil {
					return false, err
				}
			}
		}
		return len(diffs) == 0, nil
	})
	if err != nil && len(diffs) > 0 {
		return fmt.Errorf("%w:\n  %s", err, strings.Join(diffs, "\n  "))
	}
	return err
}

// bazarrPost sends a form to one of Bazarr's API calls and expects success.
func bazarrPost(ctx context.Context, m bed.Manifest, call string, form url.Values) error {
	req, err := http.NewRequestWithContext(ctx, "POST", m.URL+"/api/"+call, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-API-KEY", m.APIKey)
	return send(req, nil)
}

// arr is Sonarr's or Radarr's v3 API.
type arr struct {
	service
	key string
}

func (a arr) do(ctx context.Context, method, call string, body, answer any) error {
	var r io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		r = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, a.url()+"/api/v3/"+call, r)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Api-Key", a.key)
	return send(req, answer)
}

// httpClient follows no redirect, which would carry the key elsewhere.
var httpClient = &http.Client{Timeout: 60 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

// send sends req and decodes a 2xx answer into answer, when given. An error
// names the call and the answer, never the request's headers.
func send(req *http.Request, answer any) error {
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", req.Method, req.URL.Path, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("%s %s: %w", req.Method, req.URL.Path, err)
	}
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("%s %s: HTTP %d: %.300s", req.Method, req.URL.Path, resp.StatusCode, data)
	}
	if answer != nil {
		if err := json.Unmarshal(data, answer); err != nil {
			return fmt.Errorf("%s %s: %w", req.Method, req.URL.Path, err)
		}
	}
	return nil
}

// until calls done every pollInterval until it reports true, fails, or
// timeout passes.
func until(ctx context.Context, timeout time.Duration, what string, done func() (bool, error)) error {
	deadline := time.Now().Add(timeout)
	for {
		ok, err := done()
		if err != nil {
			return fmt.Errorf("%s: %w", what, err)
		}
		if ok {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%s: not done after %s", what, timeout)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("%s: %w", what, ctx.Err())
		case <-time.After(pollInterval):
		}
	}
}
