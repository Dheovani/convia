package webhooks

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"convia/internal/applications"
	"convia/internal/config"
	"convia/internal/database"
	"convia/internal/events"
)

/*
testDatabaseURLEnvironment points these tests at a PostgreSQL instance.

They are skipped when it is unset, so `go test ./...` stays runnable without
infrastructure, and CI always sets it so the coverage is never optional where
it matters.
*/
const testDatabaseURLEnvironment = "CONVIA_TEST_DATABASE_URL"

// fixture is a service under test together with two tenants to isolate.
type fixture struct {
	service      *Service
	store        *Store
	dispatcher   *Dispatcher
	applications *applications.Service
	pool         *pgxpool.Pool
	first        string
	second       string
	logs         *bytes.Buffer
}

func newFixture(t *testing.T) fixture {
	t.Helper()

	maintenanceURL := strings.TrimSpace(os.Getenv(testDatabaseURLEnvironment))
	if maintenanceURL == "" {
		t.Skipf("set %s to run the webhook integration tests", testDatabaseURLEnvironment)
	}

	name := "convia_test_" + strings.ToLower(rand.Text()[:16])
	execute(t, maintenanceURL, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize())
	t.Cleanup(func() {
		execute(t, maintenanceURL, "DROP DATABASE "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)")
	})

	parsed, err := url.Parse(maintenanceURL)
	if err != nil {
		t.Fatalf("parse %s: %v", testDatabaseURLEnvironment, err)
	}
	parsed.Path = "/" + name
	databaseURL := parsed.String()

	logs := &bytes.Buffer{}
	logger := slog.New(slog.NewJSONHandler(logs, nil))

	if err := database.Migrate(context.Background(), databaseURL, logger); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	pool, err := database.Open(context.Background(), config.Database{
		URL:            databaseURL,
		MaxConnections: 12,
		ConnectTimeout: 10 * time.Second,
		QueryTimeout:   5 * time.Second,
	}, logger)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(pool.Close)

	applicationService := applications.NewService(applications.NewStore(pool), logger)
	store := NewStore(pool)

	/*
		A development guard, because every receiver in these tests is on
		localhost. What production refuses is covered by destination_test.go,
		which needs no infrastructure at all.
	*/
	guard := NewDestinations(true)

	setup := fixture{
		service:      NewService(store, applicationService, guard, logger),
		store:        store,
		dispatcher:   NewDispatcher(store, guard, logger),
		applications: applicationService,
		pool:         pool,
		first:        newApplication(t, applicationService, "First Tenant"),
		second:       newApplication(t, applicationService, "Second Tenant"),
		logs:         logs,
	}

	logs.Reset()
	return setup
}

func execute(t *testing.T, databaseURL, statement string) {
	t.Helper()

	connection, err := pgx.Connect(context.Background(), databaseURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer connection.Close(context.Background())

	if _, err := connection.Exec(context.Background(), statement); err != nil {
		t.Fatalf("%s: %v", statement, err)
	}
}

func newApplication(t *testing.T, service *applications.Service, name string) string {
	t.Helper()

	application, err := service.Create(context.Background(), name)
	if err != nil {
		t.Fatalf("Create() application error = %v", err)
	}
	return application.ID
}

// register puts an endpoint in place, pointed at a receiver.
func (setup fixture) register(t *testing.T, applicationID, address string,
	types ...events.Type) (Endpoint, Secret) {
	t.Helper()

	if len(types) == 0 {
		types = events.Types()
	}

	endpoint, signing, err := setup.service.Register(context.Background(), applicationID,
		Registration{Name: "Receiver", URL: address, EventTypes: types})
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	return endpoint, signing
}

// announce queues one event, as a domain operation does on its way out.
func (setup fixture) announce(t *testing.T, applicationID string, kind events.Type) events.Event {
	t.Helper()

	event := events.New(kind, applicationID, "call_"+rand.Text(), "req_1",
		events.Data{"room_id": "room_1"})
	if err := setup.dispatcher.Enqueue(context.Background(), event); err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}
	return event
}

