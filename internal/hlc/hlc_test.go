package hlc

import (
	"math/rand/v2"
	"testing"
	"time"
)

func TestTheClockNeverRunsBackwards(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	physical := time.UnixMilli(1_000_000)
	c := New(Time{}, func() time.Time { return physical })
	previous := Time{}
	for range 10_000 {
		switch r.IntN(4) {
		case 0:
			physical = physical.Add(time.Duration(r.IntN(5)) * time.Millisecond)
		case 1:
			// A clock set back, as after correcting it.
			physical = physical.Add(-time.Duration(r.IntN(1000)) * time.Millisecond)
		case 2:
			c.Observe(Time{Wall: physical.UnixMilli() + int64(r.IntN(2000)) - 1000, Counter: uint32(r.IntN(3))})
		}
		now := c.Now()
		if !previous.Before(now) {
			t.Fatalf("%v came after %v", now, previous)
		}
		previous = now
	}
}

func TestTheClockFollowsRealTimeWhenItCan(t *testing.T) {
	physical := time.UnixMilli(5000)
	c := New(Time{Wall: 1000, Counter: 7}, func() time.Time { return physical })
	if got := c.Now(); got != (Time{Wall: 5000}) {
		t.Fatalf("Now = %v", got)
	}
	if got := c.Now(); got != (Time{Wall: 5000, Counter: 1}) {
		t.Fatalf("a second moment in the same millisecond = %v", got)
	}
	c.Observe(Time{Wall: 9000, Counter: 2})
	if got := c.Now(); got != (Time{Wall: 9000, Counter: 3}) {
		t.Fatalf("after a moment from the future = %v", got)
	}
	c.Observe(Time{Wall: 10})
	if got := c.Now(); got != (Time{Wall: 9000, Counter: 4}) {
		t.Fatalf("an old moment moved the clock: %v", got)
	}
}
