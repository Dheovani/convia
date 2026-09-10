package presence

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

/*
counted is a service that records how often it was swept, and can be told to
fail.

The sweeper's whole job is to keep calling, so what a test needs to see is that
it did — and that a failure did not stop it.
*/
type counted struct {
	mutex  sync.Mutex
	passes int
	err    error
	swept  chan struct{}
}

func (service *counted) Sweep(context.Context, int) (int, error) {
	service.mutex.Lock()
	service.passes++
	err := service.err
	service.mutex.Unlock()

	select {
	case service.swept <- struct{}{}:
	default:
	}
	return 0, err
}

func (service *counted) count() int {
	service.mutex.Lock()
	defer service.mutex.Unlock()
	return service.passes
}

// sweeping starts a sweeper on a short interval and stops it when the test
// ends, so that no test waits five seconds for a tick.
func sweeping(t *testing.T, service *counted) {
	t.Helper()

	sweeper := NewSweeper(service, quiet())
	sweeper.interval = 5 * time.Millisecond

	ctx, stop := context.WithCancel(context.Background())
	done := make(chan struct{})

	go func() {
		defer close(done)
		sweeper.Run(ctx)
	}()

	t.Cleanup(func() {
		stop()
		<-done
	})
}

// TestTheSweeperKeepsLooking is the behavior the whole type exists for.
func TestTheSweeperKeepsLooking(t *testing.T) {
	service := &counted{swept: make(chan struct{}, 1)}
	sweeping(t, service)

	for range 3 {
		select {
		case <-service.swept:
		case <-time.After(2 * time.Second):
			t.Fatalf("the sweeper stopped after %d passes", service.count())
		}
	}
}

/*
TestAFailedPassIsNotTheEndOfSweeping is the difference between losing five
seconds and losing every departure until the next restart.

There is nothing to retry when a pass fails: the claims are still expired, they
are still claimable, and the next tick takes them.
*/
func TestAFailedPassIsNotTheEndOfSweeping(t *testing.T) {
	service := &counted{swept: make(chan struct{}, 1), err: errors.New("dial tcp: connection refused")}
	sweeping(t, service)

	for range 3 {
		select {
		case <-service.swept:
		case <-time.After(2 * time.Second):
			t.Fatalf("the sweeper gave up after %d failed passes", service.count())
		}
	}
}

/*
TestCancellingStopsTheSweeper is what makes the composition root its owner.

Stopping has to be prompt and has to leave nothing behind, because a pass that
does not happen costs an announcement rather than an answer — every read
already ignores a claim past its deadline.
*/
func TestCancellingStopsTheSweeper(t *testing.T) {
	service := &counted{swept: make(chan struct{}, 1)}

	sweeper := NewSweeper(service, quiet())
	sweeper.interval = 5 * time.Millisecond

	ctx, stop := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		sweeper.Run(ctx)
	}()

	<-service.swept
	stop()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("the sweeper did not stop when its context was cancelled")
	}

	settled := service.count()
	time.Sleep(50 * time.Millisecond)
	if after := service.count(); after != settled {
		t.Errorf("the sweeper swept %d more times after stopping", after-settled)
	}
}
