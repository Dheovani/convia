/*
Package redis carries Convia's control events between the instances of one
deployment.

It is the first and only thing Convia uses Redis for, and the fit is exact
rather than convenient. `M16-001` asks for a documented use case before the
dependency is added; `M14-014` wrote it down before this package existed: the
event broker is in-process, so a subscriber connected to one instance never
sees an event produced on another.

**Publish/subscribe stores nothing.** A message nobody is listening for is gone
the instant it is sent, which is the strongest possible answer to this
milestone's own exit criterion — Redis here cannot become an accidental durable
authority, because there is nothing durable to become one with. There are no
keys, so there is nothing to expire and nothing for an eviction policy to
reclaim.

That also settles what this must never be used for. Anything a consumer must
not miss belongs in a webhook, which is a row in PostgreSQL with attempts
behind it; anything Convia must be able to answer belongs in PostgreSQL
outright. What travels here is a copy of something already recorded.

Nothing outside the composition root imports this package, and a test in
internal/events enforces it — the same containment the media provider has.
*/
package redis

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"convia/internal/events"
)

const (
	/*
		Channel is where the instances of one deployment talk to each other.

		The name carries a namespace and a version, which is the convention
		`M16-005` asks for. `convia:` says whose it is on a Redis somebody else
		may also be using, and `v1:` is what lets a future change to the
		envelope run beside this one during a rolling deployment rather than
		being read by an instance that cannot understand it.

		It is one channel for the whole deployment rather than one per tenant.
		Which subscriber receives what is decided by the broker from the
		credential that opened the stream, and it is decided the same way
		whether the event arrived from this instance or another — so a channel
		per tenant would buy less traffic between machines that already share a
		database, at the cost of subscribing and unsubscribing as streams come
		and go. docs/events.md records that as the optimization to make when
		the traffic justifies it.
	*/
	Channel = "convia:v1:events"

	/*
		envelopeVersion is the shape of what travels on the channel.

		It is separate from the event's own version because the two can differ:
		this one describes the transport between instances, and the other
		describes what a client reads.
	*/
	envelopeVersion = 1

	/*
		queueDepth is how many events may be waiting to be carried before the
		relay starts dropping them.

		The queue exists so that publishing never waits for a network round
		trip. Its depth is a judgement about a burst rather than a backlog: an
		instance this far behind on a channel that stores nothing has a broken
		connection rather than a slow one, and the honest response is to say so
		and keep serving.
	*/
	queueDepth = 1024
)

/*
Relay carries events over Redis publish/subscribe.

It satisfies [convia/internal/events.Relay], whose contract is the reason for
the queue: broadcasting must never block or fail the operation that produced
the event.
*/
type Relay struct {
	client *goredis.Client
	logger *slog.Logger

	/*
		origin identifies this process, so that the copy of an event coming back
		on the channel Convia itself published to can be recognized and dropped.
		Without it every event would be delivered twice to this instance's
		subscribers: once directly, and once through Redis.
	*/
	origin string

	timeout time.Duration

	queued   chan events.Event
	received chan events.Event

	stopping chan struct{}
	stopped  sync.Once
	running  sync.WaitGroup

	/*
		subscribed is closed once Redis has confirmed this relay's subscription
		to the shared channel.

		Nothing in production waits on it, because New deliberately does not
		wait for Redis at all. It exists because "this relay exists" and "this
		relay receives" are two different facts, and anything that needs the
		second one has no other way to learn it: pub/sub delivers to the
		subscribers that exist at the moment of the publish, so a publish sent
		into the gap between them reaches nobody and reports success.
	*/
	subscribed chan struct{}

	dropped atomic.Int64
}

// Config is what the composition root passes to reach the shared channel.
type Config struct {
	// URL is the redis:// or rediss:// address of the shared instance.
	URL string
	// Timeout bounds one operation against it.
	Timeout time.Duration
	// Origin identifies this process among the instances of the deployment.
	Origin string
}

/*
carried is the envelope on the wire.

An event travels inside a wrapper rather than alone because the relay has one
thing to say that the event does not: which instance sent it.
*/
type carried struct {
	Version int          `json:"version"`
	Origin  string       `json:"origin"`
	Event   events.Event `json:"event"`
}

/*
New opens the relay and starts carrying events.

It does not wait for Redis to answer, deliberately. An instance that refused to
start because the shared channel was unreachable would take a working API
offline over a stream that degrades to exactly what it was before this package
existed. The connection is established and re-established underneath; what a
failure costs is visible in the log and in the count of what could not be
carried.
*/
func New(settings Config, logger *slog.Logger) (*Relay, error) {
	options, err := goredis.ParseURL(settings.URL)
	if err != nil {
		return nil, fmt.Errorf("read the Redis URL: %w", err)
	}

	timeout := settings.Timeout
	if timeout <= 0 {
		timeout = 3 * time.Second
	}

	options.DialTimeout = timeout
	options.ReadTimeout = timeout
	options.WriteTimeout = timeout

	relay := &Relay{
		client:   goredis.NewClient(options),
		logger:   logger,
		origin:   settings.Origin,
		timeout:  timeout,
		queued:     make(chan events.Event, queueDepth),
		received:   make(chan events.Event, queueDepth),
		stopping:   make(chan struct{}),
		subscribed: make(chan struct{}),
	}

	relay.running.Add(2)
	go relay.carry()
	go relay.listen()

	return relay, nil
}

