package events

import (
	"sync"
	"testing"
	"time"
)

/*
recordingWatcher is what a metrics recorder does, minus the metrics.

It takes a lock because [Watcher.Ended] is called while the broker holds its
own, from whichever goroutine ended the stream, and a test that raced here
would fail somewhere else.
*/
type recordingWatcher struct {
	mutex      sync.Mutex
	reasons    []string
	deliveries []time.Duration
}

func (watcher *recordingWatcher) Ended(reason string) {
	watcher.mutex.Lock()
	defer watcher.mutex.Unlock()
	watcher.reasons = append(watcher.reasons, reason)
}

func (watcher *recordingWatcher) Delivered(latency time.Duration) {
	watcher.mutex.Lock()
	defer watcher.mutex.Unlock()
	watcher.deliveries = append(watcher.deliveries, latency)
}

func (watcher *recordingWatcher) read() ([]string, []time.Duration) {
	watcher.mutex.Lock()
	defer watcher.mutex.Unlock()
	return append([]string(nil), watcher.reasons...), append([]time.Duration(nil), watcher.deliveries...)
}

/*
TestTheBrokerSaysWhenAStreamFallsBehind.

This is the reading `M14-013` exists for, proved against the real broker rather
than against an instrument called by hand: a subscriber whose view got a hole in
it reconnects and fills the gap, so every other signal looks healthy and only
this one says it happened.
*/
func TestTheBrokerSaysWhenAStreamFallsBehind(t *testing.T) {
	watcher := &recordingWatcher{}
	broker := NewBroker()
	broker.Watch(watcher)

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

	reasons, _ := watcher.read()
	if len(reasons) != 1 || reasons[0] != "behind" {
		t.Errorf("the watcher was told %v, want one ending named behind", reasons)
	}
}

/*
TestTheBrokerSaysWhenASubscriberLeaves and when it is shut down.

All three reasons pass through one place, which is why they are counted there.
A test per caller would be a test per caller somebody adds a fourth without.
*/
func TestTheBrokerSaysWhyEveryStreamEnded(t *testing.T) {
	for reason, ending := range map[string]func(*Broker, *Stream){
		"reader": func(_ *Broker, stream *Stream) { stream.Close() },
		/*
			A shutdown ends the stream and then waits for whoever was serving
			it to let go, so the test has to be that subscriber: it watches for
			Done and closes, which is exactly what the HTTP handler does.
			Calling Stop without that waits for a release nobody will make.
		*/
		"shutdown": func(broker *Broker, stream *Stream) {
			go func() {
				<-stream.Done()
				stream.Close()
			}()
			if !broker.Stop(t.Context()) {
				t.Error("shutdown: the broker gave up waiting for its subscriber")
			}
		},
	} {
		watcher := &recordingWatcher{}
		broker := NewBroker()
		broker.Watch(watcher)

		stream, err := broker.Subscribe("app_1", Types())
		if err != nil {
			t.Fatalf("%s: Subscribe() error = %v", reason, err)
		}

		ending(broker, stream)

		select {
		case <-stream.Done():
		case <-time.After(waitFor):
			t.Fatalf("%s: the stream did not finish", reason)
		}

		reasons, _ := watcher.read()
		if len(reasons) != 1 || reasons[0] != reason {
			t.Errorf("%s: the watcher was told %v", reason, reasons)
		}
	}
}

/*
TestDeliveryIsTimedFromWhenTheChangeHappened.

The interesting delay is the transaction that had to commit before the event
could be published, not the microseconds spent fanning it out — so the clock
starts when the change happened rather than when Publish was called.
*/
func TestDeliveryIsTimedFromWhenTheChangeHappened(t *testing.T) {
	watcher := &recordingWatcher{}
	broker := NewBroker()
	broker.Watch(watcher)

	event := New(CallStarted, "app_1", "call_1", "", nil)
	event.OccurredAt = time.Now().UTC().Add(-250 * time.Millisecond)

	broker.Publish(event)

	_, deliveries := watcher.read()
	if len(deliveries) != 1 {
		t.Fatalf("the watcher was told of %d deliveries, want 1", len(deliveries))
	}
	if deliveries[0] < 200*time.Millisecond {
		t.Errorf("the delivery took %s, want at least the quarter second the change waited",
			deliveries[0])
	}
}

/*
TestAnEventFromAnotherInstanceIsNotTimed.

It was timestamped by that instance's clock, and a latency computed across two
clocks measures the skew between them at least as much as it measures Convia.
*/
func TestAnEventFromAnotherInstanceIsNotTimed(t *testing.T) {
	watcher := &recordingWatcher{}
	broker := NewBroker()
	broker.Watch(watcher)

	broker.Receive(New(CallStarted, "app_1", "call_1", "", nil))

	if _, deliveries := watcher.read(); len(deliveries) != 0 {
		t.Errorf("a relayed event was timed: %v", deliveries)
	}
}

// TestABrokerWithNoWatcherIsUnchanged: measuring is optional, and seventy
// places build one without it.
func TestABrokerWithNoWatcherIsUnchanged(t *testing.T) {
	broker := NewBroker()

	stream, err := broker.Subscribe("app_1", Types())
	if err != nil {
		t.Fatalf("Subscribe() error = %v", err)
	}

	broker.Publish(New(CallStarted, "app_1", "call_1", "", nil))
	stream.Close()

	select {
	case <-stream.Done():
	case <-time.After(waitFor):
		t.Fatal("the stream did not finish without a watcher")
	}
}
