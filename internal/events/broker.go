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
		PersonQueueDepth is the same allowance for a person's stream, and it is
		smaller on purpose.

		An application's stream carries its whole tenant; a person's carries
		the rooms they are in. There are also many more people than backends,
		and a queue is allocated in full when a stream opens, so the depth is
		multiplied by every signed-in tab. Sixty-four is still more than one
		person reads in the seconds a pause lasts.
	*/
	PersonQueueDepth = 64

	/*
		MaxStreamsPerApplication bounds one tenant's concurrent streams.

		Every instance of an application's backend needs one stream, not one
		per user, because the stream carries the whole tenant's events. Eight
		leaves room for a rolling deployment holding old and new instances at
		once, and still bounds what a single credential can hold open.
	*/
	MaxStreamsPerApplication = 8

	/*
		MaxStreamsPerPerson bounds one person's concurrent streams.

		A browser opens one per tab, and a person may hold ten sessions at once
		(sessions.MaxPerAccount). Sixteen allows several tabs across a few
		devices and still bounds what one stolen cookie can hold open.
	*/
	MaxStreamsPerPerson = 16

	/*
		MaxStreams bounds the application streams on this instance.

		It exists so that many tenants cannot together do what one tenant is
		already stopped from doing. Reaching it is answered the same way as
		reaching the per-application ceiling, so a tenant is never told
		anything about the instance's total load.
	*/
	MaxStreams = 1024

	/*
		MaxPersonStreams bounds the people's streams on this instance, apart
		from the applications'.

		They are counted separately so that people signing in cannot crowd out
		the backends of every other tenant: a thousand open tabs on Convia's
		own product would otherwise refuse every application its stream on this
		instance. At the smaller queue depth, the whole allowance is a few tens
		of megabytes held at rest.
	*/
	MaxPersonStreams = 4096
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

It holds nothing: an event is delivered to the streams open at the moment it is
published, and is then gone. That is the whole design, and its consequence is
stated rather than hidden — an event produced while a subscriber is
disconnected is not waiting for them when they return.

Which streams "open" means depends on how the deployment is put together. On
its own, a broker knows only this instance's. Given a [Relay] it also reaches
the subscribers connected to the others, which is what M16 added and what
docs/events.md describes.

Publishing never blocks and never fails, which is what keeps a slow reader from
becoming a slow API. A subscriber that cannot keep up loses its stream rather
than delaying the request that produced the event.
*/
type Broker struct {
	mutex   sync.Mutex
	streams map[*Stream]struct{}
	stopped bool

	/*
		The ceilings' counts, one pair for applications and one for people.
		They are kept rather than derived from the map above so that opening a
		stream costs a lookup rather than a walk over every other stream.
	*/
	applications int
	perTeam      map[string]int
	people       int
	perPerson    map[string]int

	/*
		serving counts the goroutines that hold a stream, so that stopping can
		wait for them rather than hoping. It is separate from the map above:
		a stream is out of the map the moment it ends, while the goroutine
		serving it still has a close frame to write.
	*/
	serving sync.WaitGroup

	/*
		relay carries events to and from the other instances of this
		deployment, and is nil when there is only one. A nil relay is a
		supported configuration rather than a degraded one: a single instance
		needs nothing carried anywhere.
	*/
	relay Relay
}

// NewBroker returns a broker that serves the subscribers of this instance
// alone, which is every deployment running one.
func NewBroker() *Broker {
	return &Broker{
		streams:   make(map[*Stream]struct{}),
		perTeam:   make(map[string]int),
		perPerson: make(map[string]int),
	}
}

