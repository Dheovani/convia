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

	"convia/internal/credentials"
	"convia/internal/sessions"

	"convia/internal/events"
)

// fakeReplayer is a journal held in memory.
type fakeReplayer struct {
	mutex    sync.Mutex
	floor    events.Cursor
	position events.Cursor
	kept     []events.Event
}

func (replayer *fakeReplayer) Position() events.Cursor {
	replayer.mutex.Lock()
	defer replayer.mutex.Unlock()
	return replayer.position
}

func (replayer *fakeReplayer) Replay(_ context.Context, applicationID string, after, until events.Cursor,
	each func(events.Event) error) error {
	if after.Before(replayer.floor) {
		return events.ErrCursorTooOld
	}
	for _, event := range replayer.kept {
		cursor, _ := events.ParseCursor(event.Cursor)
		if event.ApplicationID != applicationID || !after.Before(cursor) || until.Before(cursor) {
			continue
		}
		if err := each(event); err != nil {
			return err
		}
	}
	return nil
}

// recorded is an event as the journal returns it.
func recorded(kind events.Type, subjectID string, position int64, data events.Data) events.Event {
	event := events.New(kind, "app_1", subjectID, "", data)
	event.Cursor = events.Cursor{Transaction: 10, Position: position}.String()
	return event
}

func resuming(t *testing.T, broker *events.Broker, replayer events.Replayer, query string) (*websocket.Conn, *http.Response, error) {
	t.Helper()

	principal := holding(credentials.ScopeEventsRead, credentials.ScopeCallsRead, credentials.ScopePresenceRead)

	handler := NewTenantHandler(slog.New(slog.NewTextHandler(io.Discard, nil)), broker, replayer)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		handler.Stream(response, request.WithContext(
			credentials.ContextWithPrincipal(request.Context(), principal)))
	}))
	t.Cleanup(server.Close)

	ctx, cancel := context.WithTimeout(context.Background(), waitFor)
	defer cancel()
	connection, response, err := websocket.Dial(ctx, strings.Replace(server.URL, "http://", "ws://", 1)+query, nil)
	if connection != nil {
		t.Cleanup(func() { connection.CloseNow() })
	}
	return connection, response, err
}

func read(t *testing.T, connection *websocket.Conn) events.Event {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), waitFor)
	defer cancel()
	var event events.Event
	if err := wsjson.Read(ctx, connection, &event); err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	return event
}

/*
TestAResumedStreamIsToldWhatItMissedOnceAndInOrder is M14-006: what happened
after the cursor is replayed, the live stream takes over where the replay ends,
and an event that arrives both ways is written once.
*/
func TestAResumedStreamIsToldWhatItMissedOnceAndInOrder(t *testing.T) {
	broker := events.NewBroker()
	missed := []events.Event{
		recorded(events.CallStarted, "call_1", 2, events.Data{"room_id": "room_1"}),
		recorded(events.CallEnded, "call_1", 3, events.Data{"room_id": "room_1"}),
	}
	replayer := &fakeReplayer{
		position: events.Cursor{Transaction: 10, Position: 3},
		kept: append([]events.Event{
			recorded(events.CallStarted, "call_0", 1, events.Data{"room_id": "room_1"}),
		}, missed...),
	}

	connection, _, err := resuming(t, broker, replayer, "?after=10-1")
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	waitUntilSubscribed(t, broker)

	// The last replayed event arrives live too, and a new one after it.
	broker.Receive(missed[1])
	live := recorded(events.CallStarted, "call_2", 4, events.Data{"room_id": "room_1"})
	broker.Receive(live)

	for _, want := range append(missed, live) {
		if got := read(t, connection); got.ID != want.ID || got.Cursor != want.Cursor {
			t.Fatalf("received %s at %s, want %s at %s", got.Type, got.Cursor, want.Type, want.Cursor)
		}
	}
}

// TestAResumedStreamStillCarriesPresence: presence has no cursor and is never skipped.
func TestAResumedStreamStillCarriesPresence(t *testing.T) {
	broker := events.NewBroker()
	replayer := &fakeReplayer{position: events.Cursor{Transaction: 10, Position: 5}}

	connection, _, err := resuming(t, broker, replayer, "?after=10-5")
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	waitUntilSubscribed(t, broker)

	presence := events.New(events.PresenceChanged, "app_1", "usr_1", "", events.Data{"state": "online"})
	broker.Publish(presence)
	if got := read(t, connection); got.ID != presence.ID {
		t.Errorf("received %s, want the presence change", got.Type)
	}
}

