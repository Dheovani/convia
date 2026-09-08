package ratelimit

import (
	"strconv"
	"sync"
	"testing"
	"time"
)

/*
clock is a hand-wound time source.

Every test advances it explicitly, so the suite never sleeps and a slow machine
cannot make a timing assertion flap.
*/
type clock struct {
	moment time.Time
}

func (c *clock) advance(by time.Duration) {
	c.moment = c.moment.Add(by)
}

// withClock builds a limiter whose time the test controls.
func withClock(t *testing.T, burst int, period time.Duration, maxKeys int) (*Limiter, *clock) {
	t.Helper()

	c := &clock{moment: time.Date(2026, time.September, 6, 9, 0, 0, 0, time.UTC)}
	limiter := New(burst, period, maxKeys)
	limiter.now = func() time.Time { return c.moment }

	return limiter, c
}

/*
TestUntrackedKeyHasFullBudget proves a caller costs nothing until it spends
something, which is what keeps a well-behaved client out of the map entirely.
*/
func TestUntrackedKeyHasFullBudget(t *testing.T) {
	limiter, _ := withClock(t, 3, time.Minute, 100)

	if !limiter.Allows("192.0.2.1") {
		t.Error("an untouched key was refused")
	}
	if limiter.Tracked() != 0 {
		t.Errorf("tracked %d keys, want none until one is spent", limiter.Tracked())
	}
	if wait := limiter.RetryAfter("192.0.2.1"); wait != 0 {
		t.Errorf("RetryAfter() = %v, want 0", wait)
	}
}

// TestBudgetIsSpentThenExhausted covers the whole burst and the refusal after it.
func TestBudgetIsSpentThenExhausted(t *testing.T) {
	limiter, _ := withClock(t, 3, time.Minute, 100)
	const key = "192.0.2.1"

	for spent := 1; spent <= 3; spent++ {
		if !limiter.Allows(key) {
			t.Fatalf("refused after %d of 3 uses", spent-1)
		}
		limiter.Record(key)
	}

	if limiter.Allows(key) {
		t.Error("the key was allowed after spending its whole burst")
	}
}

/*
TestBudgetRefillsOverTime proves the refusal is temporary and proportional:
part of a period restores part of the budget.
*/
func TestBudgetRefillsOverTime(t *testing.T) {
	limiter, c := withClock(t, 3, time.Minute, 100)
	const key = "192.0.2.1"

	for range 3 {
		limiter.Record(key)
	}
	if limiter.Allows(key) {
		t.Fatal("the budget was not spent")
	}

	// One token every twenty seconds, so nineteen is not yet enough.
	c.advance(19 * time.Second)
	if limiter.Allows(key) {
		t.Error("a token arrived early")
	}

	c.advance(2 * time.Second)
	if !limiter.Allows(key) {
		t.Error("no token after a full refill interval")
	}
}

// TestRefillStopsAtTheBurst proves an idle caller banks a burst and no more.
func TestRefillStopsAtTheBurst(t *testing.T) {
	limiter, c := withClock(t, 3, time.Minute, 100)
	const key = "192.0.2.1"

	limiter.Record(key)
	c.advance(24 * time.Hour)

	for spent := 1; spent <= 3; spent++ {
		if !limiter.Allows(key) {
			t.Fatalf("refused after %d of 3 uses following a long idle period", spent-1)
		}
		limiter.Record(key)
	}
	if limiter.Allows(key) {
		t.Error("a day of idling banked more than one burst")
	}
}

/*
TestSpendingWhileEmptyDoesNotExtendTheWait proves a caller cannot be driven
into debt.

Without this, an attacker hammering a shared address would push the wait ever
further out and lock out whoever else is behind it.
*/
func TestSpendingWhileEmptyDoesNotExtendTheWait(t *testing.T) {
	limiter, c := withClock(t, 2, time.Minute, 100)
	const key = "192.0.2.1"

	limiter.Record(key)
	limiter.Record(key)
	afterExhausting := limiter.RetryAfter(key)

	for range 100 {
		limiter.Record(key)
	}

	if wait := limiter.RetryAfter(key); wait != afterExhausting {
		t.Errorf("RetryAfter() = %v after further attempts, want it unchanged at %v", wait, afterExhausting)
	}

	c.advance(30 * time.Second)
	if !limiter.Allows(key) {
		t.Error("the key never recovered despite the wait elapsing")
	}
}

// TestRetryAfterShrinksAsTheWaitElapses proves the header a client is given
// counts down rather than staying stale.
func TestRetryAfterShrinksAsTheWaitElapses(t *testing.T) {
	limiter, c := withClock(t, 1, time.Minute, 100)
	const key = "192.0.2.1"

	limiter.Record(key)
	first := limiter.RetryAfter(key)
	if first <= 0 {
		t.Fatalf("RetryAfter() = %v, want a positive wait", first)
	}

	c.advance(30 * time.Second)
	second := limiter.RetryAfter(key)

	if second >= first {
		t.Errorf("RetryAfter() = %v after waiting, want less than %v", second, first)
	}
	if second <= 0 {
		t.Errorf("RetryAfter() = %v, want the remaining half of the wait", second)
	}
}

// TestKeysAreForgottenOnceTheyRecover proves memory is reclaimed without a
// background sweep, so the limiter owns no goroutine.
func TestKeysAreForgottenOnceTheyRecover(t *testing.T) {
	limiter, c := withClock(t, 1, time.Minute, 3)

	for index := range 3 {
		limiter.Record("192.0.2." + strconv.Itoa(index))
	}
	if limiter.Tracked() != 3 {
		t.Fatalf("tracked %d keys, want 3", limiter.Tracked())
	}

	// Everything recovers, so the next arrival should find room.
	c.advance(time.Minute)
	limiter.Record("198.51.100.1")

	if limiter.Tracked() != 1 {
		t.Errorf("tracked %d keys, want only the newest once the others recovered", limiter.Tracked())
	}
	if limiter.Allows("198.51.100.1") {
		t.Error("the newest key was not charged, so it never entered the map")
	}
}

/*
TestMemoryIsBoundedUnderPressure proves the map cannot grow past its cap, and
that a caller arriving when it is full is allowed rather than refused.

Refusing there would let anyone able to fill the map lock out every address at
once, which would turn a memory bound into the outage it exists to prevent.
*/
func TestMemoryIsBoundedUnderPressure(t *testing.T) {
	limiter, _ := withClock(t, 1, time.Minute, 4)

	for index := range 50 {
		limiter.Record("192.0.2." + strconv.Itoa(index))
	}

	if tracked := limiter.Tracked(); tracked > 4 {
		t.Errorf("tracked %d keys, want at most the cap of 4", tracked)
	}
	if !limiter.Allows("198.51.100.1") {
		t.Error("an untracked caller was refused because the map was full")
	}
}

// TestConcurrentUseIsSafe is meaningful under the race detector, which CI runs.
func TestConcurrentUseIsSafe(t *testing.T) {
	limiter := New(100, time.Minute, 64)

	var group sync.WaitGroup
	for worker := range 8 {
		group.Add(1)
		go func() {
			defer group.Done()
			key := "192.0.2." + strconv.Itoa(worker%4)

			for range 200 {
				limiter.Allows(key)
				limiter.Record(key)
				limiter.RetryAfter(key)
			}
		}()
	}
	group.Wait()

	if tracked := limiter.Tracked(); tracked > 64 {
		t.Errorf("tracked %d keys, want at most the cap", tracked)
	}
}
