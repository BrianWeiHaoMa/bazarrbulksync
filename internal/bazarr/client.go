// Package bazarr is the only part of bazarrbulksync that talks to the network:
// it checks the connection to Bazarr, reads from Bazarr's HTTP API with the
// retrying of G3, and hands out what it read in the product's terms.
package bazarr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"
)

// RetryWindow is how long reads keep being retried (G3), measured from the
// first failed or unanswered request after the last success.
const RetryWindow = 5 * time.Minute

// The wait between attempts doubles from firstRetryWait up to maxRetryWait.
const (
	firstRetryWait = time.Second
	maxRetryWait   = 15 * time.Second
)

// Limits are the time limits requests carry (T1).
type Limits struct {
	// Read limits each read, the connection check included.
	Read time.Duration
}

// DefaultLimits holds every limit at 60 seconds until they are measured
// against a real Bazarr (T1).
var DefaultLimits = Limits{Read: 60 * time.Second}

// Connection is what the client needs to reach Bazarr.
type Connection struct {
	// URL is Bazarr's address including any base path, as the user gave it.
	URL    string
	APIKey string
	// Clock defaults to SystemClock.
	Clock Clock
	// Limits defaults to DefaultLimits.
	Limits Limits
}

// Status is what the connection check read from Bazarr.
type Status struct {
	// Version is Bazarr's version as Bazarr reports it.
	Version string
	// StartTime is Bazarr's start time, kept as Bazarr's own text so that two
	// reads compare equal exactly when Bazarr has not restarted.
	StartTime string
}

// Retried says whether a read needed more than one attempt.
type Retried bool

// ErrStopped is returned when the context given to an operation ended: the
// request in flight was abandoned and no other was sent.
var ErrStopped = errors.New("stopped")

// UnreachableError is returned when reads failed for RetryWindow (G3).
type UnreachableError struct {
	Address string
	// Last is the last failure.
	Last error
}

func (e *UnreachableError) Error() string {
	return fmt.Sprintf("Bazarr at %s has not answered for %s: %v", e.Address, RetryWindow, e.Last)
}

func (e *UnreachableError) Unwrap() error { return e.Last }

// Client reads from one Bazarr. Only Connect makes one, after a successful
// connection check. It is not safe for concurrent use: a run has at most one
// request in flight.
type Client struct {
	address string
	apiKey  string
	base    *url.URL
	clock   Clock
	limits  Limits
	http    *http.Client
	status  Status
	// failingSince is when the first failed or unanswered request after the
	// last success was sent; zero while the last request succeeded.
	// lastFailure is the latest failure since then, the cause G3 reports.
	failingSince time.Time
	lastFailure  error
}

// Status returns what the connection check read.
func (c *Client) Status() Status { return c.status }

func newClient(conn Connection, base *url.URL) *Client {
	clock := conn.Clock
	if clock == nil {
		clock = SystemClock{}
	}
	limits := conn.Limits
	if limits.Read <= 0 {
		limits.Read = DefaultLimits.Read
	}
	limits.Read = min(limits.Read, RetryWindow) // T1: no limit exceeds G3's window
	transport := http.DefaultTransport.(*http.Transport).Clone()
	// F3: Bazarr's API is the only contact, so no proxy from the environment.
	transport.Proxy = nil
	return &Client{
		address: conn.URL,
		apiKey:  conn.APIKey,
		base:    base,
		clock:   clock,
		limits:  limits,
		http: &http.Client{
			Transport: transport,
			// No Timeout here: each request's limit runs on the client's clock.
			// Redirects are not followed: each read is one request to Bazarr,
			// and following one would send the key wherever it points (F3).
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
}

// callURL is the address with its base path kept, then "/api/" and call.
func (c *Client) callURL(call string, query url.Values) string {
	u := *c.base
	u.RawPath = strings.TrimRight(c.base.EscapedPath(), "/") + "/api/" + call
	u.Path, _ = url.PathUnescape(u.RawPath) // EscapedPath is always a valid escape
	u.RawQuery = query.Encode()
	return u.String()
}

// errUndecodable marks an HTTP 200 whose body is not the answer expected.
var errUndecodable = errors.New("the answer is not what Bazarr sends")

// statusError is an answer other than HTTP 200.
type statusError struct{ code int }

func (e *statusError) Error() string {
	return fmt.Sprintf("HTTP %d %s", e.code, http.StatusText(e.code))
}

// timeoutError is a request abandoned at its time limit.
type timeoutError struct{ limit time.Duration }

func (e *timeoutError) Error() string {
	return fmt.Sprintf("no answer within %s", e.limit)
}

// attempt sends one GET and decodes a 200 answer into into. It returns
// ErrStopped when ctx ended.
func (c *Client) attempt(ctx context.Context, call string, query url.Values, into any, limit time.Duration) error {
	if ctx.Err() != nil {
		return ErrStopped
	}
	reqCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var timedOut atomic.Bool
	stop := c.clock.AfterFunc(limit, func() {
		timedOut.Store(true)
		cancel()
	})
	defer stop()

	err := c.get(reqCtx, call, query, into)
	switch {
	case ctx.Err() != nil:
		return ErrStopped
	case timedOut.Load():
		// Also when the answer was complete only after the limit passed.
		return &timeoutError{limit: limit}
	}
	return err
}

func (c *Client) get(ctx context.Context, call string, query url.Values, into any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.callURL(call, query), nil)
	if err != nil {
		return err
	}
	// R2: the key travels only in this header.
	req.Header.Set("X-API-KEY", c.apiKey)
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		// The status decides; waiting for the body could turn a 401 into a timeout.
		return &statusError{code: resp.StatusCode}
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(body, into); err != nil {
		return errUndecodable
	}
	if v, ok := into.(interface{ complete() bool }); ok && !v.complete() {
		return errUndecodable
	}
	return nil
}

// read performs one read under G3: a failed attempt is retried until one
// succeeds or RetryWindow has passed since c.failingSince, which also cuts
// short the attempt in flight.
func (c *Client) read(ctx context.Context, call string, query url.Values, into func() any) (Retried, error) {
	wait := firstRetryWait
	retried := Retried(false)
	for {
		sent := c.clock.Now()
		limit := c.limits.Read
		if !c.failingSince.IsZero() {
			left := c.failingSince.Add(RetryWindow).Sub(sent)
			if left <= 0 {
				return retried, &UnreachableError{Address: c.address, Last: c.lastFailure}
			}
			limit = min(limit, left)
		}
		err := c.attempt(ctx, call, query, into(), limit)
		if err == nil {
			c.failingSince, c.lastFailure = time.Time{}, nil
			return retried, nil
		}
		if errors.Is(err, ErrStopped) {
			return retried, ErrStopped
		}
		if c.failingSince.IsZero() {
			c.failingSince = sent
		}
		c.lastFailure = fmt.Errorf("%s: %w", call, err)
		retried = true
		left := c.failingSince.Add(RetryWindow).Sub(c.clock.Now())
		if left <= 0 {
			return retried, &UnreachableError{Address: c.address, Last: c.lastFailure}
		}
		if c.clock.Sleep(ctx, min(wait, left)) != nil {
			return retried, ErrStopped
		}
		wait = min(wait*2, maxRetryWait)
	}
}