// deliveries reads what Convia recorded for a tenant.
func (setup fixture) deliveries(t *testing.T, applicationID string) []Delivery {
	t.Helper()

	page, err := setup.service.ListDeliveries(context.Background(), applicationID,
		DeliveryListOptions{})
	if err != nil {
		t.Fatalf("ListDeliveries() error = %v", err)
	}
	return page.Deliveries
}

/*
receiver is a webhook consumer, and it verifies what it is sent.

It is what `M15-015` asks for and it is also the honest test of `M15-004`: a
signature is only worth sending if a consumer written from the documentation
can check it. This one does exactly what docs/webhooks.md tells consumers to
do.
*/
type receiver struct {
	mutex     sync.Mutex
	received  []receivedDelivery
	status    int
	secret    Secret
	responder func(attempt int) int
}

type receivedDelivery struct {
	DeliveryID string
	EventType  string
	Attempt    string
	Signature  string
	Body       []byte
	Verified   bool
}

func (consumer *receiver) handler() func(response http.ResponseWriter, request *http.Request) {
	return func(response http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)

		consumer.mutex.Lock()
		verified := Verify(consumer.secret, request.Header.Get(SignatureHeader),
			body, time.Now().UTC(), tolerance)

		consumer.received = append(consumer.received, receivedDelivery{
			DeliveryID: request.Header.Get(DeliveryHeader),
			EventType:  request.Header.Get(EventHeader),
			Attempt:    request.Header.Get(AttemptHeader),
			Signature:  request.Header.Get(SignatureHeader),
			Body:       body,
			Verified:   verified,
		})

		status := consumer.status
		if consumer.responder != nil {
			status = consumer.responder(len(consumer.received))
		}
		consumer.mutex.Unlock()

		response.WriteHeader(status)
	}
}

func (consumer *receiver) deliveries() []receivedDelivery {
	consumer.mutex.Lock()
	defer consumer.mutex.Unlock()
	return append([]receivedDelivery(nil), consumer.received...)
}

// listening starts a consumer that answers with one status.
func listening(t *testing.T, status int) (*receiver, *httptest.Server) {
	t.Helper()

	consumer := &receiver{status: status}
	server := httptest.NewServer(http.HandlerFunc(consumer.handler()))
	t.Cleanup(server.Close)

	return consumer, server
}

/*
TestARegisteredDestinationIsToldWhatHappened is the milestone's whole promise,
end to end against a real consumer.

An application registers somewhere to be reached, something happens, and the
delivery arrives — signed, identified, and verifiable by a consumer that wrote
its check from the documentation.
*/
func TestARegisteredDestinationIsToldWhatHappened(t *testing.T) {
	setup := newFixture(t)

	consumer, server := listening(t, http.StatusOK)
	_, signing := setup.register(t, setup.first, server.URL)
	consumer.secret = signing

	event := setup.announce(t, setup.first, events.CallStarted)
	setup.dispatcher.pass(context.Background())

	received := consumer.deliveries()
	if len(received) != 1 {
		t.Fatalf("the consumer received %d deliveries, want 1", len(received))
	}

	if !received[0].Verified {
		t.Error("the consumer could not verify the signature Convia sent")
	}
	if received[0].EventType != string(events.CallStarted) {
		t.Errorf("the delivery announced %q", received[0].EventType)
	}
	if received[0].Attempt != "1" {
		t.Errorf("the first delivery says it is attempt %q", received[0].Attempt)
	}
	if !strings.Contains(string(received[0].Body), event.ID) {
		t.Errorf("the body does not carry the event: %s", received[0].Body)
	}

	recorded := setup.deliveries(t, setup.first)
	if len(recorded) != 1 {
		t.Fatalf("Convia recorded %d deliveries, want 1", len(recorded))
	}
	if recorded[0].Status != DeliveryDelivered {
		t.Errorf("the recorded delivery is %q", recorded[0].Status)
	}
	if recorded[0].ID != received[0].DeliveryID {
		t.Errorf("the header named %q and the record is %q",
			received[0].DeliveryID, recorded[0].ID)
	}
	if recorded[0].LastStatusCode != http.StatusOK {
		t.Errorf("the record says the destination answered %d", recorded[0].LastStatusCode)
	}
}

