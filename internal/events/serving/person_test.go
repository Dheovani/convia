package serving

import (
	"errors"
	"slices"
	"testing"
	"time"

	"convia/internal/sessions"

	"convia/internal/events"
)

// signedIn is a verified session for one of app_1's people.
func signedIn(userID string) sessions.Principal {
	return sessions.Principal{
		SessionID:     "ses_1",
		AccountID:     "acc_1",
		UserID:        userID,
		ApplicationID: "app_1",
	}
}

// listen opens a person's stream covering the named rooms.
func listen(t *testing.T, broker *events.Broker, userID string, rooms ...string) *events.Stream {
	t.Helper()

	stream, err := AsPerson(broker, signedIn(userID)).Subscribe()
	if err != nil {
		t.Fatalf("Subscribe() error = %v", err)
	}
	t.Cleanup(stream.Close)

	if err := stream.Reconcile(func() ([]string, error) { return rooms, nil }); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	return stream
}

// posted is a message said in a room by somebody other than the listener.
func posted(roomID string) events.Event {
	return events.New(events.MessagePosted, "app_1", "msg_in_"+roomID, "req_1", events.Data{
		"room_id":  roomID,
		"sequence": int64(1),
		"guest":    false,
		"user_id":  "usr_somebody",
	})
}

func added(roomID, userID string) events.Event {
	return events.New(events.MemberAdded, "app_1", roomID, "req_1", events.Data{"user_id": userID})
}

func removed(roomID, userID string) events.Event {
	return events.New(events.MemberRemoved, "app_1", roomID, "req_1", events.Data{"user_id": userID})
}

/*
TestAPersonHearsOnlyAboutTheirOwnRooms is the authorization M18-018 asked for.

An application's stream is authorized once, for a tenant. A person is not a
tenant, and a stream that carried the first-party application's whole traffic to
every signed-in person would tell each of them that conversations they are not
in are happening, and when.
*/
func TestAPersonHearsOnlyAboutTheirOwnRooms(t *testing.T) {
	broker := events.NewBroker()
	stream := listen(t, broker, "usr_ana", "room_a")

	broker.Publish(posted("room_b"))
	quiet(t, stream)

	broker.Publish(posted("room_a"))
	if got := receive(t, stream).Data["room_id"]; got != "room_a" {
		t.Errorf("the person received an event about %v", got)
	}
}

// TestAStreamNobodyReconciledCarriesNothing keeps the failure closed: a stream
// whose rooms were never read covers none, rather than every room.
func TestAStreamNobodyReconciledCarriesNothing(t *testing.T) {
	broker := events.NewBroker()

	stream, err := AsPerson(broker, signedIn("usr_ana")).Subscribe()
	if err != nil {
		t.Fatalf("Subscribe() error = %v", err)
	}
	defer stream.Close()

	broker.Publish(posted("room_a"))
	broker.Publish(added("room_a", "usr_bea"))
	quiet(t, stream)
}

/*
TestBeingAddedIsNewsAndOpensTheRoom is the case that needed an event at all.

Somebody added to a room was not in it a moment ago, so a rule that asked only
"is this person in the room" before delivering would withhold the one event that
tells them they now are â€” and everything said there afterwards.
*/
func TestBeingAddedIsNewsAndOpensTheRoom(t *testing.T) {
	broker := events.NewBroker()
	stream := listen(t, broker, "usr_ana")

	broker.Publish(added("room_a", "usr_ana"))
	if got := receive(t, stream).Type; got != events.MemberAdded {
		t.Fatalf("the person received %q, want to hear they were added", got)
	}

	broker.Publish(posted("room_a"))
	if got := receive(t, stream).Type; got != events.MessagePosted {
		t.Errorf("after being added the person received %q", got)
	}
}

