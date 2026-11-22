// Package fakebazarr serves Bazarr's HTTP API over real HTTP from a library
// a test declares, for the fast tests. It checks the API key as Bazarr does,
// records every request it receives, applies the fixes and deletes it
// accepts, and can be told to hold, fail, drop, or garble an answer. Answer
// shapes follow Bazarr v1.6.2's source and the test bed.
package fakebazarr

import (
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// Subtitle is one entry of a video's `subtitles`.
type Subtitle struct {
	// Path is the file path; ignored when Embedded.
	Path string
	// Embedded makes the entry an embedded track, whose path Bazarr gives as null.
	Embedded bool
	Language string // code2
	Forced   bool
	HI       bool
}

// Movie is a movie of the library.
type Movie struct {
	RadarrID  int
	Title     string
	Subtitles []Subtitle
}

// Series is a series of the library.
type Series struct {
	SonarrSeriesID int
	Title          string
	Episodes       []Episode
}

// Episode is an episode of a series.
type Episode struct {
	SonarrEpisodeID int
	Season          int
	Episode         int
	// Title is the episode's own title.
	Title     string
	Subtitles []Subtitle
}

// Library is what the fake serves; movies and series are listed in this order.
type Library struct {
	Movies []Movie
	Series []Series
}

// Config declares the fake. Zero fields take the defaults below.
type Config struct {
	APIKey string
	// BasePath is where Bazarr is served, such as "/bazarr"; empty for the root.
	BasePath  string
	Version   string
	StartTime string // a JSON number, as Bazarr's start_time
	Library   Library
	// HTTPS serves over TLS, offering HTTP/2 and HTTP/1.1, with a certificate
	// that CertificatePEM gives.
	HTTPS bool
}

const (
	DefaultAPIKey    = "fake-api-key-5f3a9c"
	DefaultVersion   = "1.6.2"
	DefaultStartTime = "1763700000.123456"
)

// Behavior is how the fake answers one request.
type Behavior struct {
	kind   int
	status int
	body   string
	// apply makes the change first, as Serve would (Applied).
	apply bool
}

const (
	kindServe = iota
	kindHold
	kindGarble
	kindStatus
	kindBody
	kindReply
	kindDrop
	kindTruncate
)

var (
	// Serve answers as Bazarr would.
	Serve = Behavior{kind: kindServe}
	// Hold accepts the request and never answers until the client gives up.
	Hold = Behavior{kind: kindHold}
	// Garble answers HTTP 200 with a body that is not JSON.
	Garble = Behavior{kind: kindGarble}
	// Drop reads the request and closes the connection without an answer.
	Drop = Behavior{kind: kindDrop}
)

// Applied makes the change a request asks for, as Serve would, then
// answers as b does: a sync queued whose answer is held, dropped, or
// replaced by a proxy's 504.
func Applied(b Behavior) Behavior {
	b.apply = true
	return b
}

// Status answers with the HTTP status code and no Bazarr answer.
func Status(code int) Behavior { return Behavior{kind: kindStatus, status: code} }

// Body answers HTTP 200 with body as JSON, in place of Bazarr's answer.
func Body(body string) Behavior { return Behavior{kind: kindBody, body: body} }

// Reply answers with the HTTP status code and exactly body.
func Reply(code int, body string) Behavior {
	return Behavior{kind: kindReply, status: code, body: body}
}

// Truncate answers with the HTTP status code and a body cut short: it
// declares one byte more than body, sends body, and closes the connection.
func Truncate(code int, body string) Behavior {
	return Behavior{kind: kindTruncate, status: code, body: body}
}

// Request is a request the fake received.
type Request struct {
	Method   string
	Path     string // as sent, escaped
	RawQuery string
	Header   http.Header
	// Body is the request's body as sent.
	Body string
	// N counts requests from 1.
	N int
}

// Params returns the query's parameters, decoded.
func (r Request) Params() url.Values {
	v, _ := url.ParseQuery(r.RawQuery)
	return v
}

// Server is a running fake Bazarr.
type Server struct {
	// URL is the address to give the client: the server's root plus the base path.
	URL string
	cfg Config
	t   testing.TB
	hs  *httptest.Server

	mu       sync.Mutex
	respond  func(Request) Behavior
	requests []Request
	held     chan Request
	closing  chan struct{}
	// The job queue, the histories, and whether events repeat (queue.go,
	// history.go); guarded by mu.
	jobs         []*job
	lastJobID    int
	histories    map[videoKey][]Event
	repeatEvents bool
	// inFlight counts the requests being answered, and mostInFlight the
	// most at once; guarded by mu.
	inFlight, mostInFlight int
}

// New starts a fake Bazarr, closed when the test ends.
func New(t testing.TB, cfg Config) *Server {
	if cfg.APIKey == "" {
		cfg.APIKey = DefaultAPIKey
	}
	if cfg.Version == "" {
		cfg.Version = DefaultVersion
	}
	if cfg.StartTime == "" {
		cfg.StartTime = DefaultStartTime
	}
	cfg.Library = cloneLibrary(cfg.Library) // deletes change the fake's copy only
	s := &Server{cfg: cfg, t: t, held: make(chan Request, 100), closing: make(chan struct{})}
	hs := httptest.NewUnstartedServer(http.HandlerFunc(s.serve))
	if cfg.HTTPS {
		hs.EnableHTTP2 = true
		hs.StartTLS()
	} else {
		hs.Start()
	}
	s.hs = hs
	t.Cleanup(func() {
		close(s.closing)
		hs.Close()
	})
	s.URL = hs.URL + cfg.BasePath
	return s
}

// CertificatePEM is the certificate of an HTTPS fake, PEM-encoded, for a
// client to trust.
func (s *Server) CertificatePEM() []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: s.hs.Certificate().Raw})
}