/*
TestOnlySubscribedEndpointsAreQueued is what makes a subscription a filter
rather than a label.

An endpoint that asked about calls is not woken up by every roster change in
the tenant, and one that asked about nothing it is being told is not queued at
all.
*/
func TestOnlySubscribedEndpointsAreQueued(t *testing.T) {
	setup := newFixture(t)

	_, server := listening(t, http.StatusOK)
	setup.register(t, setup.first, server.URL, events.CallStarted)

	setup.announce(t, setup.first, events.ParticipantJoined)
	if recorded := setup.deliveries(t, setup.first); len(recorded) != 0 {
		t.Fatalf("an unsubscribed event queued %d deliveries", len(recorded))
	}

	setup.announce(t, setup.first, events.CallStarted)
	if recorded := setup.deliveries(t, setup.first); len(recorded) != 1 {
		t.Errorf("a subscribed event queued %d deliveries, want 1", len(recorded))
	}
}

/*
TestATenantIsNeverToldAboutAnother is the isolation check, made where a
mistake would actually happen: the fan-out query.
*/
func TestATenantIsNeverToldAboutAnother(t *testing.T) {
	setup := newFixture(t)

	_, server := listening(t, http.StatusOK)
	setup.register(t, setup.first, server.URL)
	setup.register(t, setup.second, server.URL)

	setup.announce(t, setup.first, events.CallStarted)

	if recorded := setup.deliveries(t, setup.second); len(recorded) != 0 {
		t.Errorf("the second tenant was owed %d deliveries about the first", len(recorded))
	}
	if recorded := setup.deliveries(t, setup.first); len(recorded) != 1 {
		t.Errorf("the first tenant was owed %d deliveries, want 1", len(recorded))
	}
}

/*
TestATenantWithNoEndpointsCostsNothing is what makes queuing acceptable on the
path of every announced event.

There is nothing to assert about performance here, so what is asserted is the
shape it rests on: no rows, and no error.
*/
func TestATenantWithNoEndpointsCostsNothing(t *testing.T) {
	setup := newFixture(t)

	queued, err := setup.store.Enqueue(context.Background(),
		events.New(events.CallStarted, setup.first, "call_1", "", nil),
		[]byte(`{}`), time.Now().UTC())
	if err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}
	if queued != 0 {
		t.Errorf("a tenant with no endpoints was owed %d deliveries", queued)
	}
}

/*
TestADisabledEndpointIsSentNothing covers both directions of the switch, and
the queue in between.
*/
func TestADisabledEndpointIsSentNothing(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	consumer, server := listening(t, http.StatusOK)
	endpoint, signing := setup.register(t, setup.first, server.URL)
	consumer.secret = signing

	if _, err := setup.service.Disable(ctx, setup.first, endpoint.ID); err != nil {
		t.Fatalf("Disable() error = %v", err)
	}

	setup.announce(t, setup.first, events.CallStarted)
	setup.dispatcher.pass(ctx)

	if received := consumer.deliveries(); len(received) != 0 {
		t.Errorf("a disabled endpoint received %d deliveries", len(received))
	}
	if recorded := setup.deliveries(t, setup.first); len(recorded) != 0 {
		t.Errorf("a disabled endpoint was queued %d deliveries", len(recorded))
	}

	if _, err := setup.service.Enable(ctx, setup.first, endpoint.ID); err != nil {
		t.Fatalf("Enable() error = %v", err)
	}

	setup.announce(t, setup.first, events.CallStarted)
	setup.dispatcher.pass(ctx)

	if received := consumer.deliveries(); len(received) != 1 {
		t.Errorf("an endpoint enabled again received %d deliveries, want 1", len(received))
	}
}

