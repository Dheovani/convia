package events

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// waitFor is how long a test waits for something that should already have
// happened, before deciding it never will.
const waitFor = 2 * time.Second

// receive takes the next event from a stream, or fails the test.
func receive(t *testing.T, stream *Stream) Event {
	t.Helper()

	select {
	case event := <-stream.Events():
		return event
	case <-time.After(waitFor):
		t.Fatal("no event arrived")
		return Event{}
	}
}

// quiet fails if anything arrives on a stream.
func quiet(t *testing.T, stream *Stream) {
	t.Helper()

	select {
	case event := <-stream.Events():
		t.Fatalf("an event arrived that should not have: %s about %s", event.Type, event.Subject.ID)
	case <-time.After(50 * time.Millisecond):
	}
}

// TestAnEventReachesEverySubscriberEntitledToIt is the fan-out the whole
// package exists for.
func TestAnEventReachesEverySubscriberEntitledToIt(t *testing.T) {
	broker := NewBroker()

	first, err := broker.Subscribe("app_1", Types())
	if err != nil {
		t.Fatalf("Subscribe() error = %v", err)
	}
	defer first.Close()

	second, err := broker.Subscribe("app_1", Types())
	if err != nil {
		t.Fatalf("Subscribe() error = %v", err)
	}
	defer second.Close()

	broker.Publish(New(CallStarted, "app_1", "call_1", "req_1", nil))

	for name, stream := range map[string]*Stream{"first": first, "second": second} {
		if got := receive(t, stream).Subject.ID; got != "call_1" {
			t.Errorf("the %s subscriber received an event about %q", name, got)
		}
	}
}

/*
TestATenantNeverSeesAnotherTenantsEvents is the isolation M14-005 asked for,
checked at the only place that could break it.

The application on a stream comes from a verified credential, and the one on an
event comes from the row it describes. Nothing in between could be persuaded to
match them wrongly, which is exactly why the comparison is worth pinning.
*/
func TestATenantNeverSeesAnotherTenantsEvents(t *testing.T) {
	broker := NewBroker()

	mine, err := broker.Subscribe("app_1", Types())
	if err != nil {
		t.Fatalf("Subscribe() error = %v", err)
	}
	defer mine.Close()

	theirs, err := broker.Subscribe("app_2", Types())
	if err != nil {
		t.Fatalf("Subscribe() error = %v", err)
	}
	defer theirs.Close()

	broker.Publish(New(ParticipantJoined, "app_2", "part_2", "", nil))

	quiet(t, mine)
	if got := receive(t, theirs).ApplicationID; got != "app_2" {
		t.Errorf("the other tenant received an event belonging to %q", got)
	}
}

// TestAStreamCarriesOnlyTheTypesItAskedFor proves the subscription is a filter
// and not a label.
func TestAStreamCarriesOnlyTheTypesItAskedFor(t *testing.T) {
	broker := NewBroker()

	stream, err := broker.Subscribe("app_1", []Type{CallStarted, CallEnded})
	if err != nil {
		t.Fatalf("Subscribe() error = %v", err)
	}
	defer stream.Close()

	broker.Publish(New(ParticipantJoined, "app_1", "part_1", "", nil))
	quiet(t, stream)

	broker.Publish(New(CallEnded, "app_1", "call_1", "", nil))
	if got := receive(t, stream).Type; got != CallEnded {
		t.Errorf("the stream delivered %q", got)
	}
}

/*
TestASubscriberThatFallsBehindLosesItsStreamRatherThanEvents is M14-008.

The alternative â€” dropping an event and carrying on â€” is worse than it sounds:
the subscriber would keep receiving events and would have no way to know its
picture had a hole in it. Ending the stream is the only honest signal available
until M14-006 adds a resume cursor, and it is what tells the subscriber to
re-read over REST.
*/
func TestASubscriberThatFallsBehindLosesItsStreamRatherThanEvents(t *testing.T) {
	broker := NewBroker()

	stream, err := broker.Subscribe("app_1", Types())
	if err != nil {
		t.Fatalf("Subscribe() error = %v", err)
	}
	defer stream.Close()

	// Nobody reads, so the queue fills and the next event has nowhere to go.
	for range QueueDepth + 1 {
		broker.Publish(New(CallStarted, "app_1", "call_1", "", nil))
	}

	select {
	case <-stream.Done():
	case <-time.After(waitFor):
		t.Fatal("a subscriber that stopped reading kept its stream")
	}

	if got := stream.Ending(); got != EndedBehind {
		t.Errorf("the stream ended as %d, want %d", got, EndedBehind)
	}
	if broker.Active() != 0 {
		t.Errorf("%d streams are still counted as open", broker.Active())
	}
}

