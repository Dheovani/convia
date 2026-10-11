package serving

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket/wsjson"

	"convia/internal/events"
)

// endings counts how streams finished, as the telemetry does.
type endings struct {
	mutex sync.Mutex
	seen  map[string]int
}

func (watched *endings) Ended(reason string) {
	watched.mutex.Lock()
	defer watched.mutex.Unlock()
	watched.seen[reason]++
}

func (watched *endings) Delivered(time.Duration) {}

func (watched *endings) count(reason string) int {
	watched.mutex.Lock()
	defer watched.mutex.Unlock()
	return watched.seen[reason]
}

/*
TestAClientThatStopsReadingCostsNobodyElse is `M25-007`'s slow consumer, over
real sockets rather than the broker's channels: a client that keeps its
connection and stops reading fills its socket, then its queue, and is ended as
behind -- while a client beside it receives every event, in order, without
waiting for it.

What is being proved is the part a channel test cannot reach. Once the socket
is full, the stream's writer is stuck in a write for up to writeTimeout, and
the broker must not care: it ends the stream the moment the queue is full,
whatever its writer is doing.
*/
func TestAClientThatStopsReadingCostsNobodyElse(t *testing.T) {
	broker := events.NewBroker()
	watched := &endings{seen: map[string]int{}}
	broker.Watch(watched)
	server := listening(t, broker, everything())

	reading := subscriber(t, server)
	waitUntilOpen(t, broker, 1)
	stopped := subscriber(t, server)
	waitUntilOpen(t, broker, 2)
	_ = stopped // held open and never read, which is the whole of its part

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	var received atomic.Int64
	var outOfOrder atomic.Int64
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		for {
			var event events.Event
			if err := wsjson.Read(ctx, reading, &event); err != nil {
				return
			}
			if want := received.Load(); event.Data["n"] != float64(want) {
				outOfOrder.Add(1)
			}
			received.Add(1)
		}
	}()

	/*
		Published as fast as the reading client takes them, so that it never
		falls behind on its own account: the publisher stays within half a
		queue of it. A kilobyte of padding fills the stopped client's socket
		in thousands of events rather than hundreds of thousands.
	*/
	padding := strings.Repeat("x", 1024)
	published := int64(0)
	for watched.count(events.EndedBehind.String()) == 0 {
		if ctx.Err() != nil {
			t.Fatalf("the client that stopped reading was never ended; %d events published", published)
		}
		for published-received.Load() > events.QueueDepth/2 {
			if ctx.Err() != nil {
				t.Fatalf("the reading client stopped receiving at %d of %d events", received.Load(), published)
			}
			time.Sleep(time.Millisecond)
		}
		broker.Publish(events.New(events.ParticipantJoined, "app_1", "part_1", "req_1", events.Data{
			"call_id": "call_1", "role": "member", "guest": false, "user_id": "usr_1",
			"n": published, "padding": padding,
		}))
		published++
	}

	// The stopped client's place is given back at once, though its writer may still be blocked.
	if active := broker.Active(); active != 1 {
		t.Errorf("%d streams open after the stopped client was ended, want the reading one", active)
	}

	// And the one beside it carries on: it has everything, and receives more.
	for range 10 {
		broker.Publish(events.New(events.ParticipantJoined, "app_1", "part_1", "req_1", events.Data{
			"call_id": "call_1", "role": "member", "guest": false, "user_id": "usr_1", "n": published,
		}))
		published++
	}
	deadline := time.Now().Add(waitFor)
	for received.Load() < published && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := received.Load(); got != published {
		t.Errorf("the reading client received %d of %d events", got, published)
	}
	if outOfOrder.Load() != 0 {
		t.Errorf("the reading client received %d events out of order", outOfOrder.Load())
	}
	if behind := watched.count(events.EndedBehind.String()); behind != 1 {
		t.Errorf("%d streams were ended as behind, want the one that stopped reading", behind)
	}
	t.Logf("the stopped client was ended after %d events", published-10)

	reading.CloseNow()
	<-readerDone
}
