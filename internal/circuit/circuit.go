/*
Package circuit stops asking a dependency that is not answering.

It is `M25-008`'s finding. Redis paused for thirty seconds in the middle of the
hosted service's load -- what a failover or a partition looks like from the
client's side -- and every request a person made waited behind it, up to
sixteen seconds at p95, though nothing a person asks for but their presence
lives there. Each request checked the sign-in budget in Redis first, waited out
the timeout, and only then fell back to counting on this instance, which is
what it would have done in the first place had it known.

A [Breaker] is that knowledge. One failure opens it; while it is open, callers
are told not to ask and take whatever fallback they already had. After a
cooldown, one caller is let through to find out whether the dependency is back,
and its answer closes the breaker or opens it for another cooldown. A
dependency that is down costs one timeout per cooldown, rather than one per
request.

What a caller does when told not to ask is its own decision, and it is the same
decision it already made for a failure: a breaker changes when a fallback
happens, never what the fallback is.
*/
package circuit

import (
	"context"
	"errors"
	"sync/atomic"
	"time"
)

// Breaker remembers that a dependency failed, for a cooldown. Its zero value is not usable; see New.
type Breaker struct {
	cooldown time.Duration
	now      func() time.Time

	// openUntil is when the cooldown ends, in Unix nanoseconds; zero is closed.
	openUntil atomic.Int64
	// trying is when the one caller let through after a cooldown was, or zero.
	trying atomic.Int64
}

// New is a breaker that stays open for cooldown after a failure.
func New(cooldown time.Duration) *Breaker {
	return &Breaker{cooldown: cooldown, now: time.Now}
}

/*
Allow reports whether to call the dependency.

While open it says no, except to one caller once the cooldown is over. A caller
let through that never reports -- it panicked, or forgot -- does not hold the
breaker open forever: after another cooldown, the next caller is let through.
*/
func (breaker *Breaker) Allow() bool {
	until := breaker.openUntil.Load()
	if until == 0 {
		return true
	}
	now := breaker.now().UnixNano()
	if now < until {
		return false
	}
	trying := breaker.trying.Load()
	if trying != 0 && now-trying < int64(breaker.cooldown) {
		return false
	}
	return breaker.trying.CompareAndSwap(trying, now)
}

/*
Report records how a call went: an error opens the breaker, and anything else
closes it. A call ended because its own caller went away says nothing about the
dependency, and changes nothing.
*/
func (breaker *Breaker) Report(err error) {
	switch {
	case err == nil:
		breaker.openUntil.Store(0)
		breaker.trying.Store(0)
	case errors.Is(err, context.Canceled):
	default:
		breaker.openUntil.Store(breaker.now().Add(breaker.cooldown).UnixNano())
		breaker.trying.Store(0)
	}
}

// Open reports whether the breaker is refusing callers, for whoever measures it.
func (breaker *Breaker) Open() bool { return breaker.openUntil.Load() != 0 }