/*
TestDisablingFinishesWhatWasQueued keeps the worker's index free of work that
is never going to happen.

A delivery left pending for an endpoint Convia has stopped delivering to would
sit there forever, and an application reading its deliveries would see it
described as outstanding.
*/
func TestDisablingFinishesWhatWasQueued(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	_, server := listening(t, http.StatusOK)
	endpoint, _ := setup.register(t, setup.first, server.URL)

	setup.announce(t, setup.first, events.CallStarted)
	if _, err := setup.service.Disable(ctx, setup.first, endpoint.ID); err != nil {
		t.Fatalf("Disable() error = %v", err)
	}

	recorded := setup.deliveries(t, setup.first)
	if len(recorded) != 1 {
		t.Fatalf("Convia recorded %d deliveries, want 1", len(recorded))
	}
	if recorded[0].Status != DeliveryFailed {
		t.Errorf("a delivery queued for a disabled endpoint is %q", recorded[0].Status)
	}
	if recorded[0].NextAttemptAt != nil {
		t.Error("a finished delivery is still scheduled")
	}
}

/*
TestARefusalIsNotRetriedAndAFailureIs is the retry policy, which is a decision
about what a status code means.

A destination answering `400` is refusing the request, and repeating it for two
hours would be Convia insisting. One answering `500` is having a problem, which
is what retries are for.
*/
func TestARefusalIsNotRetriedAndAFailureIs(t *testing.T) {
	cases := map[string]struct {
		status  int
		attempt int
		want    DeliveryStatus
	}{
		"a refusal is final":            {status: http.StatusBadRequest, want: DeliveryFailed},
		"not found is final":            {status: http.StatusNotFound, want: DeliveryFailed},
		"a redirect is not a delivery":  {status: http.StatusFound, want: DeliveryFailed},
		"a server error is retried":     {status: http.StatusInternalServerError, want: DeliveryPending},
		"too many requests is retried":  {status: http.StatusTooManyRequests, want: DeliveryPending},
		"a request timeout is retried":  {status: http.StatusRequestTimeout, want: DeliveryPending},
		"anything in the twos succeeds": {status: http.StatusAccepted, want: DeliveryDelivered},
	}

	for name, test := range cases {
		t.Run(name, func(t *testing.T) {
			setup := newFixture(t)

			_, server := listening(t, test.status)
			setup.register(t, setup.first, server.URL)

			setup.announce(t, setup.first, events.CallStarted)
			setup.dispatcher.pass(context.Background())

			recorded := setup.deliveries(t, setup.first)
			if len(recorded) != 1 {
				t.Fatalf("Convia recorded %d deliveries, want 1", len(recorded))
			}
			if recorded[0].Status != test.want {
				t.Errorf("a destination answering %d left the delivery %q, want %q",
					test.status, recorded[0].Status, test.want)
			}
			if recorded[0].LastStatusCode != test.status {
				t.Errorf("the record says the destination answered %d",
					recorded[0].LastStatusCode)
			}
		})
	}
}

