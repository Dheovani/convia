package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"convia/internal/api"
	"convia/internal/credentials"
	"convia/internal/events"
)

// eventStreamURL is the address a subscriber dials.
func eventStreamURL(server *httptest.Server) string {
	return strings.Replace(server.URL, "http://", "ws://", 1) + api.Prefix + "/events"
}

/*
transcript is an in-memory log a test can read while the server writes to it.

Every other test in this package reads a log after the operation that wrote it
returned, on the same goroutine, so a bytes.Buffer is enough. The access log is
different: it is written by whichever goroutine served the request, and a
stream's line is written when the connection ends rather than when the test
asked for it. Reading a plain buffer under those conditions is a data race, and
it is the same mistake the code under test would be making if it shared one.
*/
type transcript struct {
	mutex   sync.Mutex
	written bytes.Buffer
}

func (log *transcript) Write(entry []byte) (int, error) {
	log.mutex.Lock()
	defer log.mutex.Unlock()
	return log.written.Write(entry)
}

func (log *transcript) String() string {
	log.mutex.Lock()
	defer log.mutex.Unlock()
	return log.written.String()
}

/*
serving starts the whole routed server, middleware and all, over a real
listener.

The event stream is the one route that stops being a request and a response, so
proving it works means proving it works through the chain that wraps every
other route rather than in isolation.
*/
func serving(t *testing.T, broker *events.Broker, logs io.Writer) *httptest.Server {
	t.Helper()

	dependencies := testDependencies()
	dependencies.TenantEvents = events.NewTenantHandler(
		slog.New(slog.NewTextHandler(io.Discard, nil)), broker)

	logger := slog.New(slog.NewJSONHandler(logs, nil))
	server := httptest.NewServer(New("127.0.0.1:0", logger, dependencies).Handler)

	t.Cleanup(server.Close)
	return server
}

// dialStream opens the stream with a credential the stub authenticator accepts.
func dialStream(t *testing.T, server *httptest.Server) *websocket.Conn {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	connection, _, err := websocket.Dial(ctx, eventStreamURL(server), &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": []string{"Bearer " + string(sampleSecret())}},
	})
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}

	t.Cleanup(func() { connection.CloseNow() })
	return connection
}

/*
TestAnUpgradeSurvivesTheMiddlewareChain is the reason this test is here rather
than in the events package.

Every route is wrapped by the access log, which replaces the response writer
with one of its own. A wrapper that did not pass the connection through would
turn the one route that needs to take it over into a route that cannot, and
nothing in the events package would notice.
*/
func TestAnUpgradeSurvivesTheMiddlewareChain(t *testing.T) {
	broker := events.NewBroker()
	connection := dialStream(t, serving(t, broker, &transcript{}))

	waitForStream(t, broker)
	broker.Publish(events.New(events.CallStarted, sampleApplication().ID, sampleCall().ID, "req_1", nil))

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	var received events.Event
	if err := wsjson.Read(ctx, connection, &received); err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if received.Subject.ID != sampleCall().ID {
		t.Errorf("the subscriber received an event about %q", received.Subject.ID)
	}
}

/*
TestTheAccessLogTellsTheTruthAboutAnUpgrade keeps one line per request honest
for the one request that stops being one.

Without the recorder noticing the hijack, an upgraded connection would be
logged as an ordinary 200 that returned nothing — which is exactly what an
operator would be looking at while trying to work out why a client never
connected.
*/
func TestTheAccessLogTellsTheTruthAboutAnUpgrade(t *testing.T) {
	broker := events.NewBroker()
	logs := &transcript{}
	server := serving(t, broker, logs)

	connection := dialStream(t, server)
	waitForStream(t, broker)
	connection.Close(websocket.StatusNormalClosure, "done")

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if logged(t, logs.String(), api.Prefix+"/events", http.StatusSwitchingProtocols) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Errorf("the access log never recorded the upgrade as %d: %s",
		http.StatusSwitchingProtocols, logs.String())
}

/*
TestAStreamOutlivesTheServerWriteTimeout pins a property Convia depends on and
does not implement.

Every route is served with a write timeout, which is right for a request and a
response and fatal for a connection meant to stay open for hours. What saves
the stream is net/http: hijacking a connection clears the deadlines the server
set on it. That is worth a test precisely because it is somebody else's
guarantee — if it stopped holding, every stream would die thirty seconds in,
in production, long after a fast test had finished.

The timeout here is a fraction of a second, so the same failure would be
visible immediately.
*/
func TestAStreamOutlivesTheServerWriteTimeout(t *testing.T) {
	broker := events.NewBroker()

	dependencies := testDependencies()
	dependencies.TenantEvents = events.NewTenantHandler(
		slog.New(slog.NewTextHandler(io.Discard, nil)), broker)

	server := httptest.NewUnstartedServer(
		New("127.0.0.1:0", slog.New(slog.NewTextHandler(io.Discard, nil)), dependencies).Handler)
	server.Config.ReadTimeout = 150 * time.Millisecond
	server.Config.WriteTimeout = 150 * time.Millisecond
	server.Start()
	t.Cleanup(server.Close)

	connection := dialStream(t, server)
	waitForStream(t, broker)

	// Comfortably past both deadlines, and nothing has been written yet.
	time.Sleep(500 * time.Millisecond)

	broker.Publish(events.New(events.CallEnded, sampleApplication().ID, sampleCall().ID, "req_1", nil))

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	var received events.Event
	if err := wsjson.Read(ctx, connection, &received); err != nil {
		t.Fatalf("the stream did not survive the server's write timeout: %v", err)
	}
	if received.Type != events.CallEnded {
		t.Errorf("the subscriber received %q", received.Type)
	}
}

