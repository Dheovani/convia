package events

import (
	"context"
	"log/slog"
)

/*
Sink is a durable destination for events, written to while the request that
caused them is still running.

It is an interface because the package that implements it — webhooks — imports
this one for the envelope, and the dependency has to run one way. It is also
the honest shape: a durable write can fail, and saying so is what lets the
failure be handled here rather than pretended away.
*/
type Sink interface {
	Enqueue(ctx context.Context, event Event) error
}

/*
Announcer is everything that happens when Convia announces something.

There are two of those and they promise different things, which is the whole
reason this type exists rather than the domains calling both:

  - **The live stream** reaches whoever is connected right now. It cannot
    block, cannot fail, and holds nothing. A subscriber that stopped reading
    loses its stream; the request that produced the event never notices.
  - **The durable sink** records what is owed to destinations an application
    registered. It is one statement, it runs before the request returns, and it
    can fail.

Publish takes a context and returns nothing, and both halves of that are
deliberate. The context is there because a durable write is real work with a
deadline. Nothing is returned because a webhook that could not be queued must
not also undo the thing that happened: the call has already succeeded, the row
is already committed, and failing the response now would tell the caller that
nothing happened when something did.

What that costs is stated rather than hidden. A queue write that fails is an
event no destination will ever receive, and the only trace is the error logged
here — which names the event, so it can be matched with the audit entry for the
same occurrence.
*/
type Announcer struct {
	broker  *Broker
	durable Sink
	logger  *slog.Logger
}

/*
NewAnnouncer composes the live stream with a durable sink.

The sink may be nil, which is a Convia that streams events and delivers no
webhooks. That is a supported configuration rather than a degraded one, and it
is what every test that only cares about the stream uses.
*/
func NewAnnouncer(broker *Broker, durable Sink, logger *slog.Logger) *Announcer {
	return &Announcer{broker: broker, durable: durable, logger: logger}
}

// Publish delivers an event to the live stream and records what is owed.
func (announcer *Announcer) Publish(ctx context.Context, event Event) {
	announcer.broker.Publish(event)

	/*
		Not every event has a durable half. One type is advisory by
		construction, and queueing it would promise a redelivery that arrives
		after it stopped being true — [Durable] says which and why. Asking here
		rather than at the sink keeps the answer in one place: an endpoint
		cannot subscribe to it either, and both refusals read the same rule.
	*/
	if announcer.durable == nil || !Durable(event.Type) {
		return
	}

	if err := announcer.durable.Enqueue(ctx, event); err != nil {
		/*
			Logged at error and named precisely, because this is the one way an
			event that should have reached a destination never will. The event
			identifier is here so that the line can be matched with the audit
			entry for the same occurrence, which is what an operator needs to
			say what was missed.
		*/
		announcer.logger.Error("an event could not be queued for delivery",
			"error", err,
			"event_id", event.ID,
			"event", string(event.Type),
			"application_id", event.ApplicationID,
			"subject_id", event.Subject.ID,
		)
	}
}