/*
TestTheRetryScheduleIsTheOneDocumented is `M15-014`.

The schedule is fixed and without jitter precisely so that this can be
asserted: a schedule nobody can predict is a schedule nobody can test, and
docs/webhooks.md publishes these numbers to consumers.
*/
func TestTheRetryScheduleIsTheOneDocumented(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	_, server := listening(t, http.StatusInternalServerError)
	setup.register(t, setup.first, server.URL)
	setup.announce(t, setup.first, events.CallStarted)

	for attempt, wait := range backoff {
		before := time.Now().UTC()
		setup.dispatcher.pass(ctx)

		recorded := setup.deliveries(t, setup.first)
		if len(recorded) != 1 {
			t.Fatalf("Convia recorded %d deliveries, want 1", len(recorded))
		}
		if recorded[0].Attempts != attempt+1 {
			t.Fatalf("after %d passes the delivery has %d attempts", attempt+1, recorded[0].Attempts)
		}
		if recorded[0].Status != DeliveryPending {
			t.Fatalf("attempt %d left the delivery %q", attempt+1, recorded[0].Status)
		}
		if recorded[0].NextAttemptAt == nil {
			t.Fatal("a pending delivery is not scheduled")
		}

		/*
			The next attempt is the documented wait away. It is compared as a
			window rather than an instant because the clock moved while the
			attempt was being made.
		*/
		scheduled := recorded[0].NextAttemptAt.Sub(before)
		if scheduled < wait || scheduled > wait+10*time.Second {
			t.Errorf("attempt %d scheduled the next one in %v, want about %v",
				attempt+1, scheduled, wait)
		}

		// Made due again, so the next pass takes it without waiting.
		setup.makeDue(t, recorded[0].ID)
	}

	// One more attempt, which is the last one.
	setup.dispatcher.pass(ctx)

	recorded := setup.deliveries(t, setup.first)
	if recorded[0].Attempts != maxAttempts {
		t.Errorf("the delivery was tried %d times, want %d", recorded[0].Attempts, maxAttempts)
	}
	if recorded[0].Status != DeliveryFailed {
		t.Errorf("after every attempt the delivery is %q", recorded[0].Status)
	}
	if recorded[0].NextAttemptAt != nil {
		t.Error("a delivery Convia gave up on is still scheduled")
	}
}

/*
TestARetryTellsTheConsumerItIsOne is what makes duplicates survivable.

A consumer seeing an attempt above one knows Convia did not hear back, which is
usually the more useful half of a repeat.
*/
func TestARetryTellsTheConsumerItIsOne(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	// Fails once, then accepts, which is the ordinary shape of a hiccup.
	consumer := &receiver{responder: func(attempt int) int {
		if attempt == 1 {
			return http.StatusInternalServerError
		}
		return http.StatusOK
	}}
	server := httptest.NewServer(http.HandlerFunc(consumer.handler()))
	t.Cleanup(server.Close)

	_, signing := setup.register(t, setup.first, server.URL)
	consumer.secret = signing

	setup.announce(t, setup.first, events.CallStarted)
	setup.dispatcher.pass(ctx)

	recorded := setup.deliveries(t, setup.first)
	setup.makeDue(t, recorded[0].ID)
	setup.dispatcher.pass(ctx)

	received := consumer.deliveries()
	if len(received) != 2 {
		t.Fatalf("the consumer received %d deliveries, want 2", len(received))
	}
	if received[0].Attempt != "1" || received[1].Attempt != "2" {
		t.Errorf("the attempts arrived as %q and %q", received[0].Attempt, received[1].Attempt)
	}
	if received[0].DeliveryID != received[1].DeliveryID {
		t.Error("a retry carried a different delivery identifier, so a consumer could not recognize it")
	}
	if !received[1].Verified {
		t.Error("the retry's own signature did not verify")
	}
}

