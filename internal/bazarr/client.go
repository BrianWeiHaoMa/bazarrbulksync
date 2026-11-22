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
	// Change limits each sync, fix, or delete request.
	Change time.Duration
}

// DefaultLimits are the limits measured against the test bed's Bazarr 1.6.2
// (T1): idle and while it ran one and four syncs, every read was answered
// within 130 ms and every change within 163 ms (a delete; a fix 152 ms, a
// sync submission 6 ms), the slowest of each while syncs ran. The bed held
// three movies and three episodes then, and the library read grows with the
// library (about 0.4 ms a movie there), so the margin, about 400 times, is
// for libraries thousands of times larger on slower machines. A shorter read
// limit would have a large library's read retried until Bazarr counts as
// unreachable, and a shorter change limit a slow fix reported unconfirmed.
var DefaultLimits = Limits{Read: 60 * time.Second, Change: 60 * time.Second}

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
	return fmt.Sprintf("Bazarr at %s is unreachable: no answer for %v; last failure: %v", e.Address, RetryWindow, e.Last)
}

func (e *UnreachableError) Unwrap() error { return e.Last }

// Client reads from and changes one Bazarr. Only Connect makes one, after a
// successful connection check (G2). It is not safe for concurrent use: a run
// has at most one request in flight.
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

// ReadLimit is the time limit each read carries (T1).
func (c *Client) ReadLimit() time.Duration { return c.limits.Read }

func newClient(conn Connection, base *url.URL) *Client {
	clock := conn.Clock
	if clock == nil {
		clock = SystemClock{}
	}
	limits := conn.Limits
	if limits.Read <= 0 {
		limits.Read = DefaultLimits.Read
	}
	if limits.Change <= 0 {
		limits.Change = DefaultLimits.Change
	}
	// T1: no limit exceeds G3's window.
	limits.Read = min(limits.Read, RetryWindow)
	limits.Change = min(limits.Change, RetryWindow)
	transport := http.DefaultTransport.(*http.Transport).Clone()
	// F3: Bazarr's API is the only contact, so no proxy from the environment.
	transport.Proxy = nil
	// HTTP/1 only: with GODEBUG=http2debug set, Go's HTTP/2 transport logs
	// every header it sends, the API key with them (R2). A run has one
	// request in flight, so HTTP/2 gains it nothing.
	transport.Protocols = new(http.Protocols)
	transport.Protocols.SetHTTP1(true)
	// Cloning set up HTTP/2 on http.DefaultTransport, whose TLS settings, now
	// copied here, offer it to the server; DefaultTransport sets none itself.
	transport.TLSClientConfig = nil
	transport.TLSNextProto = nil
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

// limited returns the context one request runs under: it ends when ctx
// ends or when limit passes on the client's clock (T1), and timedOut says
// whether the limit ended it. release frees the limit's timer.
func (c *Client) limited(ctx context.Context, limit time.Duration) (reqCtx context.Context, timedOut func() bool, release func()) {
	reqCtx, cancel := context.WithCancel(ctx)
	var expired atomic.Bool
	stop := c.clock.AfterFunc(limit, func() {
		expired.Store(true)
		cancel()
	})
	return reqCtx, expired.Load, func() {
		stop()
		cancel()
	}
}

// attempt sends one GET and decodes a 200 answer into into. It returns
// ErrStopped when ctx ended.
func (c *Client) attempt(ctx context.Context, call string, query url.Values, into any, limit time.Duration) error {
	if ctx.Err() != nil {
		return ErrStopped
	}
	reqCtx, timedOut, release := c.limited(ctx, limit)
	defer release()

	err := c.get(reqCtx, call, query, into)
	switch {
	case ctx.Err() != nil:
		return ErrStopped
	case timedOut():
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
	_, retried, err := c.timedRead(ctx, call, query, into)
	return retried, err
}

// answeredRead is when the attempt that succeeded was sent and when its
// answer was complete, on the client's clock.
type answeredRead struct{ sent, answered time.Time }

// timedRead is read, also telling when the attempt that succeeded was sent
// and answered.
func (c *Client) timedRead(ctx context.Context, call string, query url.Values, into func() any) (answeredRead, Retried, error) {
	wait := firstRetryWait
	retried := Retried(false)
	for {
		sent := c.clock.Now()
		limit := c.limits.Read
		if !c.failingSince.IsZero() {
			left := c.failingSince.Add(RetryWindow).Sub(sent)
			if left <= 0 {
				return answeredRead{}, retried, &UnreachableError{Address: c.address, Last: c.lastFailure}
			}
			limit = min(limit, left)
		}
		err := c.attempt(ctx, call, query, into(), limit)
		if err == nil {
			c.failingSince, c.lastFailure = time.Time{}, nil
			return answeredRead{sent: sent, answered: c.clock.Now()}, retried, nil
		}
		if errors.Is(err, ErrStopped) {
			return answeredRead{}, retried, ErrStopped
		}
		if c.failingSince.IsZero() {
			c.failingSince = sent
		}
		c.lastFailure = fmt.Errorf("%s: %w", call, err)
		retried = true
		left := c.failingSince.Add(RetryWindow).Sub(c.clock.Now())
		if left <= 0 {
			return answeredRead{}, retried, &UnreachableError{Address: c.address, Last: c.lastFailure}
		}
		if c.clock.Sleep(ctx, min(wait, left)) != nil {
			return answeredRead{}, retried, ErrStopped
		}
		wait = min(wait*2, maxRetryWait)
	}
}
