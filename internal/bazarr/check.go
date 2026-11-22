package bazarr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// CheckFailure is the kind of cause a failed connection check names (G2).
type CheckFailure int

const (
	// MissingAddress: no address was given.
	MissingAddress CheckFailure = iota + 1
	// MissingAPIKey: no API key was given.
	MissingAPIKey
	// UnusableAddress: the address is not an http:// or https:// URL.
	UnusableAddress
	// NoAnswer: the server did not answer within the limit, or could not be reached.
	NoAnswer
	// RejectedKey: Bazarr answered HTTP 401.
	RejectedKey
	// NotBazarr: the answer is not Bazarr's status.
	NotBazarr
	// UnsupportedVersion: Bazarr reports no version of 1.6.0 or newer.
	UnsupportedVersion
)

// CheckError is the cause of a failed connection check.
type CheckError struct {
	Failure CheckFailure
	// Address is the address as given; empty for MissingAddress.
	Address string
	// Version is the version Bazarr reported, for UnsupportedVersion.
	Version string
	// Err is the underlying failure, when there is one.
	Err error
}

func (e *CheckError) Error() string {
	switch e.Failure {
	case MissingAddress:
		return "no Bazarr address given: set BAZARR_URL or pass --url"
	case MissingAPIKey:
		return fmt.Sprintf("no API key given for Bazarr at %s: set BAZARR_API_KEY or pass --api-key", e.Address)
	case UnusableAddress:
		return fmt.Sprintf("the Bazarr address %q cannot be used: it must be an http:// or https:// address without a query, such as http://host:6767", redactQuery(e.Address))
	case NoAnswer:
		return fmt.Sprintf("Bazarr at %s does not answer: %v", e.Address, e.Err)
	case RejectedKey:
		return fmt.Sprintf("Bazarr at %s rejected the API key", e.Address)
	case NotBazarr:
		return fmt.Sprintf("the server at %s does not answer as Bazarr does (%v); check the address and its base path", e.Address, e.Err)
	case UnsupportedVersion:
		return fmt.Sprintf("Bazarr at %s reports version %q; bazarrbulksync needs Bazarr 1.6.0 or newer", e.Address, e.Version)
	}
	return fmt.Sprintf("connection check of Bazarr at %s failed: %v", e.Address, e.Err)
}

func (e *CheckError) Unwrap() error { return e.Err }

// redactQuery drops what follows a "?" or "#" in an address that cannot be
// used, since a pasted Bazarr link can carry the key as `apikey` (R2).
func redactQuery(address string) string {
	if i := strings.IndexAny(address, "?#"); i >= 0 {
		return address[:i] + address[i:i+1] + "…"
	}
	return address
}

// statusAnswer is the answer of GET system/status.
type statusAnswer struct {
	Data *struct {
		BazarrVersion *string      `json:"bazarr_version"`
		StartTime     *json.Number `json:"start_time"`
	} `json:"data"`
}

func (a *statusAnswer) complete() bool {
	return a.Data != nil && a.Data.BazarrVersion != nil && a.Data.StartTime != nil
}

// Connect performs the connection check of G2: one read of Bazarr's status,
// not retried. Only a successful check yields a Client, so nothing can read
// from Bazarr without one. A failed check returns a *CheckError, or
// ErrStopped when ctx ended.
func Connect(ctx context.Context, conn Connection) (*Client, error) {
	if conn.URL == "" {
		return nil, &CheckError{Failure: MissingAddress}
	}
	base, ok := parseAddress(conn.URL)
	if !ok {
		return nil, &CheckError{Failure: UnusableAddress, Address: conn.URL}
	}
	if conn.APIKey == "" {
		return nil, &CheckError{Failure: MissingAPIKey, Address: conn.URL}
	}
	c := newClient(conn, base)
	var answer statusAnswer
	err := c.attempt(ctx, "system/status", nil, &answer, c.limits.Read)
	if err != nil {
		return nil, checkFailure(conn.URL, err)
	}
	version := *answer.Data.BazarrVersion
	if !versionSupported(version) {
		return nil, &CheckError{Failure: UnsupportedVersion, Address: conn.URL, Version: version}
	}
	c.status = Status{Version: version, StartTime: answer.Data.StartTime.String()}
	return c, nil
}

func checkFailure(address string, err error) error {
	if errors.Is(err, ErrStopped) {
		return ErrStopped
	}
	var status *statusError
	switch {
	case errors.As(err, &status) && status.code == http.StatusUnauthorized:
		return &CheckError{Failure: RejectedKey, Address: address}
	case errors.As(err, &status) && (status.code == http.StatusBadGateway ||
		status.code == http.StatusServiceUnavailable || status.code == http.StatusGatewayTimeout):
		// A proxy sends these when Bazarr behind it did not answer.
		return &CheckError{Failure: NoAnswer, Address: address, Err: err}
	case errors.As(err, &status) || errors.Is(err, errUndecodable):
		return &CheckError{Failure: NotBazarr, Address: address, Err: err}
	}
	return &CheckError{Failure: NoAnswer, Address: address, Err: err}
}

// parseAddress accepts an absolute http:// or https:// URL with a host and
// no query or fragment, which the calls' URLs could not keep.
func parseAddress(address string) (*url.URL, bool) {
	u, err := url.Parse(address)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" ||
		u.Opaque != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return nil, false
	}
	return u, true
}

// versionSupported reports whether version is 1.6.0 or newer, compared as
// numeric major, minor, and patch. A pre-release of 1.6.0 is older than
// 1.6.0; a version that does not parse as major.minor.patch with optional
// pre-release and build parts is not supported.
func versionSupported(version string) bool {
	version, build, hasBuild := strings.Cut(version, "+")
	core, pre, isPre := strings.Cut(version, "-")
	if (hasBuild && !validIdentifiers(build)) || (isPre && !validIdentifiers(pre)) {
		return false
	}
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return false
	}
	var n [3]int
	for i, p := range parts {
		v, err := strconv.Atoi(p)
		if err != nil || p != strconv.Itoa(v) {
			return false
		}
		n[i] = v
	}
	switch {
	case n[0] != 1:
		return n[0] > 1
	case n[1] != 6:
		return n[1] > 6
	case n[2] == 0:
		return !isPre
	}
	return true
}

// validIdentifiers reports whether s is dot-separated non-empty runs of
// ASCII letters, digits, and hyphens, as semantic versioning's pre-release
// and build parts are.
func validIdentifiers(s string) bool {
	for _, id := range strings.Split(s, ".") {
		if id == "" {
			return false
		}
		for _, r := range id {
			if !(r == '-' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z') {
				return false
			}
		}
	}
	return true
}

// StartTime reads Bazarr's start time, a read under G3, as Bazarr's own
// text, which compares with Status().StartTime and with any other read.
func (c *Client) StartTime(ctx context.Context) (string, Retried, error) {
	var answer *statusAnswer
	retried, err := c.read(ctx, "system/status", nil, func() any { answer = &statusAnswer{}; return answer })
	if err != nil {
		return "", retried, err
	}
	return answer.Data.StartTime.String(), retried, nil
}

// Restarted reports whether Bazarr has restarted between two reads of its
// start time: the start time is set once when Bazarr starts (S3).
func Restarted(earlier, now string) bool { return earlier != now }