/*
TestAnEndpointThatStoppedWorkingIsDisabled is `M15-009`.

Continuing to deliver to a destination that has stopped answering costs Convia a
connection per event and costs the application nothing but a growing list of
failures.
*/
func TestAnEndpointThatStoppedWorkingIsDisabled(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	_, server := listening(t, http.StatusBadRequest)
	endpoint, _ := setup.register(t, setup.first, server.URL)

	// A refusal is terminal on the first attempt, so each event is one failure.
	for range failuresBeforeDisabling {
		setup.announce(t, setup.first, events.CallStarted)
		setup.dispatcher.pass(ctx)
	}

	after, err := setup.service.Get(ctx, setup.first, endpoint.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if after.Status != StatusDisabled {
		t.Errorf("an endpoint that failed %d times in a row is %q",
			failuresBeforeDisabling, after.Status)
	}
	if after.DisabledReason == "" {
		t.Error("a disabled endpoint does not say why")
	}
}

/*
TestASuccessForgivesAnEndpoint keeps disablement a statement about a
destination that has stopped working rather than one having a bad afternoon.
*/
func TestASuccessForgivesAnEndpoint(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	answer := http.StatusBadRequest
	consumer := &receiver{responder: func(int) int { return answer }}
	server := httptest.NewServer(http.HandlerFunc(consumer.handler()))
	t.Cleanup(server.Close)

	endpoint, _ := setup.register(t, setup.first, server.URL)

	for range 3 {
		setup.announce(t, setup.first, events.CallStarted)
		setup.dispatcher.pass(ctx)
	}

	failing, err := setup.service.Get(ctx, setup.first, endpoint.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if failing.ConsecutiveFailures != 3 {
		t.Fatalf("three failures counted as %d", failing.ConsecutiveFailures)
	}

	consumer.mutex.Lock()
	answer = http.StatusOK
	consumer.mutex.Unlock()

	setup.announce(t, setup.first, events.CallStarted)
	setup.dispatcher.pass(ctx)

	forgiven, err := setup.service.Get(ctx, setup.first, endpoint.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if forgiven.ConsecutiveFailures != 0 {
		t.Errorf("after a success the count is %d", forgiven.ConsecutiveFailures)
	}
}

/*
TestADeliveryTooOldIsNotDelivered is the maximum age of `M15-007`.

A webhook that arrives four hours after the conversation it describes has ended
is worse than none, because a consumer would act on it.
*/
func TestADeliveryTooOldIsNotDelivered(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	consumer, server := listening(t, http.StatusOK)
	setup.register(t, setup.first, server.URL)
	setup.announce(t, setup.first, events.CallStarted)

	recorded := setup.deliveries(t, setup.first)
	setup.age(t, recorded[0].ID, maxAge+time.Hour)

	setup.dispatcher.pass(ctx)

	if received := consumer.deliveries(); len(received) != 0 {
		t.Errorf("a delivery older than %v was still sent", maxAge)
	}

	after := setup.deliveries(t, setup.first)
	if after[0].Status != DeliveryFailed {
		t.Errorf("an expired delivery is %q", after[0].Status)
	}
	if !strings.Contains(after[0].LastError, "longer than Convia keeps trying") {
		t.Errorf("an expired delivery says %q", after[0].LastError)
	}
}

/*
TestTwoWorkersNeverSendTheSameDeliveryTwice is what the lease is for.

Convia is expected to run as more than one instance, and two of them draining
one queue must divide the work rather than duplicate it.
*/
func TestTwoWorkersNeverSendTheSameDeliveryTwice(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	_, server := listening(t, http.StatusOK)
	setup.register(t, setup.first, server.URL)

	for range 5 {
		setup.announce(t, setup.first, events.CallStarted)
	}

	now := time.Now().UTC()
	first, err := setup.store.Claim(ctx, 5, leaseDuration, now)
	if err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	if len(first) != 5 {
		t.Fatalf("the first worker claimed %d deliveries, want 5", len(first))
	}

	second, err := setup.store.Claim(ctx, 5, leaseDuration, now)
	if err != nil {
		t.Fatalf("Claim() again error = %v", err)
	}
	if len(second) != 0 {
		t.Errorf("a second worker claimed %d deliveries another already held", len(second))
	}

	/*
		Once the lease expires the work is available again, which is what stops
		a crashed worker from stranding its batch.
	*/
	afterLease, err := setup.store.Claim(ctx, 5, leaseDuration, now.Add(leaseDuration+time.Second))
	if err != nil {
		t.Fatalf("Claim() after the lease error = %v", err)
	}
	if len(afterLease) != 5 {
		t.Errorf("after the lease expired a worker claimed %d deliveries, want 5", len(afterLease))
	}
}

/*
TestASigningKeyIsShownOnceAndNeverRead is the promise registration makes.

Convia holds this key, because it signs with it. What "once" means is that no
read returns it — a property of every projection in the store, checked here
against the API rather than against the SQL.
*/
func TestASigningKeyIsShownOnceAndNeverRead(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	_, server := listening(t, http.StatusOK)
	endpoint, signing := setup.register(t, setup.first, server.URL)

	if signing.Reveal() == "" {
		t.Fatal("registering returned no signing key")
	}

	read, err := setup.service.Get(ctx, setup.first, endpoint.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if strings.Contains(sprint("%+v", read), signing.Reveal()) {
		t.Error("reading an endpoint returned its signing key")
	}
	if strings.Contains(setup.logs.String(), signing.Reveal()) {
		t.Error("the signing key is in the log")
	}
}

/*
TestRotatingReplacesTheKeyImmediately is the remedy for an exposed secret.

A grace period would keep the old key working for whoever exposed it, so there
is none: the next delivery is signed with the new one.
*/
func TestRotatingReplacesTheKeyImmediately(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	consumer, server := listening(t, http.StatusOK)
	endpoint, original := setup.register(t, setup.first, server.URL)

	_, rotated, err := setup.service.Rotate(ctx, setup.first, endpoint.ID)
	if err != nil {
		t.Fatalf("Rotate() error = %v", err)
	}
	if rotated.Reveal() == original.Reveal() {
		t.Fatal("rotating produced the same key")
	}

	setup.announce(t, setup.first, events.CallStarted)
	setup.dispatcher.pass(ctx)

	received := consumer.deliveries()
	if len(received) != 1 {
		t.Fatalf("the consumer received %d deliveries, want 1", len(received))
	}

	if !Verify(rotated, received[0].Signature, received[0].Body, time.Now().UTC(), tolerance) {
		t.Error("the delivery was not signed with the new key")
	}
	if Verify(original, received[0].Signature, received[0].Body, time.Now().UTC(), tolerance) {
		t.Error("the delivery was still signed with the old key")
	}
}

/*
TestAnotherTenantsEndpointIsMissingRatherThanForbidden keeps a listing from
confirming what exists.
*/
func TestAnotherTenantsEndpointIsMissingRatherThanForbidden(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	_, server := listening(t, http.StatusOK)
	endpoint, _ := setup.register(t, setup.first, server.URL)

	if _, err := setup.service.Get(ctx, setup.second, endpoint.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get() across tenants error = %v, want %v", err, ErrNotFound)
	}
	if err := setup.service.Delete(ctx, setup.second, endpoint.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("Delete() across tenants error = %v, want %v", err, ErrNotFound)
	}
}

/*
TestDeletingAnEndpointTakesItsDeliveries is the deliberate reading of deleting
a destination.

An application that wants the history disables instead, which the contract says
in as many words.
*/
func TestDeletingAnEndpointTakesItsDeliveries(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	_, server := listening(t, http.StatusOK)
	endpoint, _ := setup.register(t, setup.first, server.URL)
	setup.announce(t, setup.first, events.CallStarted)

	if len(setup.deliveries(t, setup.first)) != 1 {
		t.Fatal("the delivery was never queued")
	}

	if err := setup.service.Delete(ctx, setup.first, endpoint.ID); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if remaining := setup.deliveries(t, setup.first); len(remaining) != 0 {
		t.Errorf("%d deliveries outlived the endpoint they were owed to", len(remaining))
	}
}

// makeDue brings a scheduled delivery forward so a test does not wait for it.
func (setup fixture) makeDue(t *testing.T, deliveryID string) {
	t.Helper()

	if _, err := setup.pool.Exec(context.Background(),
		`UPDATE webhook_deliveries SET next_attempt_at = now() - interval '1 second'
         WHERE id = $1 AND status = 'pending'`, deliveryID); err != nil {
		t.Fatalf("bring a delivery forward: %v", err)
	}
}

// age moves a delivery's creation into the past, which is how the maximum age
// is reached without waiting hours for it.
func (setup fixture) age(t *testing.T, deliveryID string, by time.Duration) {
	t.Helper()

	if _, err := setup.pool.Exec(context.Background(),
		`UPDATE webhook_deliveries SET created_at = created_at - $2::interval
         WHERE id = $1`, deliveryID, by); err != nil {
		t.Fatalf("age a delivery: %v", err)
	}
}
