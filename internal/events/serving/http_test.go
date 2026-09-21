package serving

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"convia/internal/api"
	"convia/internal/credentials"

	"convia/internal/events"
)

/*
listening starts the stream endpoint served with one caller's authority.

Authentication is not repeated here: the middleware that verifies a key is the
server package's, and putting a principal in the context directly is exactly
what it does once it has.
*/
func listening(t *testing.T, broker *events.Broker, principal credentials.Principal) *httptest.Server {
	t.Helper()

	handler := NewTenantHandler(slog.New(slog.NewTextHandler(io.Discard, nil)), broker, nil)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		handler.Stream(response, request.WithContext(
			credentials.ContextWithPrincipal(request.Context(), principal)))
	}))

	t.Cleanup(server.Close)
	return server
}

// subscriber opens a stream against a running endpoint, as a client would.
func subscriber(t *testing.T, server *httptest.Server) *websocket.Conn {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), waitFor)
	defer cancel()

	connection, _, err := websocket.Dial(ctx, strings.Replace(server.URL, "http://", "ws://", 1), nil)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}

	/*
		Registered after the server's own cleanup, so it runs before it: the
		handler is waiting on this connection, and closing the server first
		would be waiting for something the test was still holding.
	*/
	t.Cleanup(func() { connection.CloseNow() })
	return connection
}

// everything is a principal permitted to receive every event Convia delivers.
func everything() credentials.Principal {
	return holding(credentials.ScopeEventsRead, credentials.ScopeCallsRead,
		credentials.ScopeParticipantsRead, credentials.ScopeInvitationsRead)
}

/*
TestAClientReceivesEventsAsTheyHappen is the endpoint doing the one thing it
exists for, over a real socket.

Everything below tests a way it can go wrong; this tests that it goes right.
*/
func TestAClientReceivesEventsAsTheyHappen(t *testing.T) {
	broker := events.NewBroker()
	connection := subscriber(t, listening(t, broker, everything()))

	/*
		Publishing races the handler's subscription, which is the honest
		situation: a client that has just connected may or may not be
		registered yet. Waiting for the broker to hold a stream is what makes
		the test deterministic without changing what is being tested.
	*/
	waitUntilSubscribed(t, broker)

	broker.Publish(events.New(events.ParticipantJoined, "app_1", "part_1", "req_1", events.Data{
		"call_id": "call_1",
		"role":    "moderator",
		"guest":   false,
		"user_id": "usr_1",
	}))

	ctx, cancel := context.WithTimeout(context.Background(), waitFor)
	defer cancel()

	var received events.Event
	if err := wsjson.Read(ctx, connection, &received); err != nil {
		t.Fatalf("Read() error = %v", err)
	}

	if received.Type != events.ParticipantJoined {
		t.Errorf("the client received %q", received.Type)
	}
	if received.Subject.ID != "part_1" || received.Subject.Type != events.SubjectParticipant {
		t.Errorf("the client received an event about %+v", received.Subject)
	}
	if received.CorrelationID != "req_1" {
		t.Errorf("the delivered event names request %q", received.CorrelationID)
	}
	if received.Data["role"] != "moderator" {
		t.Errorf("the delivered event says the role is %v", received.Data["role"])
	}
}

/*
TestAClientThatSendsAnythingIsDisconnected is M14-010 as behavior rather than
as a promise.

The stream is one direction, so there is no message a client could send that
Convia would act on â€” including one carrying audio. Refusing outright is what
tells a client its protocol is wrong instead of letting it believe Convia
received something.
*/
func TestAClientThatSendsAnythingIsDisconnected(t *testing.T) {
	broker := events.NewBroker()
	connection := subscriber(t, listening(t, broker, everything()))

	ctx, cancel := context.WithTimeout(context.Background(), waitFor)
	defer cancel()

	if err := connection.Write(ctx, websocket.MessageText, []byte(`{"subscribe":"everything"}`)); err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	_, _, err := connection.Read(ctx)
	if got := websocket.CloseStatus(err); got != websocket.StatusUnsupportedData {
		t.Errorf("a client that spoke was closed with %d, want %d (error %v)",
			got, websocket.StatusUnsupportedData, err)
	}
}

/*
TestABinaryMessageIsRefusedToo makes sure the refusal is about direction and
not about parsing.

A binary frame is what a client trying to push media would send, and it must
meet the same answer as a text one rather than a decoding error.
*/
func TestABinaryMessageIsRefusedToo(t *testing.T) {
	broker := events.NewBroker()
	connection := subscriber(t, listening(t, broker, everything()))

	ctx, cancel := context.WithTimeout(context.Background(), waitFor)
	defer cancel()

	if err := connection.Write(ctx, websocket.MessageBinary, make([]byte, 512)); err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	_, _, err := connection.Read(ctx)
	if got := websocket.CloseStatus(err); got != websocket.StatusUnsupportedData {
		t.Errorf("a client that sent binary was closed with %d, want %d (error %v)",
			got, websocket.StatusUnsupportedData, err)
	}
}

/*
TestShutdownTellsSubscribersWhy is the other half of M14-012.

A subscriber whose instance is going away should read a reason and reconnect,
not watch a socket vanish and guess.
*/
func TestShutdownTellsSubscribersWhy(t *testing.T) {
	broker := events.NewBroker()
	connection := subscriber(t, listening(t, broker, everything()))
	waitUntilSubscribed(t, broker)

	/*
		The client reads while the instance stops, which is what a client
		actually does and what the close handshake needs: a peer echoes a close
		frame from its own read loop, so a subscriber that had stopped reading
		would keep Convia waiting out the handshake rather than answering it.
	*/
	closed := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*waitFor)
		defer cancel()

		_, _, err := connection.Read(ctx)
		closed <- err
	}()

	stopping, cancel := context.WithTimeout(context.Background(), waitFor)
	defer cancel()

	if !broker.Stop(stopping) {
		t.Error("Stop() gave up before the subscriber had let go")
	}

	select {
	case err := <-closed:
		if got := websocket.CloseStatus(err); got != websocket.StatusGoingAway {
			t.Errorf("a subscriber of a stopping instance was closed with %d, want %d (error %v)",
				got, websocket.StatusGoingAway, err)
		}
	case <-time.After(waitFor):
		t.Error("the subscriber was never told the instance was stopping")
	}
}

