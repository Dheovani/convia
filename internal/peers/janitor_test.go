package peers

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"
)

// sweeping records what a janitor asked of the store, and can refuse.
type sweeping struct {
	mutex  sync.Mutex
	passes []time.Time
	err    error
}

func (store *sweeping) PruneNonces(_ context.Context, before time.Time) (int, error) {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	store.passes = append(store.passes, before)
	return 1, store.err
}

func (store *sweeping) swept() int {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	return len(store.passes)
}

func janitor(store sweeps) *Janitor {
	sweeper := NewJanitor(store, slog.New(slog.NewTextHandler(io.Discard, nil)))
	sweeper.every = 5 * time.Millisecond
	return sweeper
}

/*
TestNoncesAreForgottenOnASchedule is `M33-006`.

They used to be forgotten by whichever instance happened to be verifying a
signature, at most once a minute — so an installation that stopped receiving
requests stopped forgetting, and kept whatever a burst had left behind for ever.
A schedule is a schedule whether or not anybody is asking.
*/
func TestNoncesAreForgottenOnASchedule(t *testing.T) {
	store := &sweeping{}
	ctx, stop := context.WithCancel(context.Background())
	defer stop()

	go janitor(store).Run(ctx)

	deadline := time.After(3 * time.Second)
	for store.swept() < 3 {
		select {
		case <-deadline:
			t.Fatalf("swept %d times, want it to keep going without being asked", store.swept())
		case <-time.After(2 * time.Millisecond):
		}
	}
}

/*
TestAFailedSweepIsTriedAgain: the rows are still expired and still deletable, so
there is nothing to retry and nothing to give up on. What a failed pass costs is
a minute.
*/
func TestAFailedSweepIsTriedAgain(t *testing.T) {
	store := &sweeping{err: errors.New("the store is briefly unreachable")}
	ctx, stop := context.WithCancel(context.Background())
	defer stop()

	go janitor(store).Run(ctx)

	deadline := time.After(3 * time.Second)
	for store.swept() < 2 {
		select {
		case <-deadline:
			t.Fatal("a janitor that met a failure stopped sweeping")
		case <-time.After(2 * time.Millisecond):
		}
	}
}

// TestSweepingStopsWithTheProcess: the owner is the composition root and the
// lifetime is the process, so cancelling is how it stops.
func TestSweepingStopsWithTheProcess(t *testing.T) {
	store := &sweeping{}
	ctx, stop := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		defer close(done)
		janitor(store).Run(ctx)
	}()

	stop()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("the janitor kept running after its context was cancelled")
	}
}
