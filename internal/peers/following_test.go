package peers

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"convia/internal/accounts"
	"convia/internal/events"
	"convia/internal/webhooks"
)

// waitFor bounds every wait in this file, so a broken expectation fails rather
// than hangs.
const waitFor = 3 * time.Second

// listing is the rooms elsewhere an account belongs to, which a test can change
// while streams are open.
type listing struct {
	mutex sync.Mutex
	rooms []RemoteRoom
	err   error
}

func (known *listing) RemoteRooms(context.Context, string) ([]RemoteRoom, error) {
	known.mutex.Lock()
	defer known.mutex.Unlock()
	return known.rooms, known.err
}

func (known *listing) become(rooms ...RemoteRoom) {
	known.mutex.Lock()
	defer known.mutex.Unlock()
	known.rooms = rooms
}

// openKey hands out a key, the way a session does for a person who is signed in.
type openKey struct{ identity accounts.Identity }

func (key openKey) Identity(context.Context, string) (accounts.Identity, error) {
	return key.identity, nil
}

func (openKey) Account(context.Context, string) (accounts.Account, error) {
	return accounts.Account{}, nil
}

/*
standIn is another installation serving the visitor stream.

It records the signature headers it was handed, because the point of a stream
between installations is that it is signed like everything else, and it answers
with whatever a test puts on `saying`.
*/
type standIn struct {
	server *httptest.Server
	saying chan events.Event

	mutex  sync.Mutex
	signed []string
}

func newStandIn(t *testing.T) *standIn {
	t.Helper()

	home := &standIn{saying: make(chan events.Event, 8)}
	home.server = httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		home.mutex.Lock()
		home.signed = append(home.signed, request.Header.Get(HeaderAccount))
		home.mutex.Unlock()

		connection, err := websocket.Accept(response, request, nil)
		if err != nil {
			return
		}
		defer connection.CloseNow()

		for {
			select {
			case <-request.Context().Done():
				return
			case event := <-home.saying:
				if err := wsjson.Write(request.Context(), connection, event); err != nil {
					return
				}
			}
		}
	}))
	t.Cleanup(home.server.Close)
	return home
}

func (home *standIn) address() string { return home.server.URL }

func (home *standIn) signers() []string {
	home.mutex.Lock()
	defer home.mutex.Unlock()
	return append([]string(nil), home.signed...)
}

