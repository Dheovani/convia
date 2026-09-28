/*
Package erasure forgets the people a deployment has finished deleting.

Deleting a user has always kept the row. `docs/users.md` says so plainly -- the
deletion stays recoverable and the external subject stays taken until erasure
frees it -- and until now nothing ever freed it. There was no age at which
Convia forgot anything, and the job that was supposed to act at the end of the
retention window was a line under "Not Yet Implemented". This is `M23-017`.

It is its own package because erasing somebody spans two domains that must not
depend on each other. What a person wrote belongs to messages, who they were
belongs to users, and neither should import the other to finish the other's
work. This composes them, the way internal/departure composes the same two for
a person who deletes their own account.
*/
package erasure

import (
	"context"
	"log/slog"
	"time"

	"convia/internal/messages"
	"convia/internal/users"
)

const (
	/*
		sweepInterval is how often an instance looks for somebody to forget.

		A retention window is measured in weeks, so the difference between
		sweeping hourly and sweeping by the minute is invisible in what anybody
		is holding -- and an hour is short enough that "it ran" is observable
		within a working day rather than at the end of one.
	*/
	sweepInterval = time.Hour

	/*
		sweepTimeout bounds one pass, so a slow store delays the next pass
		rather than stopping the janitor.

		It is generous compared with the nonce janitor's because a pass here is
		not one delete: it is a redaction across a person's whole history, done
		once per person.
	*/
	sweepTimeout = 5 * time.Minute

	/*
		atOnce bounds one pass.

		An installation that has been deleting users for months without ever
		sweeping has a backlog, and taking it in a single statement would hold
		one transaction open across all of it. A hundred at a time clears any
		plausible backlog within a day of sweeps and never makes the database
		wait on Convia.
	*/
	atOnce = 100
)

/*
people is what the janitor needs from the user store.

An interface rather than the store, so the janitor is testable without a
database and so nothing here has to know how a user is written down.
*/
type people interface {
	Expired(ctx context.Context, before time.Time, limit int) ([]users.Doomed, error)
	Erase(ctx context.Context, applicationID, id string, at time.Time) (bool, error)
}

// conversations is what somebody wrote. messages.Service satisfies it, and it
// is the same operation a person's own account deletion already performs.
type conversations interface {
	Erase(ctx context.Context, applicationID, userID string) (messages.Erasure, error)
}

/*
Janitor erases the people whose retention window has closed.

**The window is a promise in both directions.** It is how long a deletion stays
recoverable -- an application that deleted the wrong person has until then to
say so -- and it is the outer bound on how long Convia keeps somebody it was
told to forget. A window that is never enforced makes the first promise true and
the second a sentence in a document.

Every instance runs one, and they do not coordinate. Two instances reaching the
same person do the same work twice and the second finds nothing left to do,
which is the right amount of coordination for an operation that is idempotent by
construction.
*/
type Janitor struct {
	people        people
	conversations conversations
	logger        *slog.Logger

	// window is how long a deleted user is kept before being erased.
	window time.Duration

	// every is a field rather than the constant directly, so a test can sweep
	// without waiting an hour for it.
	every time.Duration
}

func NewJanitor(people people, conversations conversations, window time.Duration,
	logger *slog.Logger) *Janitor {
	return &Janitor{
		people:        people,
		conversations: conversations,
		logger:        logger,
		window:        window,
		every:         sweepInterval,
	}
}

/*
Run sweeps until the context is cancelled.

Its owner is the composition root and its lifetime is the process. Stopping is
cancelling: a pass that does not happen costs a person another hour of being
remembered, not a fact, and the next instance to start will take them.
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
pass erases one round of people whose window has closed.

A failure is logged and the next pass tries again. There is nothing to retry by
hand: they are still deleted, their window is still closed, and what was lost is
an hour.
*/
func (janitor *Janitor) pass(ctx context.Context) {
	bounded, cancel := context.WithTimeout(ctx, sweepTimeout)
	defer cancel()

	now := time.Now().UTC()
	doomed, err := janitor.people.Expired(bounded, now.Add(-janitor.window), atOnce)
	if err != nil {
		if ctx.Err() != nil {
			return // The process is shutting down, which is not a fault.
		}
		janitor.logger.Warn("people due for erasure could not be listed", "error", err)
		return
	}

	forgotten := 0
	for _, person := range doomed {
		if bounded.Err() != nil {
			break // Out of time. The rest are still due and the next pass takes them.
		}
		if janitor.forget(bounded, person, now) {
			forgotten++
		}
	}

	if forgotten > 0 {
		janitor.logger.Info("people erased at the end of their retention window",
			"count", forgotten, "due", len(doomed))
	}
}

/*
forget erases one person, in the order that survives being interrupted.

**What they wrote goes first, and their identity last.** The subject on the user
row is what puts somebody on this list, so clearing it is what takes them off
it: doing that first and then failing would leave their messages attributed for
ever, with nothing left to notice. Failing the other way round costs a retry an
hour later, which is what a retention window has room for.

Nothing is reported about who was erased beyond the identifiers, and the counts
are not attached to them. A log line naming what was removed would keep, in a
place that is shipped and retained, exactly what the database was just told to
stop holding.
*/
func (janitor *Janitor) forget(ctx context.Context, person users.Doomed, at time.Time) bool {
	if _, err := janitor.conversations.Erase(ctx, person.ApplicationID, person.ID); err != nil {
		if ctx.Err() == nil {
			janitor.logger.Warn("what somebody wrote could not be erased",
				"application_id", person.ApplicationID, "user_id", person.ID, "error", err)
		}
		return false
	}

	erased, err := janitor.people.Erase(ctx, person.ApplicationID, person.ID, at)
	if err != nil {
		if ctx.Err() == nil {
			janitor.logger.Warn("somebody could not be erased",
				"application_id", person.ApplicationID, "user_id", person.ID, "error", err)
		}
		return false
	}
	return erased
}
