package serving

import (
	"context"
	"errors"
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

	"convia/internal/sessions"

	"convia/internal/events"
)

// sessionToken is what the test browser holds. The stub below accepts it
// without looking, because verifying one is the sessions package's to test.
const sessionToken = "cvs_4XZQP7KN2VJH6TBWMDR3YAFC5E_YH3TKPQ2MWZC7NVJ6BXRD4FGA5"

// switchableSession authenticates until a test says it no longer does.
type switchableSession struct {
	mutex sync.Mutex
	err   error
}

func (session *switchableSession) Authenticate(context.Context, string) (sessions.Principal, error) {
	session.mutex.Lock()
	defer session.mutex.Unlock()
	return signedIn("usr_ana"), session.err
}

func (session *switchableSession) answer(err error) {
	session.mutex.Lock()
	defer session.mutex.Unlock()
	session.err = err
}

// rooms answers which rooms a person is in, and can change its answer.
type rooms struct {
	mutex sync.Mutex
	ids   []string
	err   error
}

func (known *rooms) RoomIDsOf(context.Context, string, string) ([]string, error) {
	known.mutex.Lock()
	defer known.mutex.Unlock()
	return known.ids, known.err
}

func (known *rooms) become(ids ...string) {
	known.mutex.Lock()
	defer known.mutex.Unlock()
	known.ids = ids
}

/*
listeningAsPerson serves a person's stream with a session already verified, the
way the server's middleware leaves a request once it has.
*/
func listeningAsPerson(t *testing.T, handler *PersonHandler) *httptest.Server {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		handler.Stream(response, request.WithContext(
			sessions.ContextWithPrincipal(request.Context(), signedIn("usr_ana"))))
	}))
	t.Cleanup(server.Close)
	return server
}

func personHandler(broker *events.Broker, session sessionAuthenticator, known memberships) *PersonHandler {
	return NewPersonHandler(slog.New(slog.NewTextHandler(io.Discard, nil)), broker, nil, session,
		alwaysVisiting{}, nil, known)
}

// dialAsPerson opens the stream the way a browser holding a session would.
func dialAsPerson(t *testing.T, server *httptest.Server) (*websocket.Conn, *http.Response, error) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), waitFor)
	defer cancel()

	connection, response, err := websocket.Dial(ctx, strings.Replace(server.URL, "http://", "ws://", 1),
		&websocket.DialOptions{HTTPHeader: http.Header{
			"Cookie": []string{sessions.CookieName + "=" + sessionToken},
		}})
	if err == nil {
		t.Cleanup(func() { connection.CloseNow() })
	}
	return connection, response, err
}

/*
incoming reads a connection on its own goroutine.

A read with a deadline is not an option here: coder/websocket closes the
connection when a read's context ends, so a test that waited a bounded time for
an event that had not arrived yet would have closed the stream it was testing.
*/
func incoming(connection *websocket.Conn) (<-chan events.Event, <-chan error) {
	arrived := make(chan events.Event, events.QueueDepth)
	ended := make(chan error, 1)

	go func() {
		for {
			var event events.Event
			if err := wsjson.Read(context.Background(), connection, &event); err != nil {
				ended <- err
				return
			}
			arrived <- event
		}
	}()
	return arrived, ended
}

// TestAPersonReceivesEventsAboutTheirRoomsOverASocket is the handler doing the
// one thing it exists for.
func TestAPersonReceivesEventsAboutTheirRoomsOverASocket(t *testing.T) {
	broker := events.NewBroker()
	server := listeningAsPerson(t, personHandler(broker, &switchableSession{}, &rooms{ids: []string{"room_a"}}))

	connection, _, err := dialAsPerson(t, server)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	arrived, _ := incoming(connection)

	/*
		No wait for the subscription here, unlike the tenant's tests. The rooms
		are read before the upgrade, so a dial that returned is a stream that
		already covers them.
	*/
	broker.Publish(posted("room_b"))
	broker.Publish(posted("room_a"))

	select {
	case event := <-arrived:
		if event.Data["room_id"] != "room_a" {
			t.Errorf("the person received an event about %v", event.Data["room_id"])
		}
		if event.CorrelationID != "" {
			t.Errorf("the person was told which request caused it: %q", event.CorrelationID)
		}
	case <-time.After(waitFor):
		t.Fatal("no event arrived")
	}
}

