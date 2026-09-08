/*
Package ratelimit bounds how often one caller may repeat an operation.

The limiter is a token bucket per key: a caller starts with a burst of tokens
and regains them at a steady rate. A bucket exists only for a key that has
actually spent a token, so a well-behaved caller costs nothing to track.

State is held in this process. Convia running as several instances therefore
enforces the limit per instance rather than across the fleet, which is the
right trade while Convia runs as one: a shared limiter would mean a network
round trip on a path whose whole purpose is to be cheaper than the work it
guards. Moving it to Redis is the answer when Convia is actually replicated.
*/
package ratelimit

import (
	"sync"
	"time"
)

/*
Limiter grants and refills a budget for each key.

It is safe for concurrent use. Every method takes the same lock, which is
adequate because the work under it is a few arithmetic operations: the lock is
never held across anything that can block.
*/
type Limiter struct {
	mutex   sync.Mutex
	buckets map[string]*bucket

	// burst is the number of tokens a key holds when it is untouched.
	burst float64
	// refill is how many tokens a key regains per second.
	refill float64
	/*
		maxKeys bounds memory. Buckets are only created for keys that spend a
		token, and a full bucket is indistinguishable from an absent one, so
		sweeping full buckets reclaims everything that is no longer interesting.
	*/
	maxKeys int

	// now is injected so that tests can advance time instead of waiting.
	now func() time.Time
}

// bucket is one key's remaining budget and when it was last computed.
type bucket struct {
	tokens float64
	last   time.Time
}

/*
New builds a limiter granting burst uses, fully refilled over period.

A key that spends its whole burst waits period before holding a full budget
again, and regains one token every period/burst in the meantime.
*/
func New(burst int, period time.Duration, maxKeys int) *Limiter {
	return &Limiter{
		buckets: make(map[string]*bucket),
		burst:   float64(burst),
		refill:  float64(burst) / period.Seconds(),
		maxKeys: maxKeys,
		now:     time.Now,
	}
}

/*
Allows reports whether a key still has budget, without spending any.

An unknown key always has budget, because a bucket is only created when one is
spent. Callers ask this before doing the work the limit protects, so that work
is skipped rather than merely counted.
*/
func (limiter *Limiter) Allows(key string) bool {
	limiter.mutex.Lock()
	defer limiter.mutex.Unlock()

	held, found := limiter.buckets[key]
	if !found {
		return true
	}
	return limiter.replenish(held) >= 1
}

/*
Record spends one token for a key.

It is called after the guarded operation has already failed, so that a caller
doing legitimate work is never charged. Spending below zero is not possible: a
caller that is already out of budget cannot be driven further into debt and so
cannot be locked out for longer by repeating itself.
*/
func (limiter *Limiter) Record(key string) {
	limiter.mutex.Lock()
	defer limiter.mutex.Unlock()

	held, found := limiter.buckets[key]
	if !found {
		if !limiter.room() {
			/*
				Every tracked key is currently spending its budget, which means
				the limiter is already refusing more callers than it has room
				to remember. The request is allowed through rather than refused
				on the strength of someone else's behavior: refusing here would
				turn a memory bound into a way to lock out everyone at once.
			*/
			return
		}

		held = &bucket{tokens: limiter.burst, last: limiter.now()}
		limiter.buckets[key] = held
	}

	tokens := limiter.replenish(held)
	if tokens >= 1 {
		held.tokens = tokens - 1
		return
	}
	held.tokens = 0
}

/*
RetryAfter reports how long a key must wait to hold one token again.

It is zero when the key has budget now, which is what a caller returns when it
has nothing to make a client wait for.
*/
func (limiter *Limiter) RetryAfter(key string) time.Duration {
	limiter.mutex.Lock()
	defer limiter.mutex.Unlock()

	held, found := limiter.buckets[key]
	if !found {
		return 0
	}

	tokens := limiter.replenish(held)
	if tokens >= 1 {
		return 0
	}
	return time.Duration((1 - tokens) / limiter.refill * float64(time.Second))
}

/*
replenish brings a bucket up to date and returns its balance.

The balance is computed from elapsed time rather than by a background timer, so
the limiter has no goroutine to own, stop, or leak, and a key that is never
asked about costs nothing.
*/
func (limiter *Limiter) replenish(held *bucket) float64 {
	moment := limiter.now()
	elapsed := moment.Sub(held.last).Seconds()

	if elapsed > 0 {
		held.tokens = min(held.tokens+elapsed*limiter.refill, limiter.burst)
		held.last = moment
	}
	return held.tokens
}

/*
room makes space for a new key, sweeping keys that have recovered.

A full bucket grants exactly what an absent one grants, so forgetting it
changes no decision. Sweeping only when the map is full keeps the cost off the
common path.
*/
func (limiter *Limiter) room() bool {
	if len(limiter.buckets) < limiter.maxKeys {
		return true
	}

	for key, held := range limiter.buckets {
		if limiter.replenish(held) >= limiter.burst {
			delete(limiter.buckets, key)
		}
	}
	return len(limiter.buckets) < limiter.maxKeys
}

// Tracked reports how many keys currently hold a bucket.
func (limiter *Limiter) Tracked() int {
	limiter.mutex.Lock()
	defer limiter.mutex.Unlock()

	return len(limiter.buckets)
}