// TestBeingRemovedIsNewsAndClosesTheRoom is the mirror: the removal arrives,
// and nothing said in the room afterwards does.
func TestBeingRemovedIsNewsAndClosesTheRoom(t *testing.T) {
	broker := events.NewBroker()
	stream := listen(t, broker, "usr_ana", "room_a")

	broker.Publish(removed("room_a", "usr_ana"))
	if got := receive(t, stream).Type; got != events.MemberRemoved {
		t.Fatalf("the person received %q, want to hear they were removed", got)
	}

	broker.Publish(posted("room_a"))
	quiet(t, stream)
}

// TestSomebodyElsesPlaceIsNewsOnlyInsideTheRoom covers the member list: who
// joined a room is something its members can read, and nobody else can.
func TestSomebodyElsesPlaceIsNewsOnlyInsideTheRoom(t *testing.T) {
	broker := events.NewBroker()
	stream := listen(t, broker, "usr_ana", "room_a")

	broker.Publish(added("room_b", "usr_bea"))
	broker.Publish(removed("room_b", "usr_bea"))
	quiet(t, stream)

	broker.Publish(added("room_a", "usr_bea"))
	if got := receive(t, stream).Data["user_id"]; got != "usr_bea" {
		t.Errorf("the person heard about %v joining", got)
	}

	// Somebody else joining a room does not change which rooms this stream
	// covers.
	broker.Publish(added("room_c", "usr_bea"))
	broker.Publish(posted("room_c"))
	quiet(t, stream)
}

/*
TestAPersonIsNotToldWhatTheyCannotRead keeps the stream from being a second way
to learn what the session surface does not expose.

An invitation being declined can name a room, so a filter by room alone would let
it through. Nothing a person can reach reads invitations to a call, and the type
set is what refuses it. A participant event that names no room reaches nobody,
whatever its type.
*/
func TestAPersonIsNotToldWhatTheyCannotRead(t *testing.T) {
	broker := events.NewBroker()
	stream := listen(t, broker, "usr_ana", "room_a")

	broker.Publish(events.New(events.InvitationDeclined, "app_1", "inv_1", "req_1", events.Data{"room_id": "room_a"}))
	broker.Publish(events.New(events.PresenceChanged, "app_1", "usr_bea", "req_1", events.Data{"state": "online"}))
	broker.Publish(events.New(events.ParticipantJoined, "app_1", "part_1", "req_1", events.Data{"call_id": "call_1"}))
	quiet(t, stream)
}

// TestAPersonNeverHearsAnotherTenant pins that a room identifier alone is not
// enough: the application still has to be the session's.
func TestAPersonNeverHearsAnotherTenant(t *testing.T) {
	broker := events.NewBroker()
	stream := listen(t, broker, "usr_ana", "room_a")

	elsewhere := posted("room_a")
	elsewhere.ApplicationID = "app_2"
	broker.Publish(elsewhere)

	quiet(t, stream)
}

/*
TestAChangeDuringAReadIsNotLost is the race Reconcile exists to close.

The read of somebody's rooms is a query, and a membership can change while it
runs. Whichever side of the read the change committed on, the stream has to end
up covering the rooms the person is in after it. The read here returns what was
true before both changes, which is the worse of the two possibilities.
*/
func TestAChangeDuringAReadIsNotLost(t *testing.T) {
	broker := events.NewBroker()
	stream := listen(t, broker, "usr_ana", "room_a")

	err := stream.Reconcile(func() ([]string, error) {
		broker.Publish(removed("room_a", "usr_ana"))
		broker.Publish(added("room_b", "usr_ana"))
		return []string{"room_a"}, nil
	})
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}

	for range 2 {
		receive(t, stream)
	}

	broker.Publish(posted("room_a"))
	quiet(t, stream)

	broker.Publish(posted("room_b"))
	if got := receive(t, stream).Data["room_id"]; got != "room_b" {
		t.Errorf("the person received an event about %v", got)
	}
}

// TestAFailedReadKeepsWhatTheStreamCovered is the other half of Reconcile: a
// read that fails is not a reason to go silent.
func TestAFailedReadKeepsWhatTheStreamCovered(t *testing.T) {
	broker := events.NewBroker()
	stream := listen(t, broker, "usr_ana", "room_a")

	unreachable := errors.New("the database is unreachable")
	if err := stream.Reconcile(func() ([]string, error) { return nil, unreachable }); !errors.Is(err, unreachable) {
		t.Fatalf("Reconcile() error = %v, want %v", err, unreachable)
	}

	broker.Publish(posted("room_a"))
	receive(t, stream)
}