// Client is an HTTP client that trusts the fake, and speaks HTTP/2 to an
// HTTPS fake.
func (s *Server) Client() *http.Client { return s.hs.Client() }

// APIKey is the key the fake accepts.
func (s *Server) APIKey() string { return s.cfg.APIKey }

// SetVersion sets the version the status reports, which may be empty.
func (s *Server) SetVersion(version string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cfg.Version = version
}

// SetStartTime sets the start time the status reports, as a restart of
// Bazarr changes it; start is a JSON number.
func (s *Server) SetStartTime(start string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cfg.StartTime = start
}

// Respond sets how each later request is answered; nil serves every request.
func (s *Server) Respond(f func(Request) Behavior) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.respond = f
}

// Requests returns every request received so far.
func (s *Server) Requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.requests)
}

// MostAtOnce is the most requests the fake was answering at one moment.
func (s *Server) MostAtOnce() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.mostInFlight
}

// WaitHeld blocks until a request is being held, and returns it.
func (s *Server) WaitHeld() Request {
	s.t.Helper()
	select {
	case r := <-s.held:
		return r
	case <-time.After(10 * time.Second): // a guard against a hang, never a wait the test relies on
		s.t.Fatal("fakebazarr: no request was held within 10s")
		return Request{}
	}
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	s.mu.Lock()
	req := Request{Method: r.Method, Path: r.URL.EscapedPath(), RawQuery: r.URL.RawQuery,
		Header: r.Header.Clone(), Body: string(body), N: len(s.requests) + 1}
	s.requests = append(s.requests, req)
	s.inFlight++
	s.mostInFlight = max(s.mostInFlight, s.inFlight)
	defer func() {
		s.mu.Lock()
		s.inFlight--
		s.mu.Unlock()
	}()
	respond := s.respond
	version, startTime := s.cfg.Version, s.cfg.StartTime
	s.mu.Unlock()

	behavior := Serve
	if respond != nil {
		behavior = respond(req)
	}
	if behavior.apply && behavior.kind != kindServe {
		s.applyChange(r)
	}
	switch behavior.kind {
	case kindHold:
		s.held <- req
		select {
		case <-r.Context().Done():
		case <-s.closing:
		}
		return
	case kindGarble:
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data": [{"radarrId": 1, "title": "trunc`))
		return
	case kindStatus:
		http.Error(w, http.StatusText(behavior.status), behavior.status)
		return
	case kindBody:
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(behavior.body))
		return
	case kindReply:
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(behavior.status)
		_, _ = w.Write([]byte(behavior.body))
		return
	case kindDrop:
		if conn, _, err := http.NewResponseController(w).Hijack(); err == nil {
			conn.Close()
		}
		return
	case kindTruncate:
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Length", strconv.Itoa(len(behavior.body)+1))
		w.WriteHeader(behavior.status)
		_, _ = w.Write([]byte(behavior.body))
		if conn, _, err := http.NewResponseController(w).Hijack(); err == nil {
			conn.Close()
		}
		return
	}

	apiPrefix := s.cfg.BasePath + "/api/"
	if !strings.HasPrefix(r.URL.Path, apiPrefix) {
		webPage(w)
		return
	}
	call := strings.TrimPrefix(r.URL.Path, apiPrefix)
	ch, isChange := changes[call]
	if !isChange && !slices.Contains([]string{"system/status", "system/jobs", "movies", "series", "episodes",
		"movies/history", "episodes/history"}, call) {
		// Bazarr 1.6.2 answers a call it does not have, even under /api/ and
		// without a key, with its web page and HTTP 200, as seen on the test
		// bed.
		webPage(w)
		return
	}
	// Bazarr also takes the key as an `apikey` parameter; the fake does not,
	// so a client sending it there fails here.
	if r.Header.Get("X-API-KEY") != s.cfg.APIKey {
		http.Error(w, `{"message": "Unauthorized"}`, http.StatusUnauthorized)
		return
	}
	if isChange && r.Method == ch.method {
		code, body := ch.answer(s, r.URL.Query())
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_, _ = w.Write([]byte(body))
		return
	}
	if r.Method != http.MethodGet || isChange {
		// The fake does not have GET subtitles (sync's references) or the
		// subtitle uploads; Bazarr's 405 for a method a call lacks.
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusMethodNotAllowed)
		_, _ = w.Write([]byte(`{"message": "The method is not allowed for the requested URL."}` + "\n"))
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var answer any
	switch call {
	case "system/status":
		answer = map[string]any{"data": map[string]any{
			"bazarr_version":  version,
			"start_time":      json.RawMessage(startTime),
			"package_version": "", "operating_system": "Linux", "python_version": "3.12.8",
		}}
	case "system/jobs":
		answer = s.queue()
	case "movies/history":
		answer = s.history("movie", r.URL.Query().Get("radarrid"))
	case "episodes/history":
		answer = s.history("episode", r.URL.Query().Get("episodeid"))
	case "movies":
		answer = s.movies(ids(r, "radarrid[]"))
	case "series":
		answer = s.series(ids(r, "seriesid[]"))
	case "episodes":
		episodeIDs, seriesIDs := ids(r, "episodeid[]"), ids(r, "seriesid[]")
		if len(episodeIDs) == 0 && len(seriesIDs) == 0 {
			http.Error(w, `"Series or Episode ID not provided"`, http.StatusNotFound)
			return
		}
		answer = s.episodes(seriesIDs, episodeIDs)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(answer)
}

// webPage answers as Bazarr's web interface does every path that is not
// one of its API calls.
func webPage(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte("<!doctype html><html lang=\"en\"><head><title>Bazarr</title></head><body></body></html>"))
}

func ids(r *http.Request, name string) []int {
	var out []int
	for _, v := range r.URL.Query()[name] {
		if n, err := strconv.Atoi(v); err == nil {
			out = append(out, n)
		}
	}
	return out
}

func subtitlesJSON(subs []Subtitle) []map[string]any {
	out := []map[string]any{}
	for _, sub := range subs {
		var path any = sub.Path
		if sub.Embedded {
			path = nil
		}
		out = append(out, map[string]any{
			"name": sub.Language, "code2": sub.Language, "code3": sub.Language,
			"path": path, "forced": sub.Forced, "hi": sub.HI,
			"file_size": nil, "embedded_track_id": nil, "id": nil,
		})
	}
	return out
}

// movies answers GET movies: {"data": [...], "total": n}.
func (s *Server) movies(radarrIDs []int) any {
	data := []map[string]any{}
	for _, m := range s.cfg.Library.Movies {
		if len(radarrIDs) > 0 && !slices.Contains(radarrIDs, m.RadarrID) {
			continue
		}
		data = append(data, map[string]any{
			"radarrId": m.RadarrID, "title": m.Title, "monitored": true,
			"subtitles": subtitlesJSON(m.Subtitles), "missing_subtitles": []any{},
		})
	}
	return map[string]any{"data": data, "total": len(data)}
}

// series answers GET series: {"data": [...], "total": n}; a series' own
// `subtitles` is always empty.
func (s *Server) series(seriesIDs []int) any {
	data := []map[string]any{}
	for _, sr := range s.cfg.Library.Series {
		if len(seriesIDs) > 0 && !slices.Contains(seriesIDs, sr.SonarrSeriesID) {
			continue
		}
		data = append(data, map[string]any{
			"sonarrSeriesId": sr.SonarrSeriesID, "title": sr.Title, "monitored": true,
			"episodeFileCount": len(sr.Episodes),
		})
	}
	return map[string]any{"data": data, "total": len(data)}
}

// episodes answers GET episodes: {"data": [...]}; for series IDs, ordered by
// season, then episode, both descending, as Bazarr orders them.
func (s *Server) episodes(seriesIDs, episodeIDs []int) any {
	type row struct {
		seriesID int
		e        Episode
	}
	var rows []row
	for _, sr := range s.cfg.Library.Series {
		for _, e := range sr.Episodes {
			if len(episodeIDs) > 0 {
				if slices.Contains(episodeIDs, e.SonarrEpisodeID) {
					rows = append(rows, row{sr.SonarrSeriesID, e})
				}
			} else if slices.Contains(seriesIDs, sr.SonarrSeriesID) {
				rows = append(rows, row{sr.SonarrSeriesID, e})
			}
		}
	}
	if len(episodeIDs) == 0 {
		slices.SortStableFunc(rows, func(a, b row) int {
			if a.e.Season != b.e.Season {
				return b.e.Season - a.e.Season
			}
			return b.e.Episode - a.e.Episode
		})
	}
	data := []map[string]any{}
	for _, r := range rows {
		data = append(data, map[string]any{
			"sonarrSeriesId": r.seriesID, "sonarrEpisodeId": r.e.SonarrEpisodeID,
			"season": r.e.Season, "episode": r.e.Episode, "title": r.e.Title, "monitored": true,
			"subtitles": subtitlesJSON(r.e.Subtitles), "missing_subtitles": []any{},
		})
	}
	return map[string]any{"data": data}
}