/*
TestEveryEndingSaysSomethingDifferent pins the close frames a client branches
on.

They are a public contract in the same way a status code is: docs/events.md
publishes them, and a client that reconnects blindly after falling behind would
keep a gap in its view forever.
*/
func TestEveryEndingSaysSomethingDifferent(t *testing.T) {
	cases := map[events.Ending]websocket.StatusCode{
		events.EndedByReader:   websocket.StatusNormalClosure,
		events.EndedBehind:     statusBehind,
		events.EndedByShutdown: websocket.StatusGoingAway,
	}

	seen := make(map[websocket.StatusCode]bool, len(cases))
	for ending, want := range cases {
		got, reason := closeFor(ending)
		if got != want {
			t.Errorf("ending %d closes with %d, want %d", ending, got, want)
		}
		if reason == "" {
			t.Errorf("ending %d closes without saying why", ending)
		}
		if seen[got] {
			t.Errorf("ending %d reuses close status %d", ending, got)
		}
		seen[got] = true
	}

	/*
		Falling behind is not a transport failure, so it uses the range RFC 6455
		reserves for applications rather than borrowing a code that means
		something else.
	*/
	if statusBehind < 4000 || statusBehind > 4999 {
		t.Errorf("the behind status %d is outside the range reserved for applications", statusBehind)
	}
}

/*
TestACredentialThatMayNotSubscribeNeverReachesASocket keeps every refusal on
the side of the exchange that can still explain itself.

Once the connection is a WebSocket there is no status code and no error body
left, so a client refused after the upgrade would events.receive a close frame where
it expected an HTTP answer.
*/
func TestACredentialThatMayNotSubscribeNeverReachesASocket(t *testing.T) {
	server := listening(t, events.NewBroker(), holding(credentials.ScopeCallsRead))

	response := attempt(t, server)
	defer response.Body.Close()

	if response.StatusCode != http.StatusForbidden {
		t.Errorf("an unpermitted credential was answered %d, want %d",
			response.StatusCode, http.StatusForbidden)
	}
	assertFailureCode(t, response, api.CodeForbidden)
}

/*
TestReachingTheCeilingIsAnHTTPRefusal covers M14-009 at the transport.

A client is told to retry, in the ordinary error shape, before anything has
been upgraded.
*/
func TestReachingTheCeilingIsAnHTTPRefusal(t *testing.T) {
	broker := events.NewBroker()
	server := listening(t, broker, everything())

	for range events.MaxStreamsPerApplication {
		subscriber(t, server)
	}
	waitUntilOpen(t, broker, events.MaxStreamsPerApplication)

	response := attempt(t, server)
	defer response.Body.Close()

	if response.StatusCode != http.StatusTooManyRequests {
		t.Errorf("a stream past the ceiling was answered %d, want %d",
			response.StatusCode, http.StatusTooManyRequests)
	}
	if response.Header.Get("Retry-After") == "" {
		t.Error("the refusal does not say when to try again")
	}
	assertFailureCode(t, response, api.CodeRateLimited)
}

/*
TestAStoppedInstanceRefusesNewStreams keeps a shutting-down instance from
accepting work it is about to abandon.
*/
func TestAStoppedInstanceRefusesNewStreams(t *testing.T) {
	broker := events.NewBroker()
	server := listening(t, broker, everything())

	ctx, cancel := context.WithTimeout(context.Background(), waitFor)
	defer cancel()
	broker.Stop(ctx)

	response := attempt(t, server)
	defer response.Body.Close()

	if response.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("a stopping instance answered %d, want %d",
			response.StatusCode, http.StatusServiceUnavailable)
	}
	assertFailureCode(t, response, api.CodeUnavailable)
}

// attempt asks for a stream and returns the HTTP answer, which is what a
// refused subscriber receives instead of an upgrade.
func attempt(t *testing.T, server *httptest.Server) *http.Response {
	t.Helper()

	request, err := http.NewRequest(http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	request.Header.Set("Connection", "Upgrade")
	request.Header.Set("Upgrade", "websocket")
	request.Header.Set("Sec-WebSocket-Version", "13")
	request.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")

	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	return response
}

// assertFailureCode checks that a refusal arrived in Convia's one error shape.
func assertFailureCode(t *testing.T, response *http.Response, code api.ErrorCode) {
	t.Helper()

	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}

	var failure struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &failure); err != nil {
		t.Fatalf("the refusal is not Convia's error shape: %s", body)
	}
	if failure.Error.Code != string(code) {
		t.Errorf("the refusal reports %q, want %q", failure.Error.Code, code)
	}
}

// waitUntilSubscribed waits for the handler to have registered its stream.
func waitUntilSubscribed(t *testing.T, broker *events.Broker) {
	t.Helper()
	waitUntilOpen(t, broker, 1)
}

// waitUntilOpen waits until the broker holds the expected number of streams.
func waitUntilOpen(t *testing.T, broker *events.Broker, expected int) {
	t.Helper()

	deadline := time.Now().Add(waitFor)
	for time.Now().Before(deadline) {
		if broker.Active() >= expected {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("only %d of %d streams opened", broker.Active(), expected)
}
