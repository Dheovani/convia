package circuit

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

func clocked(cooldown time.Duration) (*Breaker, *time.Time) {
	breaker := New(cooldown)
	at := time.Unix(1_000_000, 0)
	breaker.now = func() time.Time { return at }
	return breaker, &at
}

func TestAFailureStopsTheAskingForACooldown(t *testing.T) {
	breaker, at := clocked(time.Second)

	if !breaker.Allow() {
		t.Fatal("a closed breaker refused a caller")
	}
	breaker.Report(errors.New("i/o timeout"))

	if breaker.Allow() {
		t.Error("a caller was let through straight after a failure")
	}
	*at = at.Add(999 * time.Millisecond)
	if breaker.Allow() {
		t.Error("a caller was let through before the cooldown ended")
	}
}

func TestOneCallerFindsOutWhetherItIsBack(t *testing.T) {
	breaker, at := clocked(time.Second)
	breaker.Report(errors.New("i/o timeout"))
	*at = at.Add(time.Second)

	var allowed atomic.Int64
	var group sync.WaitGroup
	for range 100 {
		group.Add(1)
		go func() {
			defer group.Done()
			if breaker.Allow() {
				allowed.Add(1)
			}
		}()
	}
	group.Wait()
	if allowed.Load() != 1 {
		t.Fatalf("%d callers were let through after the cooldown, want one", allowed.Load())
	}

	breaker.Report(nil)
	for range 2 {
		if !breaker.Allow() {
			t.Error("a success did not close the breaker")
		}
	}
	if breaker.Open() {
		t.Error("a closed breaker reports itself open")
	}
}

func TestATrialThatFailsWaitsAnotherCooldown(t *testing.T) {
	breaker, at := clocked(time.Second)
	breaker.Report(errors.New("i/o timeout"))
	*at = at.Add(time.Second)
	if !breaker.Allow() {
		t.Fatal("nobody was let through after the cooldown")
	}
	breaker.Report(fmt.Errorf("again: %w", errors.New("i/o timeout")))

	*at = at.Add(500 * time.Millisecond)
	if breaker.Allow() {
		t.Error("a caller was let through inside the second cooldown")
	}
	*at = at.Add(500 * time.Millisecond)
	if !breaker.Allow() {
		t.Error("nobody was let through after the second cooldown")
	}
}

func TestATrialThatNeverReportsDoesNotHoldItOpen(t *testing.T) {
	breaker, at := clocked(time.Second)
	breaker.Report(errors.New("i/o timeout"))
	*at = at.Add(time.Second)
	if !breaker.Allow() {
		t.Fatal("nobody was let through after the cooldown")
	}

	*at = at.Add(999 * time.Millisecond)
	if breaker.Allow() {
		t.Error("a second caller was let through while the first was still trying")
	}
	*at = at.Add(time.Millisecond)
	if !breaker.Allow() {
		t.Error("a caller that never reported held the breaker open")
	}
}

func TestACallerThatLeftSaysNothingAboutTheDependency(t *testing.T) {
	breaker, _ := clocked(time.Second)
	breaker.Report(fmt.Errorf("read: %w", context.Canceled))
	if breaker.Open() || !breaker.Allow() {
		t.Error("a caller going away opened the breaker")
	}

	breaker.Report(context.DeadlineExceeded)
	if !breaker.Open() {
		t.Error("a dependency that did not answer in time did not open the breaker")
	}
}

// TestAnErrorRedisAnsweredIsAnAnswer keeps NOSCRIPT, which go-redis answers by sending the script, from opening a breaker.
func TestAnErrorRedisAnsweredIsAnAnswer(t *testing.T) {
	if Unanswered(goredis.Nil) != nil {
		t.Error("a missing key opened the breaker")
	}
	if Unanswered(fmt.Errorf("run: %w", reply("NOSCRIPT No matching script"))) != nil {
		t.Error("a reply of NOSCRIPT opened the breaker")
	}
	silence := errors.New("i/o timeout")
	if !errors.Is(Unanswered(silence), silence) {
		t.Error("a Redis that did not answer did not open the breaker")
	}
}

// reply is an error Redis sent back, as go-redis reports one.
type reply string

func (answer reply) Error() string { return string(answer) }
func (reply) RedisError()          {}
