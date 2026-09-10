package events

import (
	"context"
	"errors"
	"maps"
	"slices"
	"sync"
	"sync/atomic"
)

const (
	/*
		QueueDepth is how far behind one subscriber may fall before its stream
		is ended.

		A queue is what keeps a momentary hesitation — a garbage collection
		pause, a slow network — from costing a subscriber its connection, and
		what stops a subscriber that has genuinely stopped reading from
		consuming memory without limit. Two hundred and fifty-six events is
		well beyond any burst a single call produces and is a few tens of
		kilobytes at rest.
	*/
	QueueDepth = 256

	/*
		MaxStreamsPerApplication bounds one tenant's concurrent streams.

		Every instance of an application's backend needs one stream, not one
		per user, because the stream carries the whole tenant's events. Eight
		leaves room for a rolling deployment holding old and new instances at
		once, and still bounds what a single credential can hold open.
	*/
	MaxStreamsPerApplication = 8

	/*
		MaxStreams bounds the whole instance.

		It exists so that many tenants cannot together do what one tenant is
		already stopped from doing. Reaching it is answered the same way as
		reaching the per-application ceiling, so a tenant is never told
		anything about the instance's total load.
	*/
	MaxStreams = 1024
)

/*
ErrTooManyStreams reports a subscription refused because a ceiling was reached.

Both ceilings produce this one error. Which was reached is logged for operators
and never distinguished to the caller.
*/
var ErrTooManyStreams = errors.New("too many open event streams")

// ErrNothingToDeliver reports a subscription asking for no event types, which
// would be a connection that could never carry anything.
var ErrNothingToDeliver = errors.New("a stream must subscribe to at least one event type")

// ErrStopped reports a broker that has been shut down.
var ErrStopped = errors.New("the event broker has stopped")

/*
Ending says why a stream finished.

A subscriber needs it because the three cases mean different things to whoever
is reading: one is orderly, one says the picture is now incomplete, and one
says to reconnect elsewhere.
*/
type Ending int32

const (
	// EndedByReader means the subscriber closed its own stream.
	EndedByReader Ending = iota
	/*
		EndedBehind means the subscriber fell further behind than the queue
		allows and events were dropped.

		It is a distinct ending because it is the one case where reconnecting
		is not enough: the subscriber's view has a gap in it, and until M14-006
		adds a resume cursor the only way to close that gap is to re-read the
		affected calls over REST.
	*/
	EndedBehind
	// EndedByShutdown means this instance is stopping.
	EndedByShutdown
)

/*
Broker fans control events out to whoever is listening right now.

It is in-process and holds nothing: an event is delivered to the streams open
on this instance at the moment it is published, and is then gone. That is the
whole design, and its consequence is stated rather than hidden — an event
produced while a subscriber is disconnected is not waiting for them when they
return, and an event produced on another instance never reaches them at all.
docs/events.md says what running more than one instance therefore requires.

Publishing never blocks and never fails, which is what keeps a slow reader from
becoming a slow API. A subscriber that cannot keep up loses its stream rather
than delaying the request that produced the event.
*/
type Broker struct {
	mutex   sync.Mutex
	streams map[*Stream]struct{}
	perTeam map[string]int
	stopped bool

	/*
		serving counts the goroutines that hold a stream, so that stopping can
		wait for them rather than hoping. It is separate from the map above:
		a stream is out of the map the moment it ends, while the goroutine
		serving it still has a close frame to write.
	*/
	serving sync.WaitGroup
}

// NewBroker returns a broker with no subscribers.
func NewBroker() *Broker {
	return &Broker{
		streams: make(map[*Stream]struct{}),
		perTeam: make(map[string]int),
	}
}

/*
Stream is one subscriber's view of the events it is entitled to.

It is read from exactly one goroutine, the one serving the connection. Done
reports that no further event will arrive, and Ending says why.
*/
type Stream struct {
	broker        *Broker
	applicationID string
	wanted        map[Type]struct{}

	events chan Event
	done   chan struct{}
	once   sync.Once
	ending atomic.Int32

	// released fires once, when the subscriber has finished with the stream,
	// which is what a shutdown waits for.
	released sync.Once

	delivered atomic.Int64
}

// Events yields the events this stream is entitled to, in the order published.
func (stream *Stream) Events() <-chan Event { return stream.events }

// Done is closed when no further event will arrive on this stream.
func (stream *Stream) Done() <-chan struct{} { return stream.done }

/*
Ending reports why the stream finished.

It is meaningful only after Done is closed; before that it reads as the
orderly ending, which is the safe answer to give a caller that asked too early.
*/
func (stream *Stream) Ending() Ending { return Ending(stream.ending.Load()) }

// Delivered counts the events handed to this subscriber, for the summary an
// operator sees when a stream closes.
func (stream *Stream) Delivered() int64 { return stream.delivered.Load() }

// Wants reports whether this stream carries a type of event.
func (stream *Stream) Wants(kind Type) bool {
	_, wanted := stream.wanted[kind]
	return wanted
}