/*
TestOneSubscriberFallingBehindDoesNotCostAnother is the property that makes
publishing safe to call from a domain service.

A publisher that stopped at the first full queue would let one stuck consumer
decide what every other consumer sees.
*/
func TestOneSubscriberFallingBehindDoesNotCostAnother(t *testing.T) {
	broker := NewBroker()

	stuck, err := broker.Subscribe("app_1", Types())
	if err != nil {
		t.Fatalf("Subscribe() error = %v", err)
	}
	defer stuck.Close()

	for range QueueDepth {
		broker.Publish(New(CallStarted, "app_1", "call_1", "", nil))
	}

	healthy, err := broker.Subscribe("app_1", Types())
	if err != nil {
		t.Fatalf("Subscribe() error = %v", err)
	}
	defer healthy.Close()

	broker.Publish(New(CallEnded, "app_1", "call_2", "", nil))

	if got := receive(t, healthy).Subject.ID; got != "call_2" {
		t.Errorf("the healthy subscriber received an event about %q", got)
	}
	select {
	case <-stuck.Done():
	case <-time.After(waitFor):
		t.Error("the stuck subscriber kept its stream")
	}
}

// TestOneTenantCannotHoldEveryPlace covers the per-application ceiling of
// M14-009.
func TestOneTenantCannotHoldEveryPlace(t *testing.T) {
	broker := NewBroker()

	for attempt := range MaxStreamsPerApplication {
		stream, err := broker.Subscribe("app_1", Types())
		if err != nil {
			t.Fatalf("Subscribe() stream %d error = %v", attempt+1, err)
		}
		defer stream.Close()
	}

	if _, err := broker.Subscribe("app_1", Types()); !errors.Is(err, ErrTooManyStreams) {
		t.Errorf("Subscribe() past the ceiling error = %v, want %v", err, ErrTooManyStreams)
	}

	/*
		Another tenant is unaffected, which is what makes this a per-tenant
		ceiling rather than a way for one application to lock out the rest.
	*/
	other, err := broker.Subscribe("app_2", Types())
	if err != nil {
		t.Errorf("a second tenant was refused because the first was full: %v", err)
	} else {
		other.Close()
	}
}

/*
TestClosingAStreamGivesThePlaceBack is what stops the ceiling becoming
permanent.

A client that reconnects normally would otherwise spend its application's
allowance a connection at a time until nothing could connect at all.
*/
func TestClosingAStreamGivesThePlaceBack(t *testing.T) {
	broker := NewBroker()

	for range MaxStreamsPerApplication * 3 {
		stream, err := broker.Subscribe("app_1", Types())
		if err != nil {
			t.Fatalf("Subscribe() error = %v", err)
		}
		stream.Close()
		stream.Close() // Closing twice is safe, so a handler can defer it.
	}

	if broker.Active() != 0 {
		t.Errorf("%d streams are still counted as open", broker.Active())
	}
}

// TestAStreamWithNothingToCarryIsRefused keeps a connection that could never
// deliver anything from being opened.
func TestAStreamWithNothingToCarryIsRefused(t *testing.T) {
	broker := NewBroker()

	if _, err := broker.Subscribe("app_1", nil); !errors.Is(err, ErrNothingToDeliver) {
		t.Errorf("Subscribe() with no types error = %v, want %v", err, ErrNothingToDeliver)
	}
	if _, err := broker.Subscribe("app_1", []Type{"call.rescheduled"}); !errors.Is(err, ErrNothingToDeliver) {
		t.Errorf("Subscribe() with an unknown type error = %v, want %v", err, ErrNothingToDeliver)
	}
}

/*
TestStoppingTellsEverySubscriberAndWaitsForThem is M14-012's shutdown half.

A hijacked connection is invisible to http.Server.Shutdown, so this is the only
thing standing between an orderly stop and every subscriber watching its socket
disappear without explanation.
*/
func TestStoppingTellsEverySubscriberAndWaitsForThem(t *testing.T) {
	broker := NewBroker()

	streams := make([]*Stream, 0, 4)
	for range 4 {
		stream, err := broker.Subscribe("app_1", Types())
		if err != nil {
			t.Fatalf("Subscribe() error = %v", err)
		}
		streams = append(streams, stream)
	}

	/*
		Each subscriber behaves as a connection does: it waits to be told, and
		then lets go. Stop must not return before every one of them has.
	*/
	for _, stream := range streams {
		go func() {
			<-stream.Done()
			stream.Close()
		}()
	}

	ctx, cancel := context.WithTimeout(context.Background(), waitFor)
	defer cancel()

	if !broker.Stop(ctx) {
		t.Fatal("Stop() gave up before every subscriber had let go")
	}

	for _, stream := range streams {
		if got := stream.Ending(); got != EndedByShutdown {
			t.Errorf("a stream ended as %d, want %d", got, EndedByShutdown)
		}
	}
	if _, err := broker.Subscribe("app_1", Types()); !errors.Is(err, ErrStopped) {
		t.Errorf("Subscribe() after stopping error = %v, want %v", err, ErrStopped)
	}
}

