package client

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"convia/internal/events"
)

const (
	// firstRetry and lastRetry bound how long the stream waits before
	// connecting again. It starts low for an installation that is restarting
	// and backs off for one that is down.
	firstRetry = time.Second
	lastRetry  = 30 * time.Second

	// handshakeTimeout bounds opening one connection, which is a request like
	// any other until it becomes a socket.
	handshakeTimeout = 15 * time.Second
)

// The close codes docs/events.md publishes.
const (
	closedBehind      websocket.StatusCode = 4000
	closedUnauthentic websocket.StatusCode = 4001
	closedTooOld      websocket.StatusCode = 4002
)

/*
Stream is the person's events, kept open for as long as the application is signed in.

It is opened here rather than in the window because the session travels in a
header, which a page cannot set on a handshake. What the window receives is
whatever Watch is given, in the order the installation sent it.

**It resumes.** The cursor of the last event received is handed back on the next
connection, so what happened while the application was away arrives before
anything new, and nothing arrives twice. A cursor the installation no longer
keeps is dropped, and the caller is told, because what was missed can then only
be found by reading again.
*/
type Stream struct {
	client *Client
	logger *slog.Logger

	// on is called with each event, on the goroutine that reads the socket.
	on func(events.Event)
	// state is called when the stream opens, ends, or loses what it missed.
	state func(State)
	// dial opens the socket, and is replaced in tests.
	dial func(ctx context.Context, address string, header http.Header) (*websocket.Conn, error)

	/*
		cursor is how far the stream has read. The goroutine holding the
		connection writes it and the window reads it to save it, so it is
		behind a lock rather than merely assigned.
	*/
	mutex  sync.RWMutex
	cursor string
}

/*
State is what the window is told about the connection.

`Gap` says the events between the cursor and now cannot be replayed: the screen
has to read what it shows again. `Ended` says the session no longer
authenticates anybody, which is the one ending that does not mend itself.
*/
type State struct {
	Live   bool
	Gap    bool
	Ended  bool
	Reason string
}

// NewStream prepares the person's stream. Nothing is opened until Run is called.
func NewStream(client *Client, logger *slog.Logger, on func(events.Event), state func(State)) *Stream {
	return &Stream{
		client: client,
		logger: logger,
		on:     on,
		state:  state,
		dial:   dial,
	}
}

// Resume starts from a cursor kept from an earlier run of the application.
func (stream *Stream) Resume(cursor string) {
	stream.mutex.Lock()
	defer stream.mutex.Unlock()
	stream.cursor = cursor
}

// Cursor is how far the stream has read, which the application saves so that a
// restart does not begin blind.
func (stream *Stream) Cursor() string {
	stream.mutex.RLock()
	defer stream.mutex.RUnlock()
	return stream.cursor
}

/*
Run keeps the stream open until the context is cancelled.

It returns only when the caller stops it or the session stops authenticating
anybody: every other ending is a connection problem, and the answer to those is
to connect again.
*/
func (stream *Stream) Run(ctx context.Context) {
	wait := firstRetry
	for {
		ended, err := stream.once(ctx)
		if ctx.Err() != nil {
			return
		}
		if ended {
			return
		}
		if err != nil {
			stream.logger.Info("the event stream ended", "error", err, "retry_in", wait)
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		if wait *= 2; wait > lastRetry {
			wait = lastRetry
		}
	}
}

// once holds one connection for as long as it lasts, reporting whether the
// stream should stop for good.
func (stream *Stream) once(ctx context.Context) (bool, error) {
	session := stream.client.Session()
	if session == "" {
		return true, errors.New("no session is held")
	}

	opening, cancel := context.WithTimeout(ctx, handshakeTimeout)
	connection, err := stream.dial(opening, stream.address(), http.Header{
		"Authorization": []string{"Bearer " + session},
	})
	cancel()
	if err != nil {
		return false, err
	}
	defer connection.CloseNow()

	stream.state(State{Live: true})
	defer func() { stream.state(State{}) }()

	for {
		kind, message, err := connection.Read(ctx)
		if err != nil {
			return stream.ended(websocket.CloseStatus(err)), err
		}
		if kind != websocket.MessageText {
			continue
		}

		var event events.Event
		if err := json.Unmarshal(message, &event); err != nil {
			// An event this build cannot read is skipped rather than fatal:
			// the vocabulary is additive, and the rest of the stream is fine.
			stream.logger.Warn("an event could not be read", "error", err)
			continue
		}
		if event.Cursor != "" {
			stream.Resume(event.Cursor)
		}
		stream.on(event)
	}
}

/*
ended reports what a close code means for the stream, and tells the window what
it has to do about it.
*/
func (stream *Stream) ended(status websocket.StatusCode) bool {
	switch status {
	case closedUnauthentic:
		stream.state(State{Ended: true, Reason: "the session has ended"})
		return true
	case closedTooOld:
		// What was missed is gone, so the next connection starts from now and
		// whatever is on the screen is read again.
		stream.Resume("")
		stream.state(State{Gap: true, Reason: "convia no longer keeps what this application missed"})
		return false
	case closedBehind:
		// The gap is filled by resuming, which the next connection does.
		return false
	default:
		return false
	}
}

// address is the person's stream, resumed from the cursor when there is one.
func (stream *Stream) address() string {
	address := strings.Replace(stream.client.Address(), "http", "ws", 1) + prefix + "/me/events"
	cursor := stream.Cursor()
	if cursor == "" {
		return address
	}
	return address + "?after=" + url.QueryEscape(cursor)
}

func dial(ctx context.Context, address string, header http.Header) (*websocket.Conn, error) {
	connection, response, err := websocket.Dial(ctx, address, &websocket.DialOptions{HTTPHeader: header})
	if err != nil {
		if response != nil && response.StatusCode >= http.StatusBadRequest {
			return nil, &Refusal{Status: response.StatusCode, Message: response.Status}
		}
		return nil, &Unreachable{cause: err}
	}
	return connection, nil
}
