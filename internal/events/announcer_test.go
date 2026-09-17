package events

import (
	"context"
	"errors"
	"io"
	"log/slog"
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
TestAQueueThatFailsFailsTheChange is the decision docs/adr/0017 records, made
checkable.

The announcement is written in the transaction that made the change, so a queue
that cannot take it must fail the change too: otherwise a destination would
never hear of something that happened.
*/
func TestAQueueThatFailsFailsTheChange(t *testing.T) {
	broker := NewBroker()
	refusal := errors.New("the database is unreachable")
	sink := &refusingSink{err: refusal}
	announcer := NewAnnouncer(broker, sink, slog.New(slog.NewTextHandler(io.Discard, nil)))

	err := announcer.Publish(context.Background(), New(ParticipantJoined, "app_1", "part_1", "req_1", nil))
	if !errors.Is(err, refusal) {
		t.Errorf("Publish() error = %v, want the queue's", err)
	}
}

// fakeJournal records events, and can refuse to.
type fakeJournal struct {
	err      error
	recorded []Event
}

func (journal *fakeJournal) Record(_ context.Context, event Event) (Event, error) {
	if journal.err != nil {
		return Event{}, journal.err
	}
	event.Cursor = Cursor{Transaction: 7, Position: int64(len(journal.recorded) + 1)}.String()
	journal.recorded = append(journal.recorded, event)
	return event, nil
}

/*
TestAJournaledEventReachesStreamsThroughTheJournal: the announcer records the
event, queues it with its cursor, and leaves the live streams to whatever follows
the journal, which it wakes.
*/
func TestAJournaledEventReachesStreamsThroughTheJournal(t *testing.T) {
	broker := NewBroker()
	journal := &fakeJournal{}
	sink := &refusingSink{}
	woken := 0
	announcer := NewJournaledAnnouncer(broker, journal, sink, func() { woken++ },
		slog.New(slog.NewTextHandler(io.Discard, nil)))

	stream, err := broker.Subscribe("app_1", Types())
	if err != nil {
		t.Fatalf("Subscribe() error = %v", err)
	}
	defer stream.Close()

	event := New(CallStarted, "app_1", "call_1", "req_1", nil)
	if err := announcer.Publish(context.Background(), event); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}

	if len(journal.recorded) != 1 || journal.recorded[0].ID != event.ID {
		t.Fatalf("the journal recorded %v", journal.recorded)
	}
	if len(sink.received) != 1 || sink.received[0].Cursor != "7-1" {
		t.Errorf("the queue received %v, want the event with its cursor", sink.received)
	}
	if woken != 1 {
		t.Errorf("the follower was woken %d times, want once", woken)
	}
	select {
	case received := <-stream.Events():
		t.Errorf("the announcer delivered %v itself, want the journal's follower to", received)
	default:
	}

	journal.err = errors.New("the journal is full")
	if err := announcer.Publish(context.Background(), New(CallEnded, "app_1", "call_1", "", nil)); err == nil {
		t.Error("Publish() succeeded although the journal refused the event")
	}
	if len(sink.received) != 1 {
		t.Error("an event the journal refused was queued anyway")
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
