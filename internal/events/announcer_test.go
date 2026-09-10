package events

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
)

// refusingSink is a durable destination that cannot accept anything.
type refusingSink struct {
	err      error
	received []Event
}

func (sink *refusingSink) Enqueue(_ context.Context, event Event) error {
	sink.received = append(sink.received, event)
	return sink.err
}

/*
TestAnAdvisoryEventIsNotQueuedForRedelivery keeps the two promises apart at the
point where both are made.

The stream still carries it, because a stream has no second attempt: an event
reaches whoever is connected at that moment, in order, or not at all. The
durable half is skipped, because a retry there would arrive after the report
stopped being true and after the newer one that replaced it.
*/
func TestAnAdvisoryEventIsNotQueuedForRedelivery(t *testing.T) {
	broker := NewBroker()
	sink := &refusingSink{}
	announcer := NewAnnouncer(broker, sink, slog.New(slog.NewTextHandler(io.Discard, nil)))

	stream, err := broker.Subscribe("app_1", Types())
	if err != nil {
		t.Fatalf("Subscribe() error = %v", err)
	}
	defer stream.Close()

	event := New(PresenceChanged, "app_1", "usr_1", "req_1", Data{"state": "online"})
	announcer.Publish(context.Background(), event)

	if got := receive(t, stream).ID; got != event.ID {
		t.Errorf("the live stream received %q, want the advisory event", got)
	}
	if len(sink.received) != 0 {
		t.Errorf("an advisory event was queued for redelivery: %v", sink.received)
	}
}

/*
TestAnnouncingReachesBothHalves is the composition doing its one job.

A subscriber connected right now and a destination registered last week are
told about the same occurrence, from one call, so a domain service never has to
know there are two of them.
*/
func TestAnnouncingReachesBothHalves(t *testing.T) {
	broker := NewBroker()
	sink := &refusingSink{}
	announcer := NewAnnouncer(broker, sink, slog.New(slog.NewTextHandler(io.Discard, nil)))

	stream, err := broker.Subscribe("app_1", Types())
	if err != nil {
		t.Fatalf("Subscribe() error = %v", err)
	}
	defer stream.Close()

	event := New(CallStarted, "app_1", "call_1", "req_1", nil)
	announcer.Publish(context.Background(), event)

	if got := receive(t, stream).ID; got != event.ID {
		t.Errorf("the live stream received %q", got)
	}
	if len(sink.received) != 1 || sink.received[0].ID != event.ID {
		t.Errorf("the durable sink received %v", sink.received)
	}
}

/*
TestAQueueThatFailsDoesNotUndoWhatHappened is the decision ADR 0004 records,
made checkable.

The domain change has already committed by the time this runs. Failing here
would tell the caller that nothing happened when something did, and a caller
that retried would produce a second call, a second participant, or a second
invitation — which is worse than a missed notification.
*/
func TestAQueueThatFailsDoesNotUndoWhatHappened(t *testing.T) {
	logs := &bytes.Buffer{}
	broker := NewBroker()
	sink := &refusingSink{err: errors.New("the database is unreachable")}

	announcer := NewAnnouncer(broker, sink, slog.New(slog.NewJSONHandler(logs, nil)))

	stream, err := broker.Subscribe("app_1", Types())
	if err != nil {
		t.Fatalf("Subscribe() error = %v", err)
	}
	defer stream.Close()

	event := New(ParticipantJoined, "app_1", "part_1", "req_1", nil)

	// Publish returns nothing, so the only thing that could go wrong here is a
	// panic. The assertion is that the caller carries on.
	announcer.Publish(context.Background(), event)

	/*
		The live stream is unaffected. One half failing must not cost the other,
		or a database problem would also take down every open connection.
	*/
	if got := receive(t, stream).ID; got != event.ID {
		t.Errorf("a failed queue write cost the live stream its event: %q", got)
	}

	/*
		And the loss is recorded precisely. This log line is the only trace of
		an event no destination will ever receive, so it has to name the event —
		that is what lets an operator match it with the audit entry for the same
		occurrence and say what was missed.
	*/
	recorded := logs.String()
	if !strings.Contains(recorded, event.ID) {
		t.Errorf("the failure does not name the event: %s", recorded)
	}
	if !strings.Contains(recorded, `"level":"ERROR"`) {
		t.Errorf("an undeliverable event was not reported as an error: %s", recorded)
	}
	if !strings.Contains(recorded, "app_1") {
		t.Errorf("the failure does not name the tenant: %s", recorded)
	}
}

/*
TestAnnouncingWithNoDurableSinkIsSupported covers the configuration every test
that only cares about the stream uses.

A Convia that streams events and delivers no webhooks is a deployment, not a
degraded state.
*/
func TestAnnouncingWithNoDurableSinkIsSupported(t *testing.T) {
	broker := NewBroker()
	announcer := NewAnnouncer(broker, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))

	stream, err := broker.Subscribe("app_1", Types())
	if err != nil {
		t.Fatalf("Subscribe() error = %v", err)
	}
	defer stream.Close()

	announcer.Publish(context.Background(), New(CallEnded, "app_1", "call_1", "", nil))

	if got := receive(t, stream).Type; got != CallEnded {
		t.Errorf("the stream received %q", got)
	}
}