/*
TestStoppingGivesUpRatherThanHangingOnASubscriber bounds the wait above.

A subscriber that never lets go must not be able to hold a shutdown open, or a
single stuck socket would keep an instance alive past every deadline an
orchestrator has.
*/
func TestStoppingGivesUpRatherThanHangingOnASubscriber(t *testing.T) {
	broker := NewBroker()

	stream, err := broker.Subscribe("app_1", Types())
	if err != nil {
		t.Fatalf("Subscribe() error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	if broker.Stop(ctx) {
		t.Error("Stop() reported that every subscriber had let go while one had not")
	}
	if got := stream.Ending(); got != EndedByShutdown {
		t.Errorf("the held stream ended as %d, want %d", got, EndedByShutdown)
	}

	stream.Close()
}

/*
TestPublishingAndSubscribingAtOnce is M14-012's concurrency half, and is where
-race earns its place.

Connections arrive and leave while events are being published, which is the
ordinary state of a running instance rather than an edge case.
*/
func TestPublishingAndSubscribingAtOnce(t *testing.T) {
	broker := NewBroker()

	var work sync.WaitGroup
	stop := make(chan struct{})

	work.Add(1)
	go func() {
		defer work.Done()
		for {
			select {
			case <-stop:
				return
			default:
				broker.Publish(New(ParticipantJoined, "app_1", "part_1", "", nil))
			}
		}
	}()

	for range 8 {
		work.Add(1)
		go func() {
			defer work.Done()
			for range 50 {
				stream, err := broker.Subscribe("app_1", Types())
				if errors.Is(err, ErrTooManyStreams) {
					continue
				}
				if err != nil {
					t.Errorf("Subscribe() error = %v", err)
					return
				}

				// Read whatever is there, then leave, which is what a client
				// that reconnected does.
				select {
				case <-stream.Events():
				default:
				}
				stream.Close()
			}
		}()
	}

	work.Add(1)
	go func() {
		defer work.Done()
		<-time.After(20 * time.Millisecond)
		close(stop)
	}()

	work.Wait()

	if broker.Active() != 0 {
		t.Errorf("%d streams outlived their subscribers", broker.Active())
	}
}

// TestPublishingWithNobodyListeningIsHarmless covers the ordinary case of an
// instance nobody has subscribed to.
func TestPublishingWithNobodyListeningIsHarmless(t *testing.T) {
	broker := NewBroker()
	broker.Publish(New(CallStarted, "app_1", "call_1", "", nil))

	if broker.Active() != 0 {
		t.Errorf("publishing to nobody opened %d streams", broker.Active())
	}
}

/*
TestPeopleDoNotSpendAnApplicationsStreams keeps Convia's own product from
crowding out the backends of the application it is.

The first-party application is an application like any other, with eight
streams for its backend. Were a person's tab counted against those, the eighth
browser tab would refuse the backend its stream.
*/
func TestPeopleDoNotSpendAnApplicationsStreams(t *testing.T) {
	broker := NewBroker()

	for range MaxStreamsPerPerson {
		opened, err := broker.SubscribePerson("app_1", "usr_ana", Types())
		if err != nil {
			t.Fatalf("SubscribePerson() error = %v", err)
		}
		t.Cleanup(opened.Close)
	}

	if _, err := broker.SubscribePerson("app_1", "usr_ana", Types()); !errors.Is(err, ErrTooManyStreams) {
		t.Errorf("one person opened more than %d streams: error = %v", MaxStreamsPerPerson, err)
	}

	other, err := broker.SubscribePerson("app_1", "usr_bea", Types())
	if err != nil {
		t.Fatalf("SubscribePerson() error = %v", err)
	}
	backend, err := broker.Subscribe("app_1", Types())
	if err != nil {
		t.Fatalf("an application was refused its stream by its people's: %v", err)
	}
	defer backend.Close()

	/*
		Ending a person's stream frees a place for that person, and only that
		count moves, which is what a leak between the two would break.
	*/
	other.Close()
	if broker.people != MaxStreamsPerPerson || broker.applications != 1 {
		t.Errorf("counts are %d people and %d applications, want %d and 1",
			broker.people, broker.applications, MaxStreamsPerPerson)
	}
}