// TestACursorTooOldIsSaidWithItsOwnCode tells the client to re-read rather than trust a gap.
func TestACursorTooOldIsSaidWithItsOwnCode(t *testing.T) {
	for name, replayer := range map[string]events.Replayer{
		"a cursor before what is kept": &fakeReplayer{
			floor:    events.Cursor{Transaction: 10, Position: 50},
			position: events.Cursor{Transaction: 10, Position: 60},
		},
		"nothing recorded to resume from": nil,
	} {
		t.Run(name, func(t *testing.T) {
			connection, _, err := resuming(t, events.NewBroker(), replayer, "?after=10-1")
			if err != nil {
				t.Fatalf("Dial() error = %v", err)
			}

			ctx, cancel := context.WithTimeout(context.Background(), waitFor)
			defer cancel()
			_, _, err = connection.Read(ctx)
			if status := websocket.CloseStatus(err); status != statusTooOld {
				t.Errorf("closed with %d (%v), want %d", status, err, statusTooOld)
			}
		})
	}
}

// TestACursorConviaDidNotWriteIsRefused before anything is opened.
func TestACursorConviaDidNotWriteIsRefused(t *testing.T) {
	for _, cursor := range []string{"banana", "10", "10-0", "-1-2", "10-01", "10-2-3"} {
		t.Run(cursor, func(t *testing.T) {
			broker := events.NewBroker()
			_, response, err := resuming(t, broker, &fakeReplayer{}, "?after="+cursor)
			if err == nil {
				t.Fatal("Dial() succeeded")
			}
			if response == nil || response.StatusCode != http.StatusBadRequest {
				t.Errorf("response = %v, want 400", response)
			}
			if broker.Active() != 0 {
				t.Error("a stream was opened for a malformed cursor")
			}
		})
	}
}

// TestCursorsCompareByTransactionFirst is the order docs/adr/0017 relies on.
func TestCursorsCompareByTransactionFirst(t *testing.T) {
	earlier := events.Cursor{Transaction: 9, Position: 100}
	later := events.Cursor{Transaction: 10, Position: 1}
	if !earlier.Before(later) || later.Before(earlier) {
		t.Error("a cursor from an older transaction does not come first")
	}
	if earlier.Before(earlier) {
		t.Error("a cursor comes before itself")
	}

	parsed, err := events.ParseCursor(later.String())
	if err != nil || parsed != later {
		t.Errorf("ParseCursor(%q) = %v, %v", later.String(), parsed, err)
	}
	if _, err := events.ParseCursor("18446744073709551616-1"); !errors.Is(err, events.ErrInvalidCursor) {
		t.Errorf("an overflowing transaction error = %v", err)
	}
}

/*
TestAPersonIsReplayedOnlyTheirRoomsAndTheirOwnPlace: what happened in the rooms
they are in now, and every change to their own place, and nothing else.
*/
func TestAPersonIsReplayedOnlyTheirRoomsAndTheirOwnPlace(t *testing.T) {
	inRoom := recorded(events.MessagePosted, "msg_1", 2, events.Data{"room_id": "room_a"})
	inRoom.CorrelationID = "req_1"
	leftElsewhere := recorded(events.MemberRemoved, "room_c", 4, events.Data{"user_id": "usr_ana"})
	otherTenant := recorded(events.MessagePosted, "msg_3", 6, events.Data{"room_id": "room_a"})
	otherTenant.ApplicationID = "app_2"
	replayer := &fakeReplayer{
		position: events.Cursor{Transaction: 10, Position: 6},
		kept: []events.Event{
			inRoom,
			recorded(events.MessagePosted, "msg_2", 3, events.Data{"room_id": "room_b"}),
			leftElsewhere,
			recorded(events.MemberAdded, "room_c", 5, events.Data{"user_id": "usr_bea"}),
			otherTenant,
		},
	}

	broker := events.NewBroker()
	handler := NewPersonHandler(slog.New(slog.NewTextHandler(io.Discard, nil)), broker, replayer,
		&switchableSession{}, &rooms{ids: []string{"room_a"}})
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		request.URL.RawQuery = "after=10-1"
		handler.Stream(response, request.WithContext(
			sessions.ContextWithPrincipal(request.Context(), signedIn("usr_ana"))))
	}))
	t.Cleanup(server.Close)

	connection, _, err := dialAsPerson(t, server)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	arrived, _ := incoming(connection)
	waitUntilSubscribed(t, broker)

	live := recorded(events.MessagePosted, "msg_4", 7, events.Data{"room_id": "room_a"})
	broker.Receive(live)

	for _, want := range []events.Event{inRoom, leftElsewhere, live} {
		select {
		case got := <-arrived:
			if got.ID != want.ID {
				t.Fatalf("received %s about %s, want %s about %s", got.Type, got.Subject.ID, want.Type, want.Subject.ID)
			}
			if got.CorrelationID != "" {
				t.Errorf("a replayed event names request %q", got.CorrelationID)
			}
		case <-time.After(waitFor):
			t.Fatalf("waited for %s", want.Type)
		}
	}
}