func following(t *testing.T, rooms *listing) *Following {
	t.Helper()

	identity, err := accounts.NewIdentity()
	if err != nil {
		t.Fatalf("NewIdentity() error = %v", err)
	}
	/*
		The real client, with the guard set to a development instance.

		The stand-in home is on loopback, which the guard refuses everywhere
		else, and dialling it any other way would take the signature out of the
		path being tested — which is most of what a stream between installations
		is.
	*/
	follow := NewFollowing(rooms, openKey{identity}, NewClient(webhooks.NewDestinations(true)),
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	follow.every = 20 * time.Millisecond
	return follow
}

func said(room string) events.Event {
	return events.Event{
		Type:   events.MessagePosted,
		Data:   events.Data{"room_id": room, "message_id": "msg_7KQZP4XN2VJH6TBWMDR3YAFC5E"},
		Cursor: "the home's own position",
	}
}

/*
TestWhatAHomeSaysArrivesUnderTheNameThisInstallationKnows is the translation
this whole arrangement rests on.

A home names its own rooms and knows nothing of the pointer kept here. An event
forwarded as it arrived would name a room the person's own interface has never
heard of, so the room it names is rewritten before anybody sees it.
*/
func TestWhatAHomeSaysArrivesUnderTheNameThisInstallationKnows(t *testing.T) {
	home := newStandIn(t)
	rooms := &listing{rooms: []RemoteRoom{{
		ID: sampleRemoteID, Home: home.address(), RoomID: "room_7KQZP4XN2VJH6TBWMDR3YAFC5E", Name: "Their room",
	}}}

	ctx, stop := context.WithCancel(context.Background())
	defer stop()

	arriving, err := following(t, rooms).Follow(ctx, "acc_7KQZP4XN2VJH6TBWMDR3YAFC5E", "cvs_token")
	if err != nil {
		t.Fatalf("Follow() error = %v", err)
	}

	home.saying <- said("room_7KQZP4XN2VJH6TBWMDR3YAFC5E")

	select {
	case event := <-arriving:
		if event.Data["room_id"] != sampleRemoteID {
			t.Errorf("the event names %v, want the pointer this installation keeps", event.Data["room_id"])
		}
		if event.Cursor != "" {
			t.Errorf("the home's cursor was passed on: %q", event.Cursor)
		}
	case <-time.After(waitFor):
		t.Fatal("nothing arrived from the home")
	}

	/*
		And the home was told who was asking.

		A stream is one request that stays open, and it is signed like every
		other one: a handshake carrying no account is one a home would refuse,
		so a test that only watched events arrive would pass against a stand-in
		that asks for nothing.
	*/
	if signed := home.signers(); len(signed) != 1 || signed[0] == "" {
		t.Errorf("the handshake was signed by %v, want the person's own account", signed)
	}
}

// TestARoomWithNoPointerIsNotPassedOn: a home may say anything, and what this
// person is not in is not theirs to be told about.
func TestARoomWithNoPointerIsNotPassedOn(t *testing.T) {
	home := newStandIn(t)
	rooms := &listing{rooms: []RemoteRoom{{
		ID: sampleRemoteID, Home: home.address(), RoomID: "room_7KQZP4XN2VJH6TBWMDR3YAFC5E",
	}}}

	ctx, stop := context.WithCancel(context.Background())
	defer stop()

	arriving, err := following(t, rooms).Follow(ctx, "acc_7KQZP4XN2VJH6TBWMDR3YAFC5E", "cvs_token")
	if err != nil {
		t.Fatalf("Follow() error = %v", err)
	}

	home.saying <- said("room_SOMEBODYELSESROOMAAAAAAAAA")
	home.saying <- said("room_7KQZP4XN2VJH6TBWMDR3YAFC5E")

	select {
	case event := <-arriving:
		if event.Data["room_id"] != sampleRemoteID {
			t.Errorf("a room this person is not in was passed on: %v", event.Data["room_id"])
		}
	case <-time.After(waitFor):
		t.Fatal("nothing arrived from the home")
	}
}

/*
TestAHomeJoinedWhileTheStreamIsOpenIsFollowed is the case that made this read
the rooms again rather than once.

Accepting an invitation puts somebody in a room on an installation they had
nothing on a moment ago, and their stream is already open. Without this they
would hear nothing from it until they reconnected.
*/
func TestAHomeJoinedWhileTheStreamIsOpenIsFollowed(t *testing.T) {
	first := newStandIn(t)
	second := newStandIn(t)

	here := RemoteRoom{ID: sampleRemoteID, Home: first.address(), RoomID: "room_7KQZP4XN2VJH6TBWMDR3YAFC5E"}
	rooms := &listing{rooms: []RemoteRoom{here}}

	ctx, stop := context.WithCancel(context.Background())
	defer stop()

	arriving, err := following(t, rooms).Follow(ctx, "acc_7KQZP4XN2VJH6TBWMDR3YAFC5E", "cvs_token")
	if err != nil {
		t.Fatalf("Follow() error = %v", err)
	}

	joined := RemoteRoom{ID: "rrm_4XZQP7KN2VJH6TBWMDR3YAFC5E", Home: second.address(),
		RoomID: "room_4XZQP7KN2VJH6TBWMDR3YAFC5E"}
	rooms.become(here, joined)

	deadline := time.After(waitFor)
	for {
		select {
		case second.saying <- said(joined.RoomID):
		case event := <-arriving:
			if event.Data["room_id"] == joined.ID {
				return
			}
		case <-deadline:
			t.Fatal("the installation joined while the stream was open was never followed")
		}
	}
}

// TestNobodyWithRoomsNowhereOpensAnything: the cost of this for everybody it
// does not concern is nothing at all, not an idle connection.
func TestNobodyWithRoomsNowhereOpensAnything(t *testing.T) {
	arriving, err := following(t, &listing{}).Follow(context.Background(),
		"acc_7KQZP4XN2VJH6TBWMDR3YAFC5E", "cvs_token")
	if err != nil {
		t.Fatalf("Follow() error = %v", err)
	}
	if arriving != nil {
		t.Error("a stream was opened for somebody who is in no room elsewhere")
	}
}

// TestTheStreamStopsWhenThePersonsDoes: the streams belong to the connection
// that started them, so nothing is left running behind a person who has gone.
func TestTheStreamStopsWhenThePersonsDoes(t *testing.T) {
	home := newStandIn(t)
	rooms := &listing{rooms: []RemoteRoom{{
		ID: sampleRemoteID, Home: home.address(), RoomID: "room_7KQZP4XN2VJH6TBWMDR3YAFC5E",
	}}}

	ctx, stop := context.WithCancel(context.Background())
	arriving, err := following(t, rooms).Follow(ctx, "acc_7KQZP4XN2VJH6TBWMDR3YAFC5E", "cvs_token")
	if err != nil {
		t.Fatalf("Follow() error = %v", err)
	}

	stop()

	select {
	case _, open := <-arriving:
		if open {
			// Whatever was in flight arrives first; the close is what matters.
			select {
			case _, open := <-arriving:
				if open {
					t.Error("the channel stayed open after the person's stream ended")
				}
			case <-time.After(waitFor):
				t.Error("the channel was never closed")
			}
		}
	case <-time.After(waitFor):
		t.Error("the channel was never closed")
	}
}
