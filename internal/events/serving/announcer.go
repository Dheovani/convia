package serving

import (
	"context"
	"log/slog"

	"convia/internal/transaction"

	"convia/internal/events"
)

/*
Sink is a durable destination for events, written to inside the transaction
that caused them.

It is an interface because the package that implements it â€” webhooks â€” imports
this one for the envelope, and the dependency has to run one way.
*/
type Sink interface {
	Enqueue(ctx context.Context, event events.Event) error
}

/*
Announcer is everything that happens when Convia announces something.

An event that must not be lost is written, inside the transaction that made it
happen, to the journal and to the webhook queue, so either the change and its
announcement both happen or neither does. Streams learn it from the journal once
the transaction commits. See docs/adr/0017, which replaces the queueing half of
docs/adr/0004.

Presence is the exception. It is advisory and is never recorded, so it goes
straight to the live streams, and across instances through the relay.

Publish returns an error because a recorded announcement is part of the change:
a caller that cannot record it must not commit the change either.
*/
type Announcer struct {
	broker  *events.Broker
	journal events.Journal
	durable Sink
	logger  *slog.Logger

	// recorded is told after a transaction that recorded an event commits.
	recorded func()
}

/*
NewAnnouncer composes the live stream with a durable sink, without a journal.

Without a journal an event reaches the live streams of this instance once its
transaction commits, and nothing can resume from it. It is what tests that only
watch a stream use. The sink may be nil, which is a Convia that delivers no
webhooks.
*/
func NewAnnouncer(broker *events.Broker, durable Sink, logger *slog.Logger) *Announcer {
	return &Announcer{broker: broker, durable: durable, logger: logger}
}

/*
NewJournaledAnnouncer records events in journal, and calls recorded after each
transaction that did commits, so that whatever follows the journal can look now
rather than at its next poll.
*/
func NewJournaledAnnouncer(
	broker *events.Broker,
	journal events.Journal,
	durable Sink,
	recorded func(),
	logger *slog.Logger,
) *Announcer {
	return &Announcer{broker: broker, journal: journal, durable: durable, recorded: recorded, logger: logger}
}

// Publish announces an event as part of the change in the context's transaction.
func (announcer *Announcer) Publish(ctx context.Context, event events.Event) error {
	/*
		Not every event is recorded. One type is advisory by construction, and
		recording it would promise a replay or a redelivery that arrives after
		it stopped being true â€” [events.Durable] says which and why.
	*/
	if !events.Durable(event.Type) {
		announcer.broker.Publish(event)
		return nil
	}

	if announcer.journal == nil {
		transaction.AfterCommit(ctx, func(context.Context) { announcer.broker.Publish(event) })
	} else {
		recorded, err := announcer.journal.Record(ctx, event)
		if err != nil {
			return err
		}

		event = recorded
		if announcer.recorded != nil {
			transaction.AfterCommit(ctx, func(context.Context) { announcer.recorded() })
		}
	}

	if announcer.durable == nil {
		return nil
	}
	return announcer.durable.Enqueue(ctx, event)
}
