package client

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"convia/internal/events"
)

// streaming is an installation that serves the person's stream, recording the
// handshakes it received and sending what a test tells it to.
type streaming struct {
	mutex      sync.Mutex
	handshakes []*http.Request
	send       []func(ctx context.Context, connection *websocket.Conn) websocket.StatusCode
}

func (installation *streaming) handler() http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		installation.mutex.Lock()
		attempt := len(installation.handshakes)
		installation.handshakes = append(installation.handshakes, request.Clone(context.Background()))
		var answer func(ctx context.Context, connection *websocket.Conn) websocket.StatusCode
		if attempt < len(installation.send) {
			answer = installation.send[attempt]
		}
		installation.mutex.Unlock()

		connection, err := websocket.Accept(response, request, nil)
		if err != nil {
			return
		}
		defer connection.CloseNow()

		status := websocket.StatusNormalClosure
		if answer != nil {
			status = answer(request.Context(), connection)
		}
		_ = connection.Close(status, "")
	}
}

func (installation *streaming) asked() []*http.Request {
	installation.mutex.Lock()
	defer installation.mutex.Unlock()
	return append([]*http.Request(nil), installation.handshakes...)
}

// deliver writes one event down a connection, as Convia does.
func deliver(ctx context.Context, connection *websocket.Conn, kind events.Type, cursor string) {
	event := events.New(kind, "app_1", "room_1", "", events.Data{"room_id": "room_1"})
	event.Cursor = cursor
	body, _ := json.Marshal(event)
	_ = connection.Write(ctx, websocket.MessageText, body)
}

/*
watched is what a test sees of a running stream: the events and the states it
reported, read under the lock they are written behind.
*/
type watched struct {
	mutex  sync.Mutex
	events []events.Event
	states []State
}

func (seen *watched) received() []events.Event {
	seen.mutex.Lock()
	defer seen.mutex.Unlock()
	return append([]events.Event(nil), seen.events...)
}

func (seen *watched) reported() []State {
	seen.mutex.Lock()
	defer seen.mutex.Unlock()
	return append([]State(nil), seen.states...)
}

func watching(t *testing.T, installation *streaming) (*Stream, *watched) {
	t.Helper()

	server := httptest.NewServer(installation.handler())
	t.Cleanup(server.Close)

	client, err := New(server.URL, server.Client())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	client.Resume("cvs_session")

	seen := &watched{}
	stream := NewStream(client, slog.New(slog.NewTextHandler(io.Discard, nil)),
		func(event events.Event) {
			seen.mutex.Lock()
			defer seen.mutex.Unlock()
			seen.events = append(seen.events, event)
		},
		func(state State) {
			seen.mutex.Lock()
			defer seen.mutex.Unlock()
			seen.states = append(seen.states, state)
		})
	return stream, seen
}

/*
TestTheStreamIsOpenedWithTheSessionInAHeader is why the stream belongs to the
application rather than to the window: a page cannot set this header on a
handshake, and the session must not be in the page anyway.
*/
func TestTheStreamIsOpenedWithTheSessionInAHeader(t *testing.T) {
	installation := &streaming{send: []func(context.Context, *websocket.Conn) websocket.StatusCode{
		func(ctx context.Context, connection *websocket.Conn) websocket.StatusCode {
			deliver(ctx, connection, events.RoomUpdated, "10-1")
			return websocket.StatusNormalClosure
		},
	}}
	stream, seen := watching(t, installation)

	ctx, stop := context.WithCancel(context.Background())
	go stream.Run(ctx)
	waitFor(t, func() bool { return len(seen.received()) == 1 })
	stop()

	handshake := installation.asked()[0]
	if held := handshake.Header.Get("Authorization"); held != "Bearer cvs_session" {
		t.Errorf("the stream was opened with %q", held)
	}
	if handshake.URL.Path != "/v1/me/events" {
		t.Errorf("the stream was opened at %s", handshake.URL.Path)
	}
	if handshake.URL.Query().Get("after") != "" {
		t.Error("a first connection asked to resume")
	}
	if len(seen.reported()) == 0 || !seen.reported()[0].Live {
		t.Errorf("the window was not told the stream was live: %+v", seen.reported())
	}
}

