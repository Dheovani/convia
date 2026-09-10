package redis

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"convia/internal/events"
)

/*
testRedisURLEnvironment points these tests at a Redis instance.

They are skipped when it is unset, so `go test ./...` stays runnable without
infrastructure, and CI sets it so the coverage is never optional where it
matters.
*/
const testRedisURLEnvironment = "CONVIA_TEST_REDIS_URL"

// waitFor is how long a test waits for something that should already have
// happened, before deciding it never will.
const waitFor = 5 * time.Second

func quiet() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

/*
connected opens a relay against the test Redis.

Each one gets its own origin, because that is what distinguishes two instances
of a deployment from one instance talking to itself.
*/
func connected(t *testing.T, origin string) *Relay {
	t.Helper()

	address := strings.TrimSpace(os.Getenv(testRedisURLEnvironment))
	if address == "" {
		t.Skipf("set %s to run the relay integration tests", testRedisURLEnvironment)
	}

	relay, err := New(Config{URL: address, Timeout: 2 * time.Second, Origin: origin}, quiet())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	t.Cleanup(func() { relay.Close() })

	// The subscription is established asynchronously, so a publish sent before
	// it exists would be delivered to nobody — which is what pub/sub means.
	waitUntilSubscribed(t, relay)
	return relay
}

/*
waitUntilSubscribed waits until Redis reports a subscriber on the channel.

Without it these tests would race the subscription rather than the behaviour
they are about, and would fail occasionally for a reason that has nothing to do
with Convia.
*/
func waitUntilSubscribed(t *testing.T, relay *Relay) {
	t.Helper()

	deadline := time.Now().Add(waitFor)
	for time.Now().Before(deadline) {
		counts, err := relay.client.PubSubNumSub(context.Background(), Channel).Result()
		if err == nil && counts[Channel] > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the relay never subscribed to the shared channel")
}

// next takes the event a relay should already have received.
func next(t *testing.T, relay *Relay) events.Event {
	t.Helper()

	select {
	case event := <-relay.Events():
		return event
	case <-time.After(waitFor):
		t.Fatal("nothing arrived from the other instance")
		return events.Event{}
	}
}

// silence fails when anything arrives.
func silence(t *testing.T, relay *Relay) {
	t.Helper()

	select {
	case event := <-relay.Events():
		t.Errorf("an event arrived that should not have: %s about %s", event.Type, event.Subject.ID)
	case <-time.After(200 * time.Millisecond):
	}
}

/*
TestAnEventReachesTheOtherInstance is what the whole package exists for, over a
real Redis.

Two relays are two instances of one deployment. What one publishes, the other
receives, which is the gap M14 documented and could not close on its own.
*/
func TestAnEventReachesTheOtherInstance(t *testing.T) {
	first := connected(t, "ins_first")
	second := connected(t, "ins_second")

	event := events.New(events.CallStarted, "app_1", "call_1", "req_1",
		events.Data{"room_id": "room_1", "status": "active"})
	first.Broadcast(event)

	received := next(t, second)
	if received.ID != event.ID {
		t.Errorf("the other instance received %q", received.ID)
	}
	if received.Type != events.CallStarted {
		t.Errorf("the other instance received a %q", received.Type)
	}
	if received.ApplicationID != "app_1" {
		t.Errorf("the event arrived belonging to %q", received.ApplicationID)
	}
	if received.Subject.Type != events.SubjectCall || received.Subject.ID != "call_1" {
		t.Errorf("the subject arrived as %+v", received.Subject)
	}
	if received.CorrelationID != "req_1" {
		t.Errorf("the cause arrived as %q", received.CorrelationID)
	}
	if received.Data["room_id"] != "room_1" {
		t.Errorf("the data arrived as %v", received.Data)
	}
}

/*
TestAnInstanceDoesNotReceiveItsOwnEvents is the reason the envelope names its
sender.

Redis delivers a published message to every subscriber, including the one that
published it. Without this, every event would reach its own instance's
subscribers twice: once directly from the broker, and once round-tripped.
*/
func TestAnInstanceDoesNotReceiveItsOwnEvents(t *testing.T) {
	relay := connected(t, "ins_alone")

	relay.Broadcast(events.New(events.CallStarted, "app_1", "call_1", "", nil))
	silence(t, relay)
}

/*
TestTheEnvelopeSurvivesTheRoundTrip is a shape test, and it earns its place
because the wire is the one place a field can go missing silently.

An event that lost its correlation identifier or its data on the way between
instances would look perfectly normal to the subscriber that received it.
*/
func TestTheEnvelopeSurvivesTheRoundTrip(t *testing.T) {
	first := connected(t, "ins_first")
	second := connected(t, "ins_second")

	sent := events.New(events.ParticipantRemoved, "app_1", "part_1", "req_1", events.Data{
		"call_id":    "call_1",
		"role":       "member",
		"status":     "removed",
		"guest":      false,
		"removed_by": "application",
	})
	first.Broadcast(sent)

	received := next(t, second)

	original, err := json.Marshal(sent)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	arrived, err := json.Marshal(received)
	if err != nil {
		t.Fatalf("Marshal() the received event error = %v", err)
	}

	if string(original) != string(arrived) {
		t.Errorf("the event changed on the way:\n sent %s\n got  %s", original, arrived)
	}
}

/*
TestAMessageThisInstanceCannotReadIsSkipped keeps one bad message from costing
every subscriber its stream.

Something else may share the Redis, a newer instance may publish a shape this
one does not know, and a write may be truncated. None of those is a reason to
stop carrying events.
*/
func TestAMessageThisInstanceCannotReadIsSkipped(t *testing.T) {
	relay := connected(t, "ins_listener")
	other := connected(t, "ins_publisher")

	ctx := context.Background()

	// Not JSON at all.
	if err := other.client.Publish(ctx, Channel, "definitely not json").Err(); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}

	// A version this instance does not know.
	future, err := json.Marshal(map[string]any{
		"version": envelopeVersion + 1,
		"origin":  "ins_publisher",
		"event":   events.New(events.CallStarted, "app_1", "call_1", "", nil),
	})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if err := other.client.Publish(ctx, Channel, future).Err(); err != nil {
		t.Fatalf("Publish() the future error = %v", err)
	}

	silence(t, relay)

	// And the relay is still working, which is the half that matters.
	event := events.New(events.CallEnded, "app_1", "call_1", "", nil)
	other.Broadcast(event)

	if got := next(t, relay).ID; got != event.ID {
		t.Errorf("after skipping bad messages the relay delivered %q", got)
	}
}

