package events

import (
	"context"
	"sync"
	"testing"
	"time"
)

/*
scriptedRelay stands in for the other instances of a deployment.

It records what this instance sent them and lets a test hand back what they
sent, which is the whole of the seam the real relay sits in.
*/
type scriptedRelay struct {
	mutex     sync.Mutex
	broadcast []Event
}

func (relay *scriptedRelay) Broadcast(event Event) {
	relay.mutex.Lock()
	defer relay.mutex.Unlock()
	relay.broadcast = append(relay.broadcast, event)
}

func (relay *scriptedRelay) Close() error { return nil }

func (relay *scriptedRelay) carried() []Event {
	relay.mutex.Lock()
	defer relay.mutex.Unlock()
	return append([]Event(nil), relay.broadcast...)
}

/*
TestAnEventGoesToTheOtherInstancesToo is what M16 exists for.

Before it, an event produced while serving a request on one instance reached
only the subscribers connected to that instance. docs/events.md named this as
the thing that would fix it, and this is the fix at its seam.
*/
func TestAnEventGoesToTheOtherInstancesToo(t *testing.T) {
	relay := &scriptedRelay{}
	broker := NewSharedBroker(relay)

	stream, err := broker.Subscribe("app_1", Types())
	if err != nil {
		t.Fatalf("Subscribe() error = %v", err)
	}
	defer stream.Close()

	event := New(CallStarted, "app_1", "call_1", "req_1", nil)
	broker.Publish(event)

	// The subscribers here still get it directly, without a round trip.
	if got := receive(t, stream).ID; got != event.ID {
		t.Errorf("the local subscriber received %q", got)
	}

	carried := relay.carried()
	if len(carried) != 1 || carried[0].ID != event.ID {
		t.Errorf("the other instances were sent %v", carried)
	}
}

/*
TestAnEventFromElsewhereReachesTheSubscribersHere is the other direction, and
it is the half a subscriber actually notices.
*/
func TestAnEventFromElsewhereReachesTheSubscribersHere(t *testing.T) {
	relay := &scriptedRelay{}
	broker := NewSharedBroker(relay)

	stream, err := broker.Subscribe("app_1", Types())
	if err != nil {
		t.Fatalf("Subscribe() error = %v", err)
	}
	defer stream.Close()

	elsewhere := New(ParticipantJoined, "app_1", "part_1", "req_1", nil)
	broker.Receive(elsewhere)

	if got := receive(t, stream).ID; got != elsewhere.ID {
		t.Errorf("the subscriber received %q", got)
	}
}

/*
TestAnEventFromElsewhereIsNotSentBack is the loop two instances would otherwise
make of each other.

Each would receive the other's event, treat it as something to announce, and
send it on. The messages would multiply until something gave out, and the first
sign of it would be a Redis at full load.
*/
func TestAnEventFromElsewhereIsNotSentBack(t *testing.T) {
	relay := &scriptedRelay{}
	broker := NewSharedBroker(relay)

	broker.Receive(New(CallEnded, "app_1", "call_1", "", nil))

	if carried := relay.carried(); len(carried) != 0 {
		t.Errorf("an event that arrived from elsewhere was sent back: %v", carried)
	}
}

/*
TestWhoIsEntitledIsDecidedTheSameWayWhereverAnEventCameFrom is the property
that makes the relay safe to add.

A relay carries events between instances of one deployment; it does not carry
authority. An event that arrived over the wire is filtered by tenant and by
type exactly as a local one is, so a subscriber cannot be shown something it
could not have been shown before.
*/
func TestWhoIsEntitledIsDecidedTheSameWayWhereverAnEventCameFrom(t *testing.T) {
	broker := NewSharedBroker(&scriptedRelay{})

	mine, err := broker.Subscribe("app_1", []Type{CallStarted})
	if err != nil {
		t.Fatalf("Subscribe() error = %v", err)
	}
	defer mine.Close()

	// Another tenant's event, arriving from another instance.
	broker.Receive(New(CallStarted, "app_2", "call_2", "", nil))
	quiet(t, mine)

	// This tenant's event, but a type this stream did not subscribe to.
	broker.Receive(New(ParticipantJoined, "app_1", "part_1", "", nil))
	quiet(t, mine)

	broker.Receive(New(CallStarted, "app_1", "call_1", "", nil))
	if got := receive(t, mine).Subject.ID; got != "call_1" {
		t.Errorf("the stream delivered an event about %q", got)
	}
}

/*
TestASubscriberFromElsewhereCanStillFallBehind keeps the queue meaningful for
events that arrived over the wire.

A relay makes more events reach an instance, so if anything it makes falling
behind more likely rather than less. The subscriber has to be told the same
way.
*/
func TestASubscriberFromElsewhereCanStillFallBehind(t *testing.T) {
	broker := NewSharedBroker(&scriptedRelay{})

	stream, err := broker.Subscribe("app_1", Types())
	if err != nil {
		t.Fatalf("Subscribe() error = %v", err)
	}
	defer stream.Close()

	for range QueueDepth + 1 {
		broker.Receive(New(CallStarted, "app_1", "call_1", "", nil))
	}

	select {
	case <-stream.Done():
	case <-time.After(waitFor):
		t.Fatal("a subscriber that stopped reading kept its stream")
	}
	if got := stream.Ending(); got != EndedBehind {
		t.Errorf("the stream ended as %d, want %d", got, EndedBehind)
	}
}

/*
TestABrokerWithNoRelayIsUnchanged covers the ordinary deployment.

One instance needs nothing carried anywhere, and the code path that would carry
it is not merely unused — it is absent, so a single-instance Convia cannot fail
in a way that only a multi-instance one could.
*/
func TestABrokerWithNoRelayIsUnchanged(t *testing.T) {
	broker := NewBroker()

	stream, err := broker.Subscribe("app_1", Types())
	if err != nil {
		t.Fatalf("Subscribe() error = %v", err)
	}
	defer stream.Close()

	event := New(CallStarted, "app_1", "call_1", "", nil)
	broker.Publish(event)

	if got := receive(t, stream).ID; got != event.ID {
		t.Errorf("the subscriber received %q", got)
	}
}

/*
TestAStoppingInstanceStillTellsTheOthers pins a decision that is easy to get
backwards.

An instance shuts its own streams before it stops serving, so a request still
finishing publishes into a broker that has no subscribers left. Locally there
is nothing to do with that event — but the *other* instances have subscribers
who should see it, and the thing being announced really did happen. Stopping is
this instance leaving, not the deployment going quiet.
*/
func TestAStoppingInstanceStillTellsTheOthers(t *testing.T) {
	relay := &scriptedRelay{}
	broker := NewSharedBroker(relay)

	ctx, cancel := context.WithTimeout(context.Background(), waitFor)
	defer cancel()
	broker.Stop(ctx)

	event := New(CallStarted, "app_1", "call_1", "req_1", nil)
	broker.Publish(event)

	carried := relay.carried()
	if len(carried) != 1 || carried[0].ID != event.ID {
		t.Errorf("an event published while stopping reached the other instances as %v", carried)
	}
}