/*
TestTheStreamResumesFromWhereItGotTo is what keeps a reconnection from being a
gap: the cursor of the last event goes back on the next handshake.
*/
func TestTheStreamResumesFromWhereItGotTo(t *testing.T) {
	installation := &streaming{send: []func(context.Context, *websocket.Conn) websocket.StatusCode{
		func(ctx context.Context, connection *websocket.Conn) websocket.StatusCode {
			deliver(ctx, connection, events.RoomUpdated, "10-1")
			deliver(ctx, connection, events.RoomClosed, "10-4")
			return websocket.StatusAbnormalClosure
		},
		func(ctx context.Context, connection *websocket.Conn) websocket.StatusCode {
			deliver(ctx, connection, events.RoomReopened, "10-9")
			return websocket.StatusNormalClosure
		},
	}}
	stream, seen := watching(t, installation)

	ctx, stop := context.WithCancel(context.Background())
	go stream.Run(ctx)
	waitFor(t, func() bool { return len(installation.asked()) >= 2 && len(seen.received()) >= 3 })
	stop()

	if resumed := installation.asked()[1].URL.Query().Get("after"); resumed != "10-4" {
		t.Errorf("the stream resumed from %q, want the last cursor it received", resumed)
	}
	if cursor := stream.Cursor(); cursor != "10-9" {
		t.Errorf("Cursor() = %q, want what the application saves for the next run", cursor)
	}
}

/*
TestACursorTooOldIsSaidToTheWindow: what was missed cannot be replayed, so the
screen has to read what it shows again, and the next connection starts from now.
*/
func TestACursorTooOldIsSaidToTheWindow(t *testing.T) {
	installation := &streaming{send: []func(context.Context, *websocket.Conn) websocket.StatusCode{
		func(ctx context.Context, connection *websocket.Conn) websocket.StatusCode {
			deliver(ctx, connection, events.RoomUpdated, "10-1")
			return closedTooOld
		},
	}}
	stream, seen := watching(t, installation)

	ctx, stop := context.WithCancel(context.Background())
	go stream.Run(ctx)
	waitFor(t, func() bool {
		for _, state := range seen.reported() {
			if state.Gap {
				return true
			}
		}
		return false
	})
	stop()

	if cursor := stream.Cursor(); cursor != "" {
		t.Errorf("Cursor() = %q, want it dropped", cursor)
	}
}

/*
TestASessionThatEndedStopsTheStream, because reconnecting would be refused for
the same reason every time, and the window has to ask somebody to sign in.
*/
func TestASessionThatEndedStopsTheStream(t *testing.T) {
	installation := &streaming{send: []func(context.Context, *websocket.Conn) websocket.StatusCode{
		func(context.Context, *websocket.Conn) websocket.StatusCode { return closedUnauthentic },
	}}
	stream, seen := watching(t, installation)

	finished := make(chan struct{})
	go func() {
		defer close(finished)
		stream.Run(context.Background())
	}()

	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("the stream kept reconnecting after its session ended")
	}

	if asked := installation.asked(); len(asked) != 1 {
		t.Errorf("the stream connected %d times", len(asked))
	}
	var ended bool
	for _, state := range seen.reported() {
		ended = ended || state.Ended
	}
	if !ended {
		t.Errorf("the window was not told the session ended: %+v", seen.reported())
	}
}

// TestAStreamWithNoSessionDoesNotConnect: there is nothing to present, and a
// handshake without one is refused.
func TestAStreamWithNoSessionDoesNotConnect(t *testing.T) {
	installation := &streaming{}
	stream, _ := watching(t, installation)
	stream.client.Forget()

	stream.Run(context.Background())

	if asked := installation.asked(); len(asked) != 0 {
		t.Errorf("the stream connected %d times without a session", len(asked))
	}
}

func waitFor(t *testing.T, done func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if done() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("what the test was waiting for did not happen")
}