/*
TestBroadcastingNeverWaitsForRedis is the contract [convia/internal/events.Relay]
declares, checked against a Redis that is not there.

This is the property that makes the relay safe to call from inside a request.
If broadcasting could block, an unreachable Redis would not degrade the event
stream — it would slow down starting a call.
*/
func TestBroadcastingNeverWaitsForRedis(t *testing.T) {
	// Nothing is listening on this port, and New does not connect, so the
	// relay comes up and every publish behind it fails.
	relay, err := New(Config{
		URL:     "redis://127.0.0.1:6399/0",
		Timeout: 100 * time.Millisecond,
		Origin:  "ins_stranded",
	}, quiet())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer relay.Close()

	event := events.New(events.CallStarted, "app_1", "call_1", "", nil)

	started := time.Now()
	for range queueDepth * 2 {
		relay.Broadcast(event)
	}
	elapsed := time.Since(started)

	/*
		Twice the queue against a dead address. Whatever the network is doing,
		this loop is memory writes and channel sends: a second would already
		mean something was waiting.
	*/
	if elapsed > time.Second {
		t.Errorf("broadcasting %d events took %v against an unreachable Redis", queueDepth*2, elapsed)
	}
	if relay.Dropped() == 0 {
		t.Error("a full queue against an unreachable Redis dropped nothing, so something waited")
	}
}