// TestAPersonIsNotToldWhichRequestCausedSomething covers the one field of the
// envelope a person's stream leaves out, and that an application's keeps.
func TestAPersonIsNotToldWhichRequestCausedSomething(t *testing.T) {
	broker := events.NewBroker()
	person := listen(t, broker, "usr_ana", "room_a")

	backend, err := broker.Subscribe("app_1", events.Types())
	if err != nil {
		t.Fatalf("Subscribe() error = %v", err)
	}
	defer backend.Close()

	event := posted("room_a")
	if got := person.Outgoing(event).CorrelationID; got != "" {
		t.Errorf("a person was told request %q caused the event", got)
	}
	if got := backend.Outgoing(event).CorrelationID; got != event.CorrelationID {
		t.Errorf("an application was told request %q, want %q", got, event.CorrelationID)
	}
}

/*
TestEveryTypeAPersonHearsHappensInARoom keeps the two lists in step.

A person's stream admits an event by the room it names, so a type added to what
a person hears without teaching roomOf where its room is would be delivered to
nobody â€” silently, which is the kind of failure a test is for.
*/
func TestEveryTypeAPersonHearsHappensInARoom(t *testing.T) {
	for _, kind := range personTypes() {
		event := events.New(kind, "app_1", "room_1", "", events.Data{"room_id": "room_1", "user_id": "usr_1"})
		if _, scoped := events.RoomOf(event); !scoped {
			t.Errorf("%s is carried to people but names no room they could be in", kind)
		}
	}

	for _, kind := range events.Types() {
		if slices.Contains(personTypes(), kind) {
			continue
		}
		event := events.New(kind, "app_1", "room_1", "", events.Data{"room_id": "room_1"})
		if _, scoped := events.RoomOf(event); scoped {
			t.Errorf("%s names a room for a person's stream but is not one of the types it carries", kind)
		}
	}
}

/*
TestARoomsChangesReachOnlyThoseInIt is M18-029: a room its owner renamed, closed
or deleted is news to everybody else in it, and to nobody outside it. Deleting it
is the last they hear of it.
*/
func TestARoomsChangesReachOnlyThoseInIt(t *testing.T) {
	broker := events.NewBroker()
	inside := listen(t, broker, "usr_ana", "room_a")
	outside := listen(t, broker, "usr_bea", "room_b")

	for _, kind := range []events.Type{events.RoomUpdated, events.RoomClosed, events.RoomReopened} {
		broker.Publish(events.New(kind, "app_1", "room_a", "req_1", nil))
		if got := receive(t, inside).Type; got != kind {
			t.Errorf("the person in the room received %q, want %q", got, kind)
		}
	}
	quiet(t, outside)

	broker.Publish(events.New(events.RoomDeleted, "app_1", "room_a", "req_1", nil))
	if got := receive(t, inside).Type; got != events.RoomDeleted {
		t.Fatalf("the person in the room received %q, want to hear it was deleted", got)
	}
	broker.Publish(posted("room_a"))
	quiet(t, inside)
	quiet(t, outside)
}

// waitFor is how long a test waits for something that should already have
// happened, before deciding it never will.
const waitFor = 2 * time.Second

// receive takes the next event from a stream, or fails the test.
func receive(t *testing.T, stream *events.Stream) events.Event {
	t.Helper()

	select {
	case event := <-stream.Events():
		return event
	case <-time.After(waitFor):
		t.Fatal("no event arrived")
		return events.Event{}
	}
}

// quiet fails if anything arrives on a stream.
func quiet(t *testing.T, stream *events.Stream) {
	t.Helper()

	select {
	case event := <-stream.Events():
		t.Fatalf("an event arrived that should not have: %s about %s", event.Type, event.Subject.ID)
	case <-time.After(50 * time.Millisecond):
	}
}