/*
Close ends the stream and releases the subscriber's place.

It is safe to call more than once and safe to call after the broker has already
ended the stream, so the goroutine serving a connection can defer it without
knowing which side finished first.
*/
func (stream *Stream) Close() {
	stream.broker.end(stream, EndedByReader)
	stream.released.Do(stream.broker.serving.Done)
}

/*
Subscribe opens a stream of the named event types for one application.

The types are settled here, at the point where the caller's authority is
already known, rather than being chosen by the client over the connection. A
subscriber therefore cannot widen what it receives after connecting, and there
is no message it could send that would try.
*/
func (broker *Broker) Subscribe(applicationID string, types []Type) (*Stream, error) {
	if len(types) == 0 {
		return nil, ErrNothingToDeliver
	}

	wanted := make(map[Type]struct{}, len(types))
	for _, kind := range types {
		if !kind.Known() {
			return nil, ErrNothingToDeliver
		}
		wanted[kind] = struct{}{}
	}

	broker.mutex.Lock()
	defer broker.mutex.Unlock()

	if broker.stopped {
		return nil, ErrStopped
	}

	if len(broker.streams) >= MaxStreams || broker.perTeam[applicationID] >= MaxStreamsPerApplication {
		return nil, ErrTooManyStreams
	}

	stream := &Stream{
		broker:        broker,
		applicationID: applicationID,
		wanted:        wanted,
		events:        make(chan Event, QueueDepth),
		done:          make(chan struct{}),
	}

	broker.streams[stream] = struct{}{}
	broker.perTeam[applicationID]++
	broker.serving.Add(1)
	return stream, nil
}

/*
Publish delivers an event to every stream entitled to it.

It takes no context and returns no error, deliberately. Announcing what
happened must not be able to slow down, fail, or cancel the operation that
happened: a domain service calls this and carries on. A subscriber that cannot
keep up is ended here, which is the only way a full queue can be handled
without either blocking the publisher or silently discarding an event and
leaving the subscriber believing it saw everything.
*/
func (broker *Broker) Publish(event Event) {
	broker.mutex.Lock()
	defer broker.mutex.Unlock()

	if broker.stopped {
		return
	}

	var behind []*Stream
	for stream := range broker.streams {
		if stream.applicationID != event.ApplicationID || !stream.Wants(event.Type) {
			continue
		}

		select {
		case stream.events <- event:
			stream.delivered.Add(1)
		default:
			behind = append(behind, stream)
		}
	}

	for _, stream := range behind {
		broker.endLocked(stream, EndedBehind)
	}
}

/*
Stop ends every open stream, refuses new ones, and waits for the subscribers to
let go.

It exists because a hijacked connection is invisible to http.Server.Shutdown:
the server neither tracks it nor waits for it, so without this an instance
would close its listener and then exit while subscribers were still holding
sockets that nobody had told anything. Waiting is what makes the difference
between a subscriber reading "this instance is shutting down" and a subscriber
watching its connection disappear.

The wait is bounded by the caller's context, because a subscriber's socket is
not something a shutdown should be able to hang on. Whether every stream
finished is reported, so the caller can say which of the two happened.
*/
func (broker *Broker) Stop(ctx context.Context) bool {
	broker.mutex.Lock()
	broker.stopped = true
	for _, stream := range slices.Collect(maps.Keys(broker.streams)) {
		broker.endLocked(stream, EndedByShutdown)
	}
	broker.mutex.Unlock()

	/*
		Released outside the lock: a subscriber letting go takes the same lock,
		so waiting while holding it would be waiting for something this call is
		blocking.
	*/
	released := make(chan struct{})
	go func() {
		/*
			This goroutine ends when the last subscriber lets go, which happens
			whether or not the wait below is still listening. Nothing new can
			subscribe, so the count only falls.
		*/
		broker.serving.Wait()
		close(released)
	}()

	select {
	case <-released:
		return true
	case <-ctx.Done():
		return false
	}
}

// Active reports how many streams are open, for the summary an operator sees.
func (broker *Broker) Active() int {
	broker.mutex.Lock()
	defer broker.mutex.Unlock()
	return len(broker.streams)
}

// end removes a stream from the broker and records why it finished.
func (broker *Broker) end(stream *Stream, ending Ending) {
	broker.mutex.Lock()
	defer broker.mutex.Unlock()
	broker.endLocked(stream, ending)
}

/*
endLocked releases a stream while the broker's lock is held.

The event channel is never closed. A subscriber learns the stream finished from
Done, and leaving the channel open means a publish racing with an ending cannot
send on a closed channel — the stream is out of the map before this returns,
and every send happens under the same lock.
*/
func (broker *Broker) endLocked(stream *Stream, ending Ending) {
	if _, open := broker.streams[stream]; !open {
		return
	}

	delete(broker.streams, stream)
	if remaining := broker.perTeam[stream.applicationID] - 1; remaining > 0 {
		broker.perTeam[stream.applicationID] = remaining
	} else {
		delete(broker.perTeam, stream.applicationID)
	}

	stream.once.Do(func() {
		stream.ending.Store(int32(ending))
		close(stream.done)
	})
}
