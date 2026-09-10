package presence

import (
	"context"
	"log/slog"
	"time"
)

const (
	/*
		sweepInterval is how often an instance looks for presence that lapsed.

		It bounds how late an application learns somebody went quiet, and
		nothing else: a read is already correct without it. Five seconds is
		short against the shortest lifetime Convia allows, so an expiry is
		announced well inside the window in which it still matters, and long
		enough that a deployment of several instances is not spending a round
		trip a second each on finding nothing.
	*/
	sweepInterval = 5 * time.Second

	/*
		sweepBatch bounds one pass.

		A pass that tried to drain everything after an outage would hold the
		store for as long as that took and then publish the whole backlog at
		once, which is how a recovery becomes a second incident. Whatever is
		left is taken by the next pass, five seconds later.
	*/
	sweepBatch = 500

	// sweepTimeout bounds one pass against the store, so a slow store delays
	// the next pass rather than stopping the sweeper.
	sweepTimeout = 10 * time.Second
)

/*
sweeps is the behavior a sweeper needs, which is one operation.

It is an interface so the sweeper can be tested without a store, and so the
service stays the only thing that knows how a lapse becomes an event.
*/
type sweeps interface {
	Sweep(ctx context.Context, limit int) (int, error)
}

/*
Sweeper turns expiry into an event.

Presence lapses on a timer, and a timer tells nobody. Every read already
ignores a claim past its deadline, so this changes no answer Convia gives; what
it changes is whether a subscriber watching a roster ever sees somebody leave
it. Without a sweeper, arrival would stream and departure would not, which is
the worse of the two halves to lose.

Every instance runs one. They do not coordinate and do not need to: claiming an
expired entry is exclusive, so a person who lapsed is announced once and the
instances that lost the race find nothing to do.
*/
type Sweeper struct {
	service sweeps
	logger  *slog.Logger

	// interval and batch are fields rather than the constants directly so a
	// test can sweep without waiting five seconds for it.
	interval time.Duration
	batch    int
}

func NewSweeper(service sweeps, logger *slog.Logger) *Sweeper {
	return &Sweeper{service: service, logger: logger, interval: sweepInterval, batch: sweepBatch}
}

/*
Run sweeps until the context is cancelled.

Its owner is the composition root and its lifetime is the process. Stopping is
cancelling: there is nothing in flight worth waiting for, because a pass that
does not happen costs an announcement rather than a fact.
*/
func (sweeper *Sweeper) Run(ctx context.Context) {
	ticker := time.NewTicker(sweeper.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sweeper.pass(ctx)
		}
	}
}

/*
pass announces one batch of lapsed presence.

A failure is logged and the next pass tries again. There is nothing to retry
here: the entries are still expired, they are still claimable, and the only
thing lost is five seconds of promptness.
*/
func (sweeper *Sweeper) pass(ctx context.Context) {
	bounded, cancel := context.WithTimeout(ctx, sweepTimeout)
	defer cancel()

	announced, err := sweeper.service.Sweep(bounded, sweeper.batch)
	if err != nil {
		if ctx.Err() != nil {
			return // The process is shutting down, which is not a fault.
		}
		sweeper.logger.Warn("presence that lapsed could not be announced", "error", err)
		return
	}

	if announced > 0 {
		sweeper.logger.Debug("presence lapsed", "people", announced)
	}
}
