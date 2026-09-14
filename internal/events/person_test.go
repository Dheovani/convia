package events

import (
	"errors"
	"slices"
	"testing"

	"convia/internal/sessions"
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
func listen(t *testing.T, broker *Broker, userID string, rooms ...string) *Stream {
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
func posted(roomID string) Event {
	return New(MessagePosted, "app_1", "msg_in_"+roomID, "req_1", Data{
		"room_id":  roomID,
		"sequence": int64(1),
		"guest":    false,
		"user_id":  "usr_somebody",
	})
}

func added(roomID, userID string) Event {
	return New(MemberAdded, "app_1", roomID, "req_1", Data{"user_id": userID})
}

func removed(roomID, userID string) Event {
	return New(MemberRemoved, "app_1", roomID, "req_1", Data{"user_id": userID})
}

/*
TestAPersonHearsOnlyAboutTheirOwnRooms is the authorization M18-018 asked for.

An application's stream is authorized once, for a tenant. A person is not a
tenant, and a stream that carried the first-party application's whole traffic to
every signed-in person would tell each of them that conversations they are not
in are happening, and when.
*/
func TestAPersonHearsOnlyAboutTheirOwnRooms(t *testing.T) {
	broker := NewBroker()
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
	broker := NewBroker()

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
tells them they now are — and everything said there afterwards.
*/
func TestBeingAddedIsNewsAndOpensTheRoom(t *testing.T) {
	broker := NewBroker()
	stream := listen(t, broker, "usr_ana")

	broker.Publish(added("room_a", "usr_ana"))
	if got := receive(t, stream).Type; got != MemberAdded {
		t.Fatalf("the person received %q, want to hear they were added", got)
	}

	broker.Publish(posted("room_a"))
	if got := receive(t, stream).Type; got != MessagePosted {
		t.Errorf("after being added the person received %q", got)
	}
}

// TestBeingRemovedIsNewsAndClosesTheRoom is the mirror: the removal arrives,
// and nothing said in the room afterwards does.
func TestBeingRemovedIsNewsAndClosesTheRoom(t *testing.T) {
	broker := NewBroker()
	stream := listen(t, broker, "usr_ana", "room_a")

	broker.Publish(removed("room_a", "usr_ana"))
	if got := receive(t, stream).Type; got != MemberRemoved {
		t.Fatalf("the person received %q, want to hear they were removed", got)
	}

	broker.Publish(posted("room_a"))
	quiet(t, stream)
}

// TestSomebodyElsesPlaceIsNewsOnlyInsideTheRoom covers the member list: who
// joined a room is something its members can read, and nobody else can.
func TestSomebodyElsesPlaceIsNewsOnlyInsideTheRoom(t *testing.T) {
	broker := NewBroker()
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
	broker := NewBroker()
	stream := listen(t, broker, "usr_ana", "room_a")

	broker.Publish(New(InvitationDeclined, "app_1", "inv_1", "req_1", Data{"room_id": "room_a"}))
	broker.Publish(New(PresenceChanged, "app_1", "usr_bea", "req_1", Data{"state": "online"}))
	broker.Publish(New(ParticipantJoined, "app_1", "part_1", "req_1", Data{"call_id": "call_1"}))
	quiet(t, stream)
}

// TestAPersonNeverHearsAnotherTenant pins that a room identifier alone is not
// enough: the application still has to be the session's.
func TestAPersonNeverHearsAnotherTenant(t *testing.T) {
	broker := NewBroker()
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
	broker := NewBroker()
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
	broker := NewBroker()
	stream := listen(t, broker, "usr_ana", "room_a")

	unreachable := errors.New("the database is unreachable")
	if err := stream.Reconcile(func() ([]string, error) { return nil, unreachable }); !errors.Is(err, unreachable) {
		t.Fatalf("Reconcile() error = %v, want %v", err, unreachable)
	}

	broker.Publish(posted("room_a"))
	receive(t, stream)
}

/*
TestPeopleDoNotSpendAnApplicationsStreams keeps Convia's own product from
crowding out the backends of the application it is.

The first-party application is an application like any other, with eight
streams for its backend. Were a person's tab counted against those, the eighth
browser tab would refuse the backend its stream.
*/
func TestPeopleDoNotSpendAnApplicationsStreams(t *testing.T) {
	broker := NewBroker()

	for range MaxStreamsPerPerson {
		listen(t, broker, "usr_ana")
	}

	if _, err := AsPerson(broker, signedIn("usr_ana")).Subscribe(); !errors.Is(err, ErrTooManyStreams) {
		t.Errorf("one person opened more than %d streams: error = %v", MaxStreamsPerPerson, err)
	}

	other := listen(t, broker, "usr_bea")
	backend, err := broker.Subscribe("app_1", Types())
	if err != nil {
		t.Fatalf("an application was refused its stream by its people's: %v", err)
	}
	defer backend.Close()

	/*
		Ending a person's stream frees a place for that person, and only that
		count moves, which is what a leak between the two would break.
	*/
	other.Close()
	if broker.people != MaxStreamsPerPerson || broker.applications != 1 {
		t.Errorf("counts are %d people and %d applications, want %d and 1",
			broker.people, broker.applications, MaxStreamsPerPerson)
	}
}

// TestAPersonIsNotToldWhichRequestCausedSomething covers the one field of the
// envelope a person's stream leaves out, and that an application's keeps.
func TestAPersonIsNotToldWhichRequestCausedSomething(t *testing.T) {
	broker := NewBroker()
	person := listen(t, broker, "usr_ana", "room_a")

	backend, err := broker.Subscribe("app_1", Types())
	if err != nil {
		t.Fatalf("Subscribe() error = %v", err)
	}
	defer backend.Close()

	event := posted("room_a")
	if got := person.outgoing(event).CorrelationID; got != "" {
		t.Errorf("a person was told request %q caused the event", got)
	}
	if got := backend.outgoing(event).CorrelationID; got != event.CorrelationID {
		t.Errorf("an application was told request %q, want %q", got, event.CorrelationID)
	}
}

/*
TestEveryTypeAPersonHearsHappensInARoom keeps the two lists in step.

A person's stream admits an event by the room it names, so a type added to what
a person hears without teaching roomOf where its room is would be delivered to
nobody — silently, which is the kind of failure a test is for.
*/
func TestEveryTypeAPersonHearsHappensInARoom(t *testing.T) {
	for _, kind := range personTypes() {
		event := New(kind, "app_1", "room_1", "", Data{"room_id": "room_1", "user_id": "usr_1"})
		if _, scoped := roomOf(event); !scoped {
			t.Errorf("%s is carried to people but names no room they could be in", kind)
		}
	}

	for _, kind := range Types() {
		if slices.Contains(personTypes(), kind) {
			continue
		}
		event := New(kind, "app_1", "room_1", "", Data{"room_id": "room_1"})
		if _, scoped := roomOf(event); scoped {
			t.Errorf("%s names a room for a person's stream but is not one of the types it carries", kind)
		}
	}
}