/*
TestASessionThatEndsClosesTheStream is why a person's stream rechecks at all.

Signing out everywhere is what somebody does after losing a laptop. A socket
opened before that would otherwise go on carrying their rooms for as long as the
connection lasted, which is exactly as long as the person holding the laptop
wanted.
*/
func TestASessionThatEndsClosesTheStream(t *testing.T) {
	session := &switchableSession{}
	handler := personHandler(events.NewBroker(), session, &rooms{ids: []string{"room_a"}})
	handler.every = 10 * time.Millisecond
	server := listeningAsPerson(t, handler)

	connection, _, err := dialAsPerson(t, server)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	_, ended := incoming(connection)

	session.answer(sessions.ErrUnauthenticated)

	select {
	case err := <-ended:
		if got := websocket.CloseStatus(err); got != statusSessionEnded {
			t.Errorf("the stream closed with %d (%v), want %d", got, err, statusSessionEnded)
		}
	case <-time.After(waitFor):
		t.Fatal("the stream stayed open after its session ended")
	}
}

/*
TestACheckThatCannotBeMadeKeepsTheStreamOpen covers the failure that is about
Convia rather than the person. A database that did not answer once says nothing
about whether somebody is still signed in.
*/
func TestACheckThatCannotBeMadeKeepsTheStreamOpen(t *testing.T) {
	broker := events.NewBroker()
	session := &switchableSession{}
	handler := personHandler(broker, session, &rooms{ids: []string{"room_a"}})
	handler.every = 5 * time.Millisecond
	server := listeningAsPerson(t, handler)

	connection, _, err := dialAsPerson(t, server)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	arrived, ended := incoming(connection)

	session.answer(errors.New("the database is unreachable"))
	time.Sleep(50 * time.Millisecond)

	broker.Publish(posted("room_a"))

	select {
	case <-arrived:
	case err := <-ended:
		t.Fatalf("the stream closed on a check that could not be made: %v", err)
	case <-time.After(waitFor):
		t.Fatal("no event arrived")
	}
}

/*
TestARecheckReadsTheRoomsAgain is what bounds an event lost between instances.

A person added on another instance learns of it from an event that instance
relays. If the relay dropped it, the stream would never cover the room; reading
the rooms again is what makes that a delay rather than a permanent silence.
*/
func TestARecheckReadsTheRoomsAgain(t *testing.T) {
	broker := events.NewBroker()
	known := &rooms{}
	handler := personHandler(broker, &switchableSession{}, known)
	handler.every = 5 * time.Millisecond
	server := listeningAsPerson(t, handler)

	connection, _, err := dialAsPerson(t, server)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	arrived, _ := incoming(connection)

	// Added somewhere this stream never heard about.
	known.become("room_a")

	deadline := time.After(waitFor)
	for {
		broker.Publish(posted("room_a"))
		select {
		case <-arrived:
			return
		case <-deadline:
			t.Fatal("the stream never came to cover a room its person was added to")
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// TestRoomsThatCannotBeReadAreRefusedBeforeTheUpgrade keeps a failed read an
// HTTP answer rather than a socket that would stay silent forever.
func TestRoomsThatCannotBeReadAreRefusedBeforeTheUpgrade(t *testing.T) {
	broker := events.NewBroker()
	handler := personHandler(broker, &switchableSession{}, &rooms{err: errors.New("the database is unreachable")})
	server := listeningAsPerson(t, handler)

	_, response, err := dialAsPerson(t, server)
	if err == nil {
		t.Fatal("a stream whose rooms could not be read was upgraded")
	}
	if response == nil {
		t.Fatalf("Dial() error = %v, with no HTTP answer to inspect", err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", response.StatusCode, http.StatusInternalServerError)
	}
	if store := response.Header.Get("Cache-Control"); store != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", store)
	}

	deadline := time.Now().Add(waitFor)
	for broker.Active() != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if broker.Active() != 0 {
		t.Errorf("a refused stream kept its place: %d open", broker.Active())
	}
}

// alwaysVisiting is a visitor nobody has taken away, which is what the tests
// about a session's stream need from this dependency: nothing.
type alwaysVisiting struct{}

func (alwaysVisiting) Visiting(context.Context, string) (bool, error) { return true, nil }

// visiting answers whether a visitor is still somebody here, and can change
// its answer while a stream is open.
type visiting struct {
	mutex sync.Mutex
	still bool
	err   error
}

func (person *visiting) Visiting(context.Context, string) (bool, error) {
	person.mutex.Lock()
	defer person.mutex.Unlock()
	return person.still, person.err
}

func (person *visiting) leaves() {
	person.mutex.Lock()
	defer person.mutex.Unlock()
	person.still = false
}

/*
listeningAsVisitor serves the visitor's stream the way the peer surface leaves a
request once a signature has been verified and the signer turned out to be
somebody here: a principal in the context, and no session anywhere.
*/
func listeningAsVisitor(t *testing.T, handler *PersonHandler) *httptest.Server {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		handler.Visiting(response, request.WithContext(
			sessions.ContextWithPrincipal(request.Context(), signedIn("usr_ana"))))
	}))
	t.Cleanup(server.Close)
	return server
}

