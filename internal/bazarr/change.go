package bazarr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Fix is one of Bazarr's subtitle modifications of F10, the only fixes the
// client can send (R3, N3).
type Fix int

const (
	FixRemoveHI Fix = iota + 1
	FixRemoveTags
	FixOCR
	FixCommon
	FixUppercase
	FixEmoji
)

// Fixes are the six fixes in F10's order.
var Fixes = []Fix{FixRemoveHI, FixRemoveTags, FixOCR, FixCommon, FixUppercase, FixEmoji}

// action is the word `PATCH subtitles` names fix by. Bazarr 1.6.2 accepts a
// word it does not know, even a differently cased one, with HTTP 204, so
// each word is exactly the technical specification's.
func (f Fix) action() (string, bool) {
	switch f {
	case FixRemoveHI:
		return "remove_HI", true
	case FixRemoveTags:
		return "remove_tags", true
	case FixOCR:
		return "OCR_fixes", true
	case FixCommon:
		return "common", true
	case FixUppercase:
		return "fix_uppercase", true
	case FixEmoji:
		return "emoji", true
	}
	return "", false
}

// ChangeOutcome is what is known of a change once the client returns.
type ChangeOutcome int

const (
	// NotSent: a stop came before the request was sent; Bazarr changed nothing.
	NotSent ChangeOutcome = iota
	// Accepted: Bazarr answered HTTP 204.
	Accepted
	// Refused: Bazarr answered any other status except 502, 503, and 504.
	Refused
	// Unanswered: no complete answer within the time limit, or 502, 503, or
	// 504, which a proxy sends when Bazarr did not answer it.
	Unanswered
	// Stopped: a stop abandoned the request in flight; Bazarr may or may not
	// have made the change.
	Stopped
)

// ChangeResult is the outcome of one change.
type ChangeResult struct {
	Outcome ChangeOutcome
	// Reason is Bazarr's reason for a refusal, on one line.
	Reason string
	// StartTime is Bazarr's start time as read after an unanswered change,
	// when that read succeeded; empty otherwise.
	StartTime string
}

// errUnknownFix is returned, with nothing sent, for a Fix outside the six.
var errUnknownFix = errors.New("not one of Bazarr's six fixes")

// Fix asks Bazarr once to apply fix to sub. The error is non-nil only when
// the run must end: ErrStopped, or an *UnreachableError when Bazarr did not
// answer the start-time read that follows an unanswered change (G3). The
// result says what is known of the change either way.
func (c *Client) Fix(ctx context.Context, sub Subtitle, fix Fix) (ChangeResult, error) {
	action, ok := fix.action()
	if !ok {
		return ChangeResult{}, fmt.Errorf("fix %d: %w", fix, errUnknownFix)
	}
	return c.change(ctx, http.MethodPatch, "subtitles", subtitleQuery(action, sub))
}

// SyncOptions are the sync options of F8 a sync request carries; the zero
// value of each is the option not given, which the request leaves out (G16).
type SyncOptions struct {
	// Reference is "a:N" or "s:N" as given, or the path of the reference
	// subtitle the caller resolved from "lang:CODE".
	Reference string
	// MaxOffsetSeconds is at least 1 when given.
	MaxOffsetSeconds int
	NoFixFramerate   bool
	GSS              bool
}

// Sync asks Bazarr once to synchronize sub, with the same result as Fix.
// Bazarr queues the sync and answers before it runs (S1).
func (c *Client) Sync(ctx context.Context, sub Subtitle, opts SyncOptions) (ChangeResult, error) {
	query := subtitleQuery("sync", sub)
	if opts.Reference != "" {
		query.Set("reference", opts.Reference)
	}
	if opts.MaxOffsetSeconds > 0 {
		query.Set("max_offset_seconds", strconv.Itoa(opts.MaxOffsetSeconds))
	}
	// Bazarr reads these two as given only when they are "True".
	if opts.NoFixFramerate {
		query.Set("no_fix_framerate", "True")
	}
	if opts.GSS {
		query.Set("gss", "True")
	}
	return c.change(ctx, http.MethodPatch, "subtitles", query)
}

// subtitleQuery is what `PATCH subtitles` needs to name sub for action.
func subtitleQuery(action string, sub Subtitle) url.Values {
	return url.Values{
		"action":   {action},
		"language": {sub.Language},
		"path":     {sub.Path},
		"type":     {string(sub.Type)},
		"id":       {strconv.Itoa(sub.Video().ID)}, // an episode's own ID, never the series ID `list` prints
		"forced":   {pyBool(sub.Forced)},
		"hi":       {pyBool(sub.HI)},
	}
}