/*
TestAnUnreachableRedisIsReportedRatherThanFatal covers M16-008 at the point an
operator meets it.

An instance that could not reach the shared channel keeps serving. Ping is what
the composition root uses to say so at startup, and it is deliberately not part
of readiness: a narrowed stream is not a reason to take an instance out of the
load balancer.
*/
func TestAnUnreachableRedisIsReportedRatherThanFatal(t *testing.T) {
	relay, err := New(Config{
		URL:     "redis://127.0.0.1:6399/0",
		Timeout: 200 * time.Millisecond,
		Origin:  "ins_stranded",
	}, quiet())
	if err != nil {
		t.Fatalf("New() refused to build a relay for an unreachable Redis: %v", err)
	}
	defer relay.Close()

	ctx, cancel := context.WithTimeout(context.Background(), waitFor)
	defer cancel()

	if err := relay.Ping(ctx); err == nil {
		t.Error("Ping() reported an unreachable Redis as reachable")
	}
}

/*
TestAMalformedAddressIsRefusedAtStartup is the one Redis failure that should
stop a process.

An address nobody can parse will never work, and a deployment that started
anyway would carry no events while looking configured.
*/
func TestAMalformedAddressIsRefusedAtStartup(t *testing.T) {
	if _, err := New(Config{URL: "not a redis url"}, quiet()); err == nil {
		t.Error("New() accepted an address it cannot connect to")
	}
}

/*
TestClosingIsSafeToRepeat lets a shutdown path defer it without knowing whether
an error path already ran.
*/
func TestClosingIsSafeToRepeat(t *testing.T) {
	relay, err := New(Config{
		URL:     "redis://127.0.0.1:6399/0",
		Timeout: 100 * time.Millisecond,
		Origin:  "ins_stranded",
	}, quiet())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if err := relay.Close(); err != nil {
		t.Errorf("Close() error = %v", err)
	}
	relay.Close()
}

/*
TestAnAddressIsLoggableWithoutItsPassword covers the value an operator actually
needs to see.

A Redis URL routinely carries a password, and the host is the part that answers
"which one did this instance connect to".
*/
func TestAnAddressIsLoggableWithoutItsPassword(t *testing.T) {
	withPassword := Redacted("redis://:hunter2@redis.internal:6379/0")

	if strings.Contains(withPassword, "hunter2") {
		t.Errorf("the redacted address carries the password: %s", withPassword)
	}
	if !strings.Contains(withPassword, "redis.internal:6379") {
		t.Errorf("the redacted address hides the host too: %s", withPassword)
	}

	if got := Redacted("redis://redis.internal:6379/0"); got != "redis.internal:6379" {
		t.Errorf("an address with no password rendered as %q", got)
	}
	if got := Redacted("nonsense"); got != "[unparseable]" {
		t.Errorf("an unreadable address rendered as %q", got)
	}
}

/*
TestTheChannelNameCarriesItsNamespaceAndVersion is M16-005 as a check rather
than a convention nobody applies.

The namespace is what keeps Convia's traffic recognizable on a Redis somebody
else is also using, and the version is what lets a change to the envelope run
beside this one during a rolling deployment.
*/
func TestTheChannelNameCarriesItsNamespaceAndVersion(t *testing.T) {
	if !strings.HasPrefix(Channel, "convia:") {
		t.Errorf("the channel %q does not say whose it is", Channel)
	}
	if !strings.Contains(Channel, ":v1:") {
		t.Errorf("the channel %q carries no version", Channel)
	}
}

/*
TestNothingIsWrittenToRedis is this milestone's exit criterion, checked rather
than asserted in prose.

"Redis cannot become an accidental durable authority" is a strong claim, and
the reason it holds is structural: publish/subscribe writes no keys. If a
future change starts storing something, this test is what notices.
*/
func TestNothingIsWrittenToRedis(t *testing.T) {
	first := connected(t, "ins_first")
	second := connected(t, "ins_second")

	before := keyCount(t, first.client)

	for range 20 {
		first.Broadcast(events.New(events.CallStarted, "app_1", "call_1", "", nil))
	}
	next(t, second)

	if after := keyCount(t, first.client); after != before {
		t.Errorf("carrying events left %d keys behind in Redis", after-before)
	}
}

// keyCount reports how many keys the test Redis holds.
func keyCount(t *testing.T, client *goredis.Client) int64 {
	t.Helper()

	size, err := client.DBSize(context.Background()).Result()
	if err != nil {
		t.Fatalf("DBSize() error = %v", err)
	}
	return size
}