/*
NewSharedBroker returns a broker whose events also reach the subscribers
connected to other instances.

What changes is only where an event goes, never what a subscriber is entitled
to: the relay carries events between instances of one deployment, and which of
them a given stream receives is still decided here, from the credential that
opened it.
*/
func NewSharedBroker(relay Relay) *Broker {
	broker := NewBroker()
	broker.relay = relay
	return broker
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

	/*
		person is who the stream is for when a person opened it, and nil when
		an application did. It is set when the stream opens and never replaced,
		so reading the pointer needs no lock; what it points at changes, and
		only under the broker's.
	*/
	person *audience

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
Reconcile replaces the rooms a person's stream covers with a fresh read of them.

The read happens outside the broker's lock, because it is a query, and a
membership can change while it runs. So the changes about this person that
arrive meanwhile are kept and applied on top of what was read. Each one states
whether the person is in a room, rather than a difference, so applying them in
order onto a read taken at any moment in between gives the state after the
last of them — whichever side of the read each one actually committed on.

A read that fails leaves what the stream already covered in place and reports
the error. The previous answer is stale by at most one interval, and a stream
that covered nothing at all would look exactly like a quiet one.

It may only be called on a stream a person opened, by the goroutine serving it.
*/
func (stream *Stream) Reconcile(read func() ([]string, error)) error {
	person := stream.person
	if person == nil {
		panic("events: Reconcile called on an application's stream, which covers a tenant rather than rooms")
	}

	stream.broker.mutex.Lock()
	person.reading = true
	person.changes = nil
	stream.broker.mutex.Unlock()

	identifiers, err := read()

	stream.broker.mutex.Lock()
	defer stream.broker.mutex.Unlock()

	changes := person.changes
	person.reading = false
	person.changes = nil

	if err != nil {
		return err
	}

	rooms := make(map[string]struct{}, len(identifiers))
	for _, roomID := range identifiers {
		rooms[roomID] = struct{}{}
	}
	for _, change := range changes {
		if change.member {
			rooms[change.roomID] = struct{}{}
		} else {
			delete(rooms, change.roomID)
		}
	}
	person.rooms = rooms
	return nil
}

/*
outgoing is an event as this subscriber receives it.

A person is not told which request caused something. The correlation
identifier is another person's request, and it exists to be matched against an
access log only an operator reads — so it is of no use to a person, and it is
not Convia's to hand one person about another.
*/
func (stream *Stream) outgoing(event Event) Event {
	if stream.person != nil {
		event.CorrelationID = ""
	}
	return event
}

/*
audience is whom a person's stream is for, and which rooms it covers right now.

It is what makes a person's stream authorized per room rather than per tenant:
an event reaches it only when it names a room the person is in. The rooms are
held here rather than asked about per event, because an event is delivered
under the broker's lock to every stream at once, and a query there would put
the database inside publishing — which must not be able to block.
*/
type audience struct {
	userID string
	rooms  map[string]struct{}

	// reading is true while [Stream.Reconcile] is reading the person's rooms,
	// and changes holds what arrived about them meanwhile.
	reading bool
	changes []membership
}

// membership is one change to whether a person is in a room.
type membership struct {
	roomID string
	member bool
}

/*
admits reports whether an event reaches this person, and keeps their rooms
current as it goes.

A change to the person's own membership is delivered if they were in the room
before it **or** are in it after. Being added is news to somebody who was not
there yet, and being removed is news to somebody who no longer is; judging
either by one side alone would withhold exactly the event the person needs.

It is called with the broker's lock held.
*/
func (person *audience) admits(event Event) bool {
	roomID, scoped := roomOf(event)
	if !scoped {
		return false
	}

	_, before := person.rooms[roomID]
	if !about(event, person.userID) {
		return before
	}

	member := event.Type == MemberAdded
	if member {
		person.rooms[roomID] = struct{}{}
	} else {
		delete(person.rooms, roomID)
	}
	if person.reading {
		person.changes = append(person.changes, membership{roomID: roomID, member: member})
	}
	return before || member
}

/*
roomOf names the room an event happened in, when it happened in one.

Only the types a person's stream carries are named here. Anything else is not
about a room as far as a person is concerned, so it reaches nobody through this
path, which is the answer that fails closed when a type is added.
*/
func roomOf(event Event) (string, bool) {
	switch event.Type {
	case MemberAdded, MemberRemoved:
		return event.Subject.ID, event.Subject.ID != ""
	case MessagePosted, MessageEdited, MessageDeleted,
		CallStarted, CallEnded,
		ParticipantJoined, ParticipantLeft, ParticipantRemoved, ParticipantRoleChanged:
		roomID, named := event.Data["room_id"].(string)
		return roomID, named && roomID != ""
	default:
		return "", false
	}
}

// about reports whether an event changes one person's own membership.
func about(event Event, userID string) bool {
	if event.Type != MemberAdded && event.Type != MemberRemoved {
		return false
	}
	named, _ := event.Data["user_id"].(string)
	return named == userID
}

/*
Subscribe opens a stream of the named event types for one application.

The types are settled here, at the point where the caller's authority is
already known, rather than being chosen by the client over the connection. A
subscriber therefore cannot widen what it receives after connecting, and there
is no message it could send that would try.
*/
func (broker *Broker) Subscribe(applicationID string, types []Type) (*Stream, error) {
	return broker.open(applicationID, types, nil)
}

/*
subscribePerson opens a stream for one of an application's people.

It covers no rooms until [Stream.Reconcile] says which, so a stream that is
opened and never reconciled carries nothing rather than everything.
*/
func (broker *Broker) subscribePerson(applicationID, userID string, types []Type) (*Stream, error) {
	return broker.open(applicationID, types, &audience{userID: userID, rooms: make(map[string]struct{})})
}

func (broker *Broker) open(applicationID string, types []Type, person *audience) (*Stream, error) {
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

	depth := QueueDepth
	if person == nil {
		if broker.applications >= MaxStreams || broker.perTeam[applicationID] >= MaxStreamsPerApplication {
			return nil, ErrTooManyStreams
		}
	} else {
		if broker.people >= MaxPersonStreams || broker.perPerson[person.userID] >= MaxStreamsPerPerson {
			return nil, ErrTooManyStreams
		}
		depth = PersonQueueDepth
	}

	stream := &Stream{
		broker:        broker,
		applicationID: applicationID,
		wanted:        wanted,
		person:        person,
		events:        make(chan Event, depth),
		done:          make(chan struct{}),
	}

	broker.streams[stream] = struct{}{}
	broker.count(stream, 1)
	broker.serving.Add(1)
	return stream, nil
}

// count moves a stream's ceilings by one in either direction. It is called with
// the broker's lock held.
func (broker *Broker) count(stream *Stream, delta int) {
	total, perKey, key := &broker.applications, broker.perTeam, stream.applicationID
	if stream.person != nil {
		total, perKey, key = &broker.people, broker.perPerson, stream.person.userID
	}

	*total += delta
	if remaining := perKey[key] + delta; remaining > 0 {
		perKey[key] = remaining
	} else {
		delete(perKey, key)
	}
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
	broker.deliver(event)

	/*
		Carried to the other instances after this one's subscribers have it, so
		that a slow or unreachable relay cannot delay the delivery it was meant
		to widen. Broadcast is non-blocking, which is what makes that ordering
		free rather than a compromise.
	*/
	if broker.relay != nil {
		broker.relay.Broadcast(event)
	}
}

/*
Receive delivers an event produced on another instance.

It is the relay's way in, and it deliberately does not broadcast: an event that
arrived from elsewhere has already been everywhere, and sending it on would be
a loop between two instances that each thought the other needed telling.
*/
func (broker *Broker) Receive(event Event) {
	broker.deliver(event)
}

// deliver hands an event to this instance's entitled subscribers.
func (broker *Broker) deliver(event Event) {
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
		if stream.person != nil && !stream.person.admits(event) {
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
	broker.count(stream, -1)

	stream.once.Do(func() {
		stream.ending.Store(int32(ending))
		close(stream.done)
	})
}