/*
Broadcast queues an event for the other instances.

It never blocks, which is the whole of its contract. A full queue means the
connection is broken rather than busy, so the event is dropped and counted: the
alternative is holding up the request that produced it, and this is the
best-effort half of Convia's delivery — the durable half is webhooks.
*/
func (relay *Relay) Broadcast(event events.Event) {
	select {
	case relay.queued <- event:
	default:
		/*
			Reported occasionally rather than every time. Whatever produces one
			of these produces thousands, and a log that repeats itself at that
			rate buries the line an operator is looking for.
		*/
		if dropped := relay.dropped.Add(1); dropped == 1 || dropped%100 == 0 {
			relay.logger.Error("events are not reaching the other instances",
				"dropped", dropped,
				"remedy", "check that CONVIA_REDIS_URL is reachable")
		}
	}
}

/*
Events yields what the other instances published.

The composition root reads this and hands each one to the broker, which is what
keeps this package out of the broker's dependencies: the relay knows how to
carry an event and nothing about who is entitled to it.
*/
func (relay *Relay) Events() <-chan events.Event { return relay.received }

// Dropped counts the events that could not be carried, for the summary an
// operator sees when the process stops.
func (relay *Relay) Dropped() int64 { return relay.dropped.Load() }

/*
Close stops carrying events and releases the connection.

It is called once, by the composition root, as the process shuts down. Calling
it twice is safe, so a shutdown path can defer it without knowing whether the
error path already ran.
*/
func (relay *Relay) Close() error {
	relay.stopped.Do(func() { close(relay.stopping) })
	relay.running.Wait()
	return relay.client.Close()
}

/*
carry publishes queued events until the relay stops.

One goroutine, owned by the relay, ended by Close. Publishing here rather than
in Broadcast is what makes Broadcast unable to block.
*/
func (relay *Relay) carry() {
	defer relay.running.Done()

	for {
		select {
		case <-relay.stopping:
			return
		case event := <-relay.queued:
			relay.publish(event)
		}
	}
}

// publish sends one event to the other instances.
func (relay *Relay) publish(event events.Event) {
	body, err := json.Marshal(carried{
		Version: envelopeVersion,
		Origin:  relay.origin,
		Event:   event,
	})
	if err != nil {
		relay.logger.Error("render an event for the other instances",
			"error", err, "event_id", event.ID)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), relay.timeout)
	defer cancel()

	if err := relay.client.Publish(ctx, Channel, body).Err(); err != nil {
		relay.logger.Error("carry an event to the other instances",
			"error", err, "event_id", event.ID, "event", string(event.Type))
	}
}

/*
listen receives what the other instances published.

go-redis re-establishes the subscription underneath, so a Redis that goes away
and comes back leaves a gap in what this instance's subscribers saw rather than
a stream that never recovers. That gap is the cost of the design, and it is the
same one a subscriber already accepts by using a stream at all.
*/
func (relay *Relay) listen() {
	defer relay.running.Done()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	subscription := relay.client.Subscribe(ctx, Channel)
	defer subscription.Close()

	/*
		Receive blocks until Redis confirms the subscription, which is the
		documented way to tell "asked to subscribe" apart from "is subscribed".
		A failure here is not fatal and is not even reported: the connection is
		re-established underneath, and refusing to carry events because the
		first attempt did not land is the behaviour New exists to avoid. What a
		failure costs is that `subscribed` stays open, so nothing is told a
		subscription is live when it is not.
	*/
	if _, err := subscription.Receive(ctx); err == nil {
		close(relay.subscribed)
	}

	incoming := subscription.Channel()

	for {
		select {
		case <-relay.stopping:
			return
		case message, open := <-incoming:
			if !open {
				return
			}
			relay.accept(message.Payload)
		}
	}
}

/*
accept turns one message into an event, or explains why it did not.

An unreadable message is logged and skipped rather than retried. Whatever
produced it — a newer version, another product sharing the Redis, a truncated
write — will produce the next one too, and stopping the relay over it would
cost every instance its stream.
*/
func (relay *Relay) accept(payload string) {
	var message carried
	if err := json.Unmarshal([]byte(payload), &message); err != nil {
		relay.logger.Warn("a message on the shared channel could not be read",
			"error", err, "bytes", len(payload))
		return
	}

	if message.Version != envelopeVersion {
		relay.logger.Warn("a message on the shared channel is a version this instance does not know",
			"version", message.Version, "known", envelopeVersion)
		return
	}

	/*
		Convia's own copy comes back on the channel it published to, because
		Redis delivers to every subscriber including the publisher. Dropping it
		here is what stops every event reaching this instance's subscribers
		twice.
	*/
	if message.Origin == relay.origin {
		return
	}

	select {
	case relay.received <- message.Event:
	case <-relay.stopping:
	default:
		relay.logger.Error("events from the other instances are arriving faster than they are delivered",
			"event_id", message.Event.ID)
	}
}

/*
Ping reports whether the shared channel is reachable.

Nothing depends on it, and readiness deliberately does not: an instance whose
stream has narrowed to its own subscribers is still serving every request
correctly. It exists so the composition root can say at startup whether this
process reached the others, which is the difference between a deployment that
is configured and one that only looks it.
*/
func (relay *Relay) Ping(ctx context.Context) error {
	if err := relay.client.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("reach the shared channel: %w", err)
	}
	return nil
}

/*
Redacted reports an address that is safe to log.

A Redis URL routinely carries a password, and an operator still needs to see
which host an instance was pointed at.
*/
func Redacted(rawURL string) string {
	options, err := goredis.ParseURL(rawURL)
	if err != nil {
		return "[unparseable]"
	}

	if options.Password != "" {
		return "[redacted]@" + options.Addr
	}
	return options.Addr
}
