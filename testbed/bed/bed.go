// Package bed is the hand-off between the test bed and the checks run
// against it: the file the bed's bring-up writes, holding Bazarr's address,
// its API key, and the library the bed placed on disk.
package bed

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"slices"
	"sort"

	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/bazarr"
)

// EnvVar names the file a check reads the bed from. A check against the bed
// runs only when it is set: never on the product's own BAZARR_URL and
// BAZARR_API_KEY, so a plain `go test ./...` stays offline in any shell.
const EnvVar = "BAZARRBULKSYNC_TESTBED"

// Manifest is what the bed placed and where Bazarr answers.
type Manifest struct {
	URL    string
	APIKey string
	// BazarrImage is the image Bazarr runs from.
	BazarrImage string
	// Movies and Series are every movie and series the bed added, in the
	// order Bazarr lists them; the IDs are Radarr's and Sonarr's.
	Movies []Video
	Series []Video
	// Subtitles are the external subtitle files placed, as Bazarr's client
	// hands them out, the path byte for byte.
	Subtitles []bazarr.Subtitle
	// Embedded names the videos the bed gave an embedded subtitle track, by
	// their video file's path.
	Embedded []string
	// Container is the Bazarr container, which sees the media volume as
	// Bazarr does; ReadFile reads through it.
	Container string
	// Placed holds the SHA-256, in hexadecimal, of each placed subtitle
	// file's bytes as the bring-up left them, by path.
	Placed map[string]string
	// Planted is the text placed in subtitle files for the checks of fix.
	Planted []Planted
}

// Planted is text the bed placed in a subtitle file for a fix of F10: the
// file holds Before, and the fix leaves After where Before was.
type Planted struct {
	Path, Fix, Before, After string
}

// Video is a movie or a series as Radarr or Sonarr added it.
type Video struct {
	ID    int
	Title string
}

// Load reads the manifest EnvVar names. It returns ok false when EnvVar is
// not set.
func Load() (m Manifest, ok bool, err error) {
	path := os.Getenv(EnvVar)
	if path == "" {
		return Manifest{}, false, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Manifest{}, true, fmt.Errorf("%s: %w", EnvVar, err)
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return Manifest{}, true, fmt.Errorf("%s: %s is not a test bed file: %w", EnvVar, path, err)
	}
	if m.Container == "" || len(m.Subtitles) == 0 || len(m.Placed) != len(m.Subtitles) {
		return Manifest{}, true, fmt.Errorf("%s: %s was written by an older bring-up; run `go run ./testbed up`", EnvVar, path)
	}
	return m, true, nil
}

// Write writes the manifest to path, readable by its owner only, since it
// holds the API key. A temporary file renamed into place keeps an
// interrupted write from leaving half a manifest.
func (m Manifest) Write(path string) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(path+".tmp", append(data, '\n'), 0o600); err != nil {
		return err
	}
	// WriteFile keeps the mode of a file already there.
	if err := os.Chmod(path+".tmp", 0o600); err != nil {
		return err
	}
	return os.Rename(path+".tmp", path)
}

// ReadFile returns the bytes of a file on the media volume as Bazarr sees
// it, or an error wrapping fs.ErrNotExist when there is none.
func (m Manifest) ReadFile(path string) ([]byte, error) {
	cmd := exec.Command("docker", "exec", m.Container, "sh", "-c", `test -f "$1" || exit 3; cat -- "$1"`, "sh", path)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	data, err := cmd.Output()
	if exitErr := (*exec.ExitError)(nil); errors.As(err, &exitErr) && exitErr.ExitCode() == 3 {
		return nil, fmt.Errorf("%s: %w", path, fs.ErrNotExist)
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s in %s: %v: %s", path, m.Container, err, stderr.Bytes())
	}
	return data, nil
}

// Compare returns the placed subtitles the library lacks and the library's
// subtitles the bed did not place, each sorted by path. A subtitle counts
// as shown only with every attribute the bed intended.
func (m Manifest) Compare(lib bazarr.Library) (missing, unexpected []bazarr.Subtitle) {
	shown := lib.Subtitles()
	count := make(map[bazarr.Subtitle]int, len(shown))
	for _, s := range shown {
		count[s]++
	}
	for _, s := range m.Subtitles {
		if count[s] > 0 {
			count[s]--
		} else {
			missing = append(missing, s)
		}
	}
	for _, s := range shown {
		if count[s] > 0 {
			count[s]--
			unexpected = append(unexpected, s)
		}
	}
	byPath := func(subs []bazarr.Subtitle) {
		sort.SliceStable(subs, func(i, j int) bool { return subs[i].Path < subs[j].Path })
	}
	byPath(missing)
	byPath(unexpected)
	return missing, unexpected
}

// Differences describes each way lib differs from the library the bed
// placed: a placed subtitle not shown with every attribute, a subtitle shown
// that the bed did not place, and movies or series other than the bed's or
// out of Bazarr's order. None means Bazarr shows exactly the bed's library.
func (m Manifest) Differences(lib bazarr.Library) []string {
	var diffs []string
	missing, unexpected := m.Compare(lib)
	for _, s := range missing {
		diffs = append(diffs, fmt.Sprintf("placed but not shown: %+v", s))
	}
	for _, s := range unexpected {
		diffs = append(diffs, fmt.Sprintf("shown but not placed: %+v", s))
	}
	var movies, series []Video
	for _, mv := range lib.Movies {
		movies = append(movies, Video{ID: mv.ID, Title: mv.Title})
	}
	for _, s := range lib.Series {
		series = append(series, Video{ID: s.ID, Title: s.Title})
	}
	if !slices.Equal(movies, m.Movies) {
		diffs = append(diffs, fmt.Sprintf("movies %v, want %v in this order", movies, m.Movies))
	}
	if !slices.Equal(series, m.Series) {
		diffs = append(diffs, fmt.Sprintf("series %v, want %v in this order", series, m.Series))
	}
	return diffs
}

// ShownVideo is a movie or an episode as Bazarr's API answers it.
type ShownVideo struct {
	// Path is the video file's path.
	Path string
	// Subtitles holds the path of each of its subtitle entries, nil for an
	// embedded track.
	Subtitles []*string
}

// Shown reads Bazarr's movies and the episodes of the bed's series directly,
// not through the client, so that checks can hold the client and the
// program against Bazarr's own answer. It follows no redirect, which would
// carry the key elsewhere.
func (m Manifest) Shown() ([]ShownVideo, error) {
	calls := []string{"movies"}
	for _, s := range m.Series {
		calls = append(calls, fmt.Sprintf("episodes?seriesid[]=%d", s.ID))
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	var videos []ShownVideo
	for _, call := range calls {
		req, err := http.NewRequest("GET", m.URL+"/api/"+call, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("X-API-KEY", m.APIKey)
		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		var answer struct {
			Data []struct {
				Path      string `json:"path"`
				Subtitles []struct {
					Path *string `json:"path"`
				} `json:"subtitles"`
			} `json:"data"`
		}
		err = json.NewDecoder(resp.Body).Decode(&answer)
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("GET %s: %w", call, err)
		}
		for _, v := range answer.Data {
			video := ShownVideo{Path: v.Path}
			for _, s := range v.Subtitles {
				video.Subtitles = append(video.Subtitles, s.Path)
			}
			videos = append(videos, video)
		}
	}
	return videos, nil
}