// Delete asks Bazarr once to delete sub, with the same result as Fix.
func (c *Client) Delete(ctx context.Context, sub Subtitle) (ChangeResult, error) {
	query := url.Values{
		"language": {sub.Language},
		"forced":   {pyBool(sub.Forced)},
		"hi":       {pyBool(sub.HI)},
		"path":     {sub.Path},
	}
	call := "movies/subtitles"
	if sub.Type == TypeEpisode {
		call = "episodes/subtitles"
		query.Set("seriesid", strconv.Itoa(sub.SeriesID))
		query.Set("episodeid", strconv.Itoa(sub.EpisodeID))
	} else {
		query.Set("radarrid", strconv.Itoa(sub.MovieID))
	}
	return c.change(ctx, http.MethodDelete, call, query)
}

// pyBool spells a flag as Bazarr compares it: "True" or "False".
func pyBool(b bool) string {
	if b {
		return "True"
	}
	return "False"
}

// change sends one changing request and never repeats it (R3, G24, G27).
// The parameters travel in the query, which Bazarr 1.6.2 reads for every
// change as it reads a form or JSON body.
func (c *Client) change(ctx context.Context, method, call string, query url.Values) (ChangeResult, error) {
	if ctx.Err() != nil {
		return ChangeResult{Outcome: NotSent}, ErrStopped
	}
	sent := c.clock.Now()
	outcome, reason, failure := c.sendChange(ctx, method, call, query)
	switch outcome {
	case Stopped:
		return ChangeResult{Outcome: Stopped}, ErrStopped
	case Accepted, Refused:
		c.failingSince, c.lastFailure = time.Time{}, nil
		return ChangeResult{Outcome: outcome, Reason: reason}, nil
	}
	// Unanswered: G3's window runs from this change, the first unanswered
	// request after the last success; reading the start time before
	// returning keeps a run from sending change after change into an
	// outage, each waiting out its limit.
	if c.failingSince.IsZero() {
		c.failingSince = sent
	}
	c.lastFailure = fmt.Errorf("%s %s: %w", method, call, failure)
	startTime, _, err := c.StartTime(ctx)
	return ChangeResult{Outcome: Unanswered, StartTime: startTime}, err
}

// maxReasonBody bounds how much of a refusal's body is kept.
const maxReasonBody = 64 << 10

// sendChange sends the request under the change limit and classifies the
// answer. failure describes an unanswered change.
func (c *Client) sendChange(ctx context.Context, method, call string, query url.Values) (outcome ChangeOutcome, reason string, failure error) {
	limit := c.limits.Change
	reqCtx, timedOut, release := c.limited(ctx, limit)
	defer release()
	// How a transfer that failed ends: a stop wins over the limit.
	cut := func(err error) (ChangeOutcome, string, error) {
		switch {
		case ctx.Err() != nil:
			return Stopped, "", nil
		case timedOut():
			return Unanswered, "", &timeoutError{limit: limit}
		}
		return Unanswered, "", err
	}

	req, err := http.NewRequestWithContext(reqCtx, method, c.callURL(call, query), nil)
	if err != nil {
		return cut(err)
	}
	// R2: the key travels only in this header.
	req.Header.Set("X-API-KEY", c.apiKey)
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return cut(err)
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusNoContent:
		return Accepted, "", nil
	case http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return Unanswered, "", &statusError{code: resp.StatusCode}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxReasonBody))
	if err == nil {
		// A refusal counts only once its answer is complete.
		_, err = io.Copy(io.Discard, resp.Body)
	}
	if err != nil {
		return cut(err)
	}
	return Refused, c.refusalReason(resp.StatusCode, body), nil
}

// maxReasonBytes bounds a reason so that a result line can carry it.
const maxReasonBytes = 300

// refusalReason reduces a refusal's body to one line without control
// characters or the API key (R2). Bazarr sends its reason as a JSON string,
// which is unquoted; an empty body is named by its status.
func (c *Client) refusalReason(code int, body []byte) string {
	text := string(body)
	var s string
	if json.Unmarshal(body, &s) == nil {
		text = s
	}
	text = strings.ToValidUTF8(text, "�")
	text = strings.Join(strings.FieldsFunc(text, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r) || unicode.In(r, unicode.Zl, unicode.Zp, unicode.Cf)
	}), " ")
	text = strings.ReplaceAll(text, c.apiKey, "[API key]")
	if len(text) > maxReasonBytes {
		cut := maxReasonBytes
		for cut > 0 && !utf8.RuneStart(text[cut]) {
			cut--
		}
		text = text[:cut] + "…"
	}
	if text == "" {
		return (&statusError{code: code}).Error()
	}
	return text
}
