// Package fakeclock is a clock for tests: time stands still until the test
// advances it, so tests of time limits and retry windows never wait.
package fakeclock

import (
	"context"
	"sort"
	"sync"
	"testing"
	"time"
)

// guard bounds how long a test waits for the code under test to reach a
// state; reaching it never depends on real time, so the guard only turns a
// hang into a failure.
const guard = 10 * time.Second

// Clock is a fake time. Its zero value is not usable; call New.
type Clock struct {
	t       testing.TB
	mu      sync.Mutex
	now     time.Time
	timers  []*timer
	changed chan struct{}
}

type timer struct {
	when  time.Time
	fire  func()
	sleep bool
}

// New returns a clock standing at a fixed instant.
func New(t testing.TB) *Clock {
	return &Clock{t: t, now: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC), changed: make(chan struct{})}
}

// Now returns the clock's time.
func (c *Clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *Clock) add(tm *timer) {
	c.timers = append(c.timers, tm)
	c.notify()
}

func (c *Clock) remove(tm *timer) bool {
	for i, x := range c.timers {
		if x == tm {
			c.timers = append(c.timers[:i], c.timers[i+1:]...)
			c.notify()
			return true
		}
	}
	return false
}

// notify wakes every waiter; the caller holds mu.
func (c *Clock) notify() {
	close(c.changed)
	c.changed = make(chan struct{})
}

// AfterFunc calls f when the clock is advanced past d from now.
func (c *Clock) AfterFunc(d time.Duration, f func()) func() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	tm := &timer{when: c.now.Add(d), fire: f}
	c.add(tm)
	return func() bool {
		c.mu.Lock()
		defer c.mu.Unlock()
		return c.remove(tm)
	}
}

// Sleep returns once the clock is advanced past d from now, or when ctx ends.
func (c *Clock) Sleep(ctx context.Context, d time.Duration) error {
	woken := make(chan struct{})
	c.mu.Lock()
	tm := &timer{when: c.now.Add(d), fire: func() { close(woken) }, sleep: true}
	c.add(tm)
	c.mu.Unlock()
	select {
	case <-woken:
		return nil
	case <-ctx.Done():
		c.mu.Lock()
		c.remove(tm)
		c.mu.Unlock()
		return ctx.Err()
	}
}

// Advance moves the clock forward by d, firing in order every timer due.
func (c *Clock) Advance(d time.Duration) {
	c.mu.Lock()
	end := c.now.Add(d)
	for {
		sort.SliceStable(c.timers, func(i, j int) bool { return c.timers[i].when.Before(c.timers[j].when) })
		if len(c.timers) == 0 || c.timers[0].when.After(end) {
			break
		}
		tm := c.timers[0]
		c.timers = c.timers[1:]
		if tm.when.After(c.now) {
			c.now = tm.when
		}
		c.notify()
		c.mu.Unlock()
		tm.fire()
		c.mu.Lock()
	}
	c.now = end
	c.notify()
	c.mu.Unlock()
}

// WaitSleep blocks until the code under test sleeps, and returns how long
// it sleeps for.
func (c *Clock) WaitSleep() time.Duration {
	c.t.Helper()
	return c.wait("a sleep", func() (time.Duration, bool) {
		for _, tm := range c.timers {
			if tm.sleep {
				return tm.when.Sub(c.now), true
			}
		}
		return 0, false
	})
}

// WaitTimer blocks until a timer that is not a sleep is pending, such as a
// request's time limit, and returns how long until it fires.
func (c *Clock) WaitTimer() time.Duration {
	c.t.Helper()
	return c.wait("a timer", func() (time.Duration, bool) {
		for _, tm := range c.timers {
			if !tm.sleep {
				return tm.when.Sub(c.now), true
			}
		}
		return 0, false
	})
}

func (c *Clock) wait(what string, found func() (time.Duration, bool)) time.Duration {
	c.t.Helper()
	deadline := time.NewTimer(guard)
	defer deadline.Stop()
	for {
		c.mu.Lock()
		d, ok := found()
		ch := c.changed
		c.mu.Unlock()
		if ok {
			return d
		}
		select {
		case <-ch:
		case <-deadline.C:
			c.t.Fatalf("fakeclock: nothing waited on %s within %s", what, guard)
		}
	}
}
