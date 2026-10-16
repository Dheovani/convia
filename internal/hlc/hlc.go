// Package hlc is the hybrid logical clock that orders a conversation (ADR 0010): it follows real time where the clocks allow it, and never runs backwards.
package hlc

import (
	"sync"
	"time"
)

// Time is a moment of the clock: milliseconds since 1970, and a counter for moments within one.
type Time struct {
	Wall    int64
	Counter uint32
}

// Before orders two moments.
func (t Time) Before(u Time) bool {
	return t.Wall < u.Wall || t.Wall == u.Wall && t.Counter < u.Counter
}

// Clock hands out moments, each after every one it handed out or observed.
type Clock struct {
	mu   sync.Mutex
	last Time
	now  func() time.Time
}

// New is a clock that has already seen last, as when a peer starts again with its history.
func New(last Time, now func() time.Time) *Clock {
	return &Clock{last: last, now: now}
}

// Now is a moment for something happening here.
func (c *Clock) Now() Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	physical := c.now().UnixMilli()
	if physical > c.last.Wall {
		c.last = Time{Wall: physical}
	} else {
		c.last.Counter++
	}
	return c.last
}

// Observe moves the clock past remote, a moment from another peer.
func (c *Clock) Observe(remote Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.last.Before(remote) {
		c.last = remote
	}
}