/*
TestTheStreamIsRefusedWithoutACredential proves the stream is authenticated by
the same middleware as everything else.

A WebSocket endpoint that quietly skipped authentication would be the most
valuable route in the API to find.
*/
func TestTheStreamIsRefusedWithoutACredential(t *testing.T) {
	server := serving(t, events.NewBroker(), io.Discard)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	connection, response, err := websocket.Dial(ctx, eventStreamURL(server), nil)
	if err == nil {
		connection.CloseNow()
		t.Fatal("an unauthenticated request was upgraded")
	}
	if response == nil {
		t.Fatalf("Dial() error = %v, with no HTTP answer to inspect", err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusUnauthorized {
		t.Errorf("an unauthenticated stream was answered %d, want %d",
			response.StatusCode, http.StatusUnauthorized)
	}
}

/*
TestTheContractDescribesTheHandshakeAndTheEnvelope is M14-011.

OpenAPI 3.0.3 cannot describe a stream, but it can describe the exchange that
opens one and the shape of what travels on it. Both are published, so a client
author has the endpoint, its refusals, and the envelope in the same document as
the rest of the API — and a delivered event is validated against the schema
that document names.
*/
func TestTheContractDescribesTheHandshakeAndTheEnvelope(t *testing.T) {
	document := loadSpecification(t)

	item := document.Paths.Find(api.Prefix + "/events")
	if item == nil || item.Get == nil {
		t.Fatalf("%s does not describe the event stream", specificationPath)
	}
	if item.Get.Responses.Status(http.StatusSwitchingProtocols) == nil {
		t.Error("the event stream operation does not describe the upgrade it answers with")
	}

	schema, described := document.Components.Schemas["Event"]
	if !described {
		t.Fatalf("%s declares no Event schema", specificationPath)
	}

	event := events.New(events.ParticipantRemoved, sampleApplication().ID,
		sampleParticipant().ID, "req_1", events.Data{
			"call_id":    sampleCall().ID,
			"role":       "member",
			"status":     "removed",
			"guest":      false,
			"user_id":    sampleUser().ID,
			"removed_by": "application",
		})

	body, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	assertBodyMatchesSchema(t, schema.Value, body)
}

/*
TestTheContractAndTheVocabularyAgree keeps the published event types and the
delivered ones from drifting apart, in both directions.
*/
func TestTheContractAndTheVocabularyAgree(t *testing.T) {
	document := loadSpecification(t)

	schema, described := document.Components.Schemas["EventType"]
	if !described {
		t.Fatalf("%s declares no EventType schema", specificationPath)
	}

	documented := make(map[string]bool, len(schema.Value.Enum))
	for _, value := range schema.Value.Enum {
		name, isString := value.(string)
		if !isString {
			t.Fatalf("the EventType enum contains a non-string value: %v", value)
		}
		documented[name] = false
	}

	for _, kind := range events.Types() {
		if _, present := documented[string(kind)]; !present {
			t.Errorf("event type %q is delivered but missing from the contract", kind)
			continue
		}
		documented[string(kind)] = true
	}

	for name, delivered := range documented {
		if !delivered {
			t.Errorf("event type %q is documented but never delivered", name)
		}
	}
}

/*
TestTheStreamPublishesNoProviderName is the same tripwire the media boundary
already has, applied to the newest thing that leaves Convia.

An event is a Convia concept. A field naming whatever transports the audio
would make the stream a way to learn what the contract deliberately does not
say.
*/
func TestTheStreamPublishesNoProviderName(t *testing.T) {
	event := events.New(events.CallStarted, sampleApplication().ID, sampleCall().ID, "req_1",
		events.Data{"room_id": sampleRoom().ID, "status": "active", "actor": "application"})

	body, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	for _, forbidden := range []string{"livekit", "sfu", "turn", "webrtc"} {
		if strings.Contains(strings.ToLower(string(body)), forbidden) {
			t.Errorf("a delivered event mentions %q: %s", forbidden, body)
		}
	}
}

// logged reports whether the access log holds one line for a path with a status.
func logged(t *testing.T, entries, path string, status int) bool {
	t.Helper()

	for line := range strings.SplitSeq(entries, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}

		var record struct {
			Message string `json:"msg"`
			Path    string `json:"path"`
			Status  int    `json:"status"`
		}
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			continue
		}
		if record.Message == "HTTP request" && record.Path == path && record.Status == status {
			return true
		}
	}
	return false
}

// waitForStream waits until the handler has registered its subscription.
func waitForStream(t *testing.T, broker *events.Broker) {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if broker.Active() > 0 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("the stream never opened")
}

// sampleSecret is any value the stub authenticator accepts as a key.
func sampleSecret() credentials.Secret {
	return credentials.Secret("cvk_" + strings.Repeat("A", 26) + "_" + strings.Repeat("B", 26))
}