func dialAsVisitor(t *testing.T, server *httptest.Server) (*websocket.Conn, *http.Response, error) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), waitFor)
	defer cancel()

	connection, response, err := websocket.Dial(ctx, strings.Replace(server.URL, "http://", "ws://", 1), nil)
	if err == nil {
		t.Cleanup(func() { connection.CloseNow() })
	}
	return connection, response, err
}

/*
TestAVisitorReceivesTheSameEventsAPersonHereDoes is what makes a room elsewhere
worth being in.

A visitor is a user here — membership decides what they may be told, exactly as
it does for anybody else — and the only thing that differs is what proved them.
Nothing about the stream itself is a second implementation.
*/
func TestAVisitorReceivesTheSameEventsAPersonHereDoes(t *testing.T) {
	broker := events.NewBroker()
	handler := NewPersonHandler(slog.New(slog.NewTextHandler(io.Discard, nil)), broker, nil,
		&switchableSession{}, &visiting{still: true}, nil, &rooms{ids: []string{"room_a"}})

	connection, _, err := dialAsVisitor(t, listeningAsVisitor(t, handler))
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	arrived, _ := incoming(connection)

	broker.Publish(posted("room_b"))
	broker.Publish(posted("room_a"))

	select {
	case event := <-arrived:
		if event.Data["room_id"] != "room_a" {
			t.Errorf("the visitor received an event about %v", event.Data["room_id"])
		}
	case <-time.After(waitFor):
		t.Fatal("no event arrived")
	}
}

/*
TestAVisitorNoLongerRecognisedIsClosed is the visitor's half of the rule a
session's stream already has.

A signature proved somebody once, and a stream stays open for hours. Somebody
removed from every room here, or suspended, must stop being delivered to rather
than keep a connection that happens to have been opened while they were welcome.
*/
func TestAVisitorNoLongerRecognisedIsClosed(t *testing.T) {
	person := &visiting{still: true}
	handler := NewPersonHandler(slog.New(slog.NewTextHandler(io.Discard, nil)), events.NewBroker(), nil,
		&switchableSession{}, person, nil, &rooms{ids: []string{"room_a"}})
	handler.every = 10 * time.Millisecond

	connection, _, err := dialAsVisitor(t, listeningAsVisitor(t, handler))
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	_, failed := incoming(connection)

	person.leaves()

	select {
	case err := <-failed:
		if websocket.CloseStatus(err) != statusSessionEnded {
			t.Errorf("the stream closed with %v, want %d", err, statusSessionEnded)
		}
	case <-time.After(waitFor):
		t.Fatal("the stream stayed open for somebody this installation no longer recognises")
	}
}
