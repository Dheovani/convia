package peers

import (
	"context"
	"log/slog"
	"time"
)

const (
	/*
		sweepInterval is how often an instance forgets nonces that have expired.

		A nonce is kept only long enough that a replay of the request it came
		with would be refused for being too old anyway — twice the clock skew
		Convia allows — so nothing here changes an answer. What it changes is
		whether the table grows without bound.

		A minute is short against how long a nonce is kept and long enough that
		several instances sweeping the same table are not spending a round trip
		each on finding nothing.
	*/
	sweepInterval = time.Minute

	// sweepTimeout bounds one pass against the store, so a slow store delays
	// the next pass rather than stopping the janitor.
	sweepTimeout = 30 * time.Second
)

/*
sweeps is the behavior a janitor needs, which is one operation.

It is an interface so the janitor can be tested without a store, and so the
store stays the only thing that knows how a nonce is written down.
*/
type sweeps interface {
	PruneNonces(ctx context.Context, before time.Time) (int, error)
}

/*
Janitor forgets the nonces that no longer refuse anything.

**Every signed request writes one down**, because that is what makes a replay a
replay: a request whose nonce this installation has already seen is refused. A
nonce stops being useful once the timestamp it came with would be rejected for
being too old, and from then on it is a row nobody will ever read.

This used to happen inside verification, by whichever instance happened to be
checking a signature, at most once a minute. Two things were wrong with it. An
installation that stops receiving requests stops sweeping, so a burst is kept
for ever; and an ordinary request paid for a delete it had no reason to.

Every instance runs one. They do not coordinate and do not need to: deleting a
row that another instance has already deleted finds nothing, which is the right
amount of work to do about it.
*/
type Janitor struct {
	store  sweeps
	logger *slog.Logger

	// every is a field rather than the constant directly, so a test can sweep
	// without waiting a minute for it.
	every time.Duration
}

func NewJanitor(store sweeps, logger *slog.Logger) *Janitor {
	return &Janitor{store: store, logger: logger, every: sweepInterval}
}

/*
Run sweeps until the context is cancelled.

Its owner is the composition root and its lifetime is the process. Stopping is
cancelling: a pass that does not happen costs some rows, not a fact, and the
next instance to start will take them.
*/
func (janitor *Janitor) Run(ctx context.Context) {
	ticker := time.NewTicker(janitor.every)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			janitor.pass(ctx)
		}
	}
}

/*
pass forgets one round of expired nonces.

A failure is logged and the next pass tries again. There is nothing to retry:
the rows are still expired, they are still deletable, and what was lost is a
minute.
*/
func (janitor *Janitor) pass(ctx context.Context) {
	bounded, cancel := context.WithTimeout(ctx, sweepTimeout)
	defer cancel()

	forgotten, err := janitor.store.PruneNonces(bounded, time.Now().UTC())
	if err != nil {
		if ctx.Err() != nil {
			return // The process is shutting down, which is not a fault.
		}
		janitor.logger.Warn("expired nonces could not be forgotten", "error", err)
		return
	}
	if forgotten > 0 {
		janitor.logger.Debug("expired nonces forgotten", "count", forgotten)
	}
}
