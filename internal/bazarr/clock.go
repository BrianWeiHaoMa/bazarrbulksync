package bazarr

import (
	"context"
	"time"
)

// Clock is the time the client measures its request limits and G3's five
// minutes against. Tests pass a clock they advance themselves.
type Clock interface {
	Now() time.Time
	// AfterFunc calls f once d has passed, unless the returned stop is called first.
	AfterFunc(d time.Duration, f func()) (stop func() bool)
	// Sleep returns nil once d has passed, or ctx's error if ctx ends first.
	Sleep(ctx context.Context, d time.Duration) error
}

// SystemClock is the real time.
type SystemClock struct{}

// Now returns the current time.
func (SystemClock) Now() time.Time { return time.Now() }

// AfterFunc calls f in its own goroutine once d has passed.
func (SystemClock) AfterFunc(d time.Duration, f func()) func() bool {
	return time.AfterFunc(d, f).Stop
}

// Sleep waits for d or for ctx to end.
func (SystemClock) Sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
