package participants

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"

	"convia/internal/calls"
	"convia/internal/media"
	"convia/internal/rooms"
	"convia/internal/sessions"
)

// personalRoom opens a room the way a person does, with its owner in it, and
// gives everybody else named a place.
func (setup fixture) personalRoom(t *testing.T, applicationID, ownerID string, members ...string) rooms.Room {
	t.Helper()
	ctx := context.Background()

	room, err := setup.rooms.CreateFor(ctx, applicationID, ownerID, rooms.Definition{Name: "Standup"})
	if err != nil {
		t.Fatalf("open a room: %v", err)
	}

	for _, member := range members {
		if _, _, err := setup.rooms.AddMember(ctx, applicationID, room.ID, member); err != nil {
			t.Fatalf("add %s to the room: %v", member, err)
		}
	}
	return room
}

// seat joins a person to a room's call and fails the test if it could not.
func (setup fixture) seat(t *testing.T, applicationID, roomID, userID string, role Role) Seat {
	t.Helper()

	seat, _, err := setup.service.JoinRoom(context.Background(), applicationID, roomID, userID, role)
	if err != nil {
		t.Fatalf("JoinRoom() error = %v", err)
	}
	return seat
}

func (setup fixture) callNow(t *testing.T, applicationID, callID string) calls.Call {
	t.Helper()

	call, err := setup.calls.Get(context.Background(), applicationID, callID)
	if err != nil {
		t.Fatalf("read the call: %v", err)
	}
	return call
}

func (setup fixture) participantNow(t *testing.T, applicationID, id string) Participant {
	t.Helper()

	participant, err := setup.service.Get(context.Background(), applicationID, id)
	if err != nil {
		t.Fatalf("read the participant: %v", err)
	}
	return participant
}

// reportAbout is what the media plane would say about a call's session.
func reportAbout(call calls.Call, kind media.ReportKind, participantID string) media.Report {
	return media.Report{
		Kind:          kind,
		Session:       media.Session{Reference: "room-for-" + call.ID},
		ParticipantID: participantID,
	}
}

func endedBy(call calls.Call) calls.Actor {
	if call.EndedBy == nil {
		return ""
	}
	return *call.EndedBy
}

/*
TestStartingACallInARoomSeatsWhoeverStartedIt is the ordinary path of M18-004.

Pressing the button in a quiet room starts a call with you in it, and the next
person to press it is in the same call.
*/
func TestStartingACallInARoomSeatsWhoeverStartedIt(t *testing.T) {
	setup, plane := newMediaFixture(t)
	ctx := context.Background()

	ana := setup.newUser(t, setup.first, "ana")
	bea := setup.newUser(t, setup.first, "bea")
	room := setup.personalRoom(t, setup.first, ana, bea)

	first, started, err := setup.service.JoinRoom(ctx, setup.first, room.ID, ana, RoleModerator)
	if err != nil {
		t.Fatalf("JoinRoom() error = %v", err)
	}
	if !started {
		t.Error("the first person in a quiet room did not start a call")
	}
	if first.Call.StartedBy != calls.ActorPerson {
		t.Errorf("the call was started by %q, want %q", first.Call.StartedBy, calls.ActorPerson)
	}
	if first.Participant.UserID != ana || !first.Participant.Present() || first.Participant.Role != RoleModerator {
		t.Errorf("the starter was seated as %+v, want present as the moderator", first.Participant)
	}
	if !first.Credential.Issued() {
		t.Error("the starter was given nothing to connect with")
	}

	second, startedAgain, err := setup.service.JoinRoom(ctx, setup.first, room.ID, bea, RoleMember)
	if err != nil {
		t.Fatalf("JoinRoom() for the second person error = %v", err)
	}
	if startedAgain {
		t.Error("joining a call that was already running reported starting one")
	}
	if second.Call.ID != first.Call.ID {
		t.Errorf("the second person is in %s, want the call already running (%s)", second.Call.ID, first.Call.ID)
	}

	if got := len(plane.admissions()); got != 2 {
		t.Errorf("the media plane admitted %d people, want 2", got)
	}
}

// TestPeopleStartingACallAtOnceEndUpInTheSameOne is the race the one-call-per-room
// rule settles, seen from the people it settles it for.
func TestPeopleStartingACallAtOnceEndUpInTheSameOne(t *testing.T) {
	setup, _ := newMediaFixture(t)
	ctx := context.Background()

	owner := setup.newUser(t, setup.first, "owner")
	people := make([]string, 8)
	for index := range people {
		people[index] = setup.newUser(t, setup.first, "racer-"+string(rune('a'+index)))
	}
	room := setup.personalRoom(t, setup.first, owner, people...)

	var (
		start    sync.WaitGroup
		finished sync.WaitGroup
		mutex    sync.Mutex
		failures []error
		starts   int
	)
	seated := make(map[string]int)

	start.Add(1)
	for _, person := range people {
		finished.Add(1)
		go func() {
			defer finished.Done()
			start.Wait()

			seat, started, err := setup.service.JoinRoom(ctx, setup.first, room.ID, person, RoleMember)

			mutex.Lock()
			defer mutex.Unlock()
			if err != nil {
				failures = append(failures, err)
				return
			}
			seated[seat.Call.ID]++
			if started {
				starts++
			}
		}()
	}
	start.Done()
	finished.Wait()

	if len(failures) > 0 {
		t.Fatalf("simultaneous joins failed: %v", failures)
	}
	if len(seated) != 1 {
		t.Errorf("simultaneous joins seated people in %d calls, want one: %v", len(seated), seated)
	}
	if starts != 1 {
		t.Errorf("%d joins reported starting the call, want exactly one", starts)
	}
}

/*
TestACallEndsWhenItsLastParticipantLeaves is how a call in Convia's own product
ends: nobody ends it for everybody.
*/
func TestACallEndsWhenItsLastParticipantLeaves(t *testing.T) {
	setup, plane := newMediaFixture(t)
	ctx := context.Background()

	ana := setup.newUser(t, setup.first, "ana")
	bea := setup.newUser(t, setup.first, "bea")
	room := setup.personalRoom(t, setup.first, ana, bea)

	anaSeat := setup.seat(t, setup.first, room.ID, ana, RoleModerator)
	beaSeat := setup.seat(t, setup.first, room.ID, bea, RoleMember)

	if err := setup.service.LeaveRoom(ctx, setup.first, room.ID, ana, calls.ActorPerson); err != nil {
		t.Fatalf("LeaveRoom() error = %v", err)
	}
	if setup.callNow(t, setup.first, anaSeat.Call.ID).Ended() {
		t.Fatal("the call ended while somebody was still in it")
	}
	if got := setup.participantNow(t, setup.first, anaSeat.Participant.ID).Status; got != StatusLeft {
		t.Errorf("the person who left is %q, want %q", got, StatusLeft)
	}

	if err := setup.service.LeaveRoom(ctx, setup.first, room.ID, bea, calls.ActorPerson); err != nil {
		t.Fatalf("LeaveRoom() for the last person error = %v", err)
	}

	ended := setup.callNow(t, setup.first, anaSeat.Call.ID)
	if !ended.Ended() || endedBy(ended) != calls.ActorPerson || ended.EndReason != emptyReason {
		t.Errorf("after the last person left the call is %q, ended by %q because %q; want it ended by a person",
			ended.Status, endedBy(ended), ended.EndReason)
	}

	for _, participantID := range []string{anaSeat.Participant.ID, beaSeat.Participant.ID} {
		if !slices.Contains(plane.disconnections(), participantID) {
			t.Errorf("%s left the call and was not disconnected from it", participantID)
		}
	}

	if err := setup.service.LeaveRoom(ctx, setup.first, room.ID, bea, calls.ActorPerson); err != nil {
		t.Errorf("leaving a call that is over error = %v, want it to change nothing", err)
	}
}

// TestAnApplicationsCallDoesNotEndWhenItEmpties keeps the rule to Convia's own
// product: an application decides when its calls end.
func TestAnApplicationsCallDoesNotEndWhenItEmpties(t *testing.T) {
	setup, plane := newMediaFixture(t)
	ctx := context.Background()

	call := setup.newCall(t, setup.first, nil)
	participant := setup.join(t, setup.first, call.ID, setup.newUser(t, setup.first, "ana"), RoleMember)

	if _, err := setup.service.Leave(ctx, setup.first, participant.ID); err != nil {
		t.Fatalf("Leave() error = %v", err)
	}

	if setup.callNow(t, setup.first, call.ID).Ended() {
		t.Error("an application's call ended because its last participant left")
	}
	if !slices.Contains(plane.disconnections(), participant.ID) {
		t.Error("somebody an application says has left is still connected")
	}
}

/*
TestLosingYourPlaceInARoomTakesYouOutOfItsCall covers the ways a person stops
being in a room while its call is running: leaving it or being removed from it,
which are one act for a room, and being banned.
*/
func TestLosingYourPlaceInARoomTakesYouOutOfItsCall(t *testing.T) {
	losses := map[string]func(setup fixture, roomID, userID string) error{
		"leaving or being removed": func(setup fixture, roomID, userID string) error {
			_, err := setup.rooms.RemoveMember(context.Background(), setup.first, roomID, userID)
			return err
		},
		"being banned": func(setup fixture, roomID, userID string) error {
			_, err := setup.rooms.Ban(context.Background(), setup.first, roomID, userID)
			return err
		},
	}

	for name, lose := range losses {
		t.Run(name, func(t *testing.T) {
			setup, plane := newMediaFixture(t)

			ana := setup.newUser(t, setup.first, "ana")
			bea := setup.newUser(t, setup.first, "bea")
			room := setup.personalRoom(t, setup.first, ana, bea)

			anaSeat := setup.seat(t, setup.first, room.ID, ana, RoleModerator)
			beaSeat := setup.seat(t, setup.first, room.ID, bea, RoleMember)

			if err := lose(setup, room.ID, bea); err != nil {
				t.Fatalf("take the place away: %v", err)
			}

			if setup.participantNow(t, setup.first, beaSeat.Participant.ID).Present() {
				t.Error("somebody with no place in the room is still in its call")
			}
			if !slices.Contains(plane.disconnections(), beaSeat.Participant.ID) {
				t.Error("somebody with no place in the room is still connected to its call")
			}
			if setup.callNow(t, setup.first, anaSeat.Call.ID).Ended() {
				t.Error("the call ended while its owner was still in it")
			}
		})
	}
}

// TestACallWhoseLastPersonLosesTheirPlaceIsEndedByConvia records the ending as
// nobody's request.
func TestACallWhoseLastPersonLosesTheirPlaceIsEndedByConvia(t *testing.T) {
	setup, _ := newMediaFixture(t)
	ctx := context.Background()

	ana := setup.newUser(t, setup.first, "ana")
	bea := setup.newUser(t, setup.first, "bea")
	room := setup.personalRoom(t, setup.first, ana, bea)

	seat := setup.seat(t, setup.first, room.ID, bea, RoleMember)

	if _, err := setup.rooms.Ban(ctx, setup.first, room.ID, bea); err != nil {
		t.Fatalf("Ban() error = %v", err)
	}

	ended := setup.callNow(t, setup.first, seat.Call.ID)
	if !ended.Ended() || endedBy(ended) != calls.ActorSystem {
		t.Errorf("the call is %q and ended by %q, want it ended by Convia", ended.Status, endedBy(ended))
	}
}

// TestAnApplicationsCallKeepsSomebodyWhoLeftTheRoom keeps membership from
// deciding who is in an application's calls, which it never has.
func TestAnApplicationsCallKeepsSomebodyWhoLeftTheRoom(t *testing.T) {
	setup, _ := newMediaFixture(t)
	ctx := context.Background()

	call := setup.newCall(t, setup.first, nil)
	ana := setup.newUser(t, setup.first, "ana")
	if _, _, err := setup.rooms.AddMember(ctx, setup.first, call.RoomID, ana); err != nil {
		t.Fatalf("AddMember() error = %v", err)
	}
	participant := setup.join(t, setup.first, call.ID, ana, RoleMember)

	if _, err := setup.rooms.RemoveMember(ctx, setup.first, call.RoomID, ana); err != nil {
		t.Fatalf("RemoveMember() error = %v", err)
	}

	if !setup.participantNow(t, setup.first, participant.ID).Present() {
		t.Error("an application's participant was taken out of its call by a change of membership")
	}
}

// TestDeletingARoomEndsTheCallItWasHolding holds for every room, whoever opened
// it: nobody can find a call in a room that is gone.
func TestDeletingARoomEndsTheCallItWasHolding(t *testing.T) {
	t.Run("a room a person opened", func(t *testing.T) {
		setup, _ := newMediaFixture(t)

		ana := setup.newUser(t, setup.first, "ana")
		room := setup.personalRoom(t, setup.first, ana)
		seat := setup.seat(t, setup.first, room.ID, ana, RoleModerator)

		if err := setup.rooms.Delete(context.Background(), setup.first, room.ID); err != nil {
			t.Fatalf("Delete() error = %v", err)
		}

		ended := setup.callNow(t, setup.first, seat.Call.ID)
		if !ended.Ended() || endedBy(ended) != calls.ActorSystem || ended.EndReason != deletedRoomReason {
			t.Errorf("the call is %q, ended by %q because %q; want it ended by Convia with the room",
				ended.Status, endedBy(ended), ended.EndReason)
		}
	})

	t.Run("a room an application created", func(t *testing.T) {
		setup, _ := newMediaFixture(t)

		call := setup.newCall(t, setup.first, nil)
		if err := setup.rooms.Delete(context.Background(), setup.first, call.RoomID); err != nil {
			t.Fatalf("Delete() error = %v", err)
		}

		if ended := setup.callNow(t, setup.first, call.ID); !ended.Ended() || endedBy(ended) != calls.ActorSystem {
			t.Errorf("the call is %q and ended by %q, want it ended by Convia with the room",
				ended.Status, endedBy(ended))
		}
	})
}

/*
TestTheRoomsOwnerPutsSomebodyOutOfItsCall is the moderation M18-004 delivers:
put out at once, and not back into that call.
*/
func TestTheRoomsOwnerPutsSomebodyOutOfItsCall(t *testing.T) {
	setup, plane := newMediaFixture(t)
	ctx := context.Background()

	ana := setup.newUser(t, setup.first, "ana")
	bea := setup.newUser(t, setup.first, "bea")
	cid := setup.newUser(t, setup.first, "cid")
	room := setup.personalRoom(t, setup.first, ana, bea, cid)

	anaSeat := setup.seat(t, setup.first, room.ID, ana, RoleModerator)
	beaSeat := setup.seat(t, setup.first, room.ID, bea, RoleMember)
	setup.seat(t, setup.first, room.ID, cid, RoleMember)

	if err := setup.service.RemoveFromRoom(ctx, setup.first, room.ID, cid, bea); !errors.Is(err, ErrNotAModerator) {
		t.Errorf("a member putting somebody out error = %v, want %v", err, ErrNotAModerator)
	}

	if err := setup.service.RemoveFromRoom(ctx, setup.first, room.ID, ana, bea); err != nil {
		t.Fatalf("RemoveFromRoom() error = %v", err)
	}

	removed := setup.participantNow(t, setup.first, beaSeat.Participant.ID)
	if removed.Status != StatusRemoved || removed.RemovedByID != anaSeat.Participant.ID {
		t.Errorf("the person put out is %q, removed by %q; want removed by the moderator",
			removed.Status, removed.RemovedByID)
	}
	if !slices.Contains(plane.disconnections(), beaSeat.Participant.ID) {
		t.Error("the person put out is still connected")
	}

	if _, _, err := setup.service.JoinRoom(ctx, setup.first, room.ID, bea, RoleMember); !errors.Is(err, ErrRemoved) {
		t.Errorf("rejoining the call they were put out of error = %v, want %v", err, ErrRemoved)
	}

	var invalid ValidationError
	if err := setup.service.RemoveFromRoom(ctx, setup.first, room.ID, ana, ana); !errors.As(err, &invalid) {
		t.Errorf("a moderator putting themselves out error = %v, want a validation error", err)
	}
}

// TestAnOwnerModeratesOnlyACallTheyAreIn keeps a moderator's authority where
// the roster puts it: inside the call.
func TestAnOwnerModeratesOnlyACallTheyAreIn(t *testing.T) {
	setup, _ := newMediaFixture(t)
	ctx := context.Background()

	ana := setup.newUser(t, setup.first, "ana")
	bea := setup.newUser(t, setup.first, "bea")
	room := setup.personalRoom(t, setup.first, ana, bea)
	setup.seat(t, setup.first, room.ID, bea, RoleMember)

	if err := setup.service.RemoveFromRoom(ctx, setup.first, room.ID, ana, bea); !errors.Is(err, ErrGone) {
		t.Errorf("an owner outside the call putting somebody out error = %v, want %v", err, ErrGone)
	}
}

/*
TestADepartureIsBelievedOnlyOnceNobodyIsConnected is the reload a report of a
departure must not be fooled by.
*/
func TestADepartureIsBelievedOnlyOnceNobodyIsConnected(t *testing.T) {
	setup, plane := newMediaFixture(t)
	ctx := context.Background()

	ana := setup.newUser(t, setup.first, "ana")
	bea := setup.newUser(t, setup.first, "bea")
	room := setup.personalRoom(t, setup.first, ana, bea)

	anaSeat := setup.seat(t, setup.first, room.ID, ana, RoleModerator)
	beaSeat := setup.seat(t, setup.first, room.ID, bea, RoleMember)
	call := anaSeat.Call

	// Ana reloads: her old connection is reported gone while her new one is open.
	plane.connect(anaSeat.Participant.ID)
	if err := setup.service.Reported(ctx, reportAbout(call, media.ReportDisconnected, anaSeat.Participant.ID)); err != nil {
		t.Fatalf("Reported() error = %v", err)
	}
	if !setup.participantNow(t, setup.first, anaSeat.Participant.ID).Present() {
		t.Fatal("somebody still connected was taken out of the call on a report about an old connection")
	}

	// Bea has really gone.
	if err := setup.service.Reported(ctx, reportAbout(call, media.ReportDisconnected, beaSeat.Participant.ID)); err != nil {
		t.Fatalf("Reported() error = %v", err)
	}
	if got := setup.participantNow(t, setup.first, beaSeat.Participant.ID).Status; got != StatusLeft {
		t.Errorf("somebody whose connection went away is %q, want %q", got, StatusLeft)
	}
	if setup.callNow(t, setup.first, call.ID).Ended() {
		t.Fatal("the call ended while somebody was still connected to it")
	}

	// Then Ana goes too, and nobody asked for the call to end.
	if err := plane.Disconnect(ctx, media.Session{}, anaSeat.Participant.ID); err != nil {
		t.Fatalf("drop the connection: %v", err)
	}
	if err := setup.service.Reported(ctx, reportAbout(call, media.ReportDisconnected, anaSeat.Participant.ID)); err != nil {
		t.Fatalf("Reported() error = %v", err)
	}
	if ended := setup.callNow(t, setup.first, call.ID); !ended.Ended() || endedBy(ended) != calls.ActorSystem {
		t.Errorf("the call is %q and ended by %q, want it ended by Convia", ended.Status, endedBy(ended))
	}
}

// TestSomebodyConnectedWhoIsNotInTheCallIsDisconnected closes the gap a
// credential leaves: it outlives the participation it was issued for.
func TestSomebodyConnectedWhoIsNotInTheCallIsDisconnected(t *testing.T) {
	setup, plane := newMediaFixture(t)
	ctx := context.Background()

	ana := setup.newUser(t, setup.first, "ana")
	bea := setup.newUser(t, setup.first, "bea")
	room := setup.personalRoom(t, setup.first, ana, bea)

	anaSeat := setup.seat(t, setup.first, room.ID, ana, RoleModerator)
	beaSeat := setup.seat(t, setup.first, room.ID, bea, RoleMember)

	if err := setup.service.RemoveFromRoom(ctx, setup.first, room.ID, ana, bea); err != nil {
		t.Fatalf("RemoveFromRoom() error = %v", err)
	}
	before := len(plane.disconnections())

	if err := setup.service.Reported(ctx, reportAbout(anaSeat.Call, media.ReportConnected, beaSeat.Participant.ID)); err != nil {
		t.Fatalf("Reported() error = %v", err)
	}
	after := plane.disconnections()
	if len(after) != before+1 || after[len(after)-1] != beaSeat.Participant.ID {
		t.Error("somebody put out of the call connected with the credential they still held, and stayed")
	}

	if err := setup.service.Reported(ctx, reportAbout(anaSeat.Call, media.ReportConnected, anaSeat.Participant.ID)); err != nil {
		t.Fatalf("Reported() error = %v", err)
	}
	if got := len(plane.disconnections()); got != len(after) {
		t.Error("somebody in the call was disconnected for connecting to it")
	}
}

// TestAFinishedSessionEndsACallNobodyArrivedAt is the call no departure will ever
// be reported for.
func TestAFinishedSessionEndsACallNobodyArrivedAt(t *testing.T) {
	setup, _ := newMediaFixture(t)
	ctx := context.Background()

	ana := setup.newUser(t, setup.first, "ana")
	room := setup.personalRoom(t, setup.first, ana)
	seat := setup.seat(t, setup.first, room.ID, ana, RoleModerator)

	if err := setup.service.Reported(ctx, reportAbout(seat.Call, media.ReportFinished, "")); err != nil {
		t.Fatalf("Reported() error = %v", err)
	}

	if got := setup.participantNow(t, setup.first, seat.Participant.ID).Status; got != StatusLeft {
		t.Errorf("somebody in a call whose session is gone is %q, want %q", got, StatusLeft)
	}
	if ended := setup.callNow(t, setup.first, seat.Call.ID); !ended.Ended() || endedBy(ended) != calls.ActorSystem {
		t.Errorf("the call is %q and ended by %q, want it ended by Convia", ended.Status, endedBy(ended))
	}
}

// TestAFinishedSessionLeavesAnApplicationsCallRunning keeps a lapsed session from
// ending a call an application is still waiting in.
func TestAFinishedSessionLeavesAnApplicationsCallRunning(t *testing.T) {
	setup, _ := newMediaFixture(t)
	ctx := context.Background()

	call := setup.newCall(t, setup.first, nil)
	participant := setup.join(t, setup.first, call.ID, setup.newUser(t, setup.first, "ana"), RoleMember)

	if err := setup.service.Reported(ctx, reportAbout(call, media.ReportFinished, "")); err != nil {
		t.Fatalf("Reported() error = %v", err)
	}

	if setup.callNow(t, setup.first, call.ID).Ended() {
		t.Error("an application's call was ended by its session lapsing")
	}
	if !setup.participantNow(t, setup.first, participant.ID).Present() {
		t.Error("an application's participant was taken out of its call by its session lapsing")
	}
}

/*
TestAnApplicationsCallOutlivesItsLastConnection keeps a report of the last
departure from ending a call Convia's own product does not hold: the person is
recorded as having left, and the application still decides when its call ends.
*/
func TestAnApplicationsCallOutlivesItsLastConnection(t *testing.T) {
	setup, _ := newMediaFixture(t)
	ctx := context.Background()

	call := setup.newCall(t, setup.first, nil)
	participant := setup.join(t, setup.first, call.ID, setup.newUser(t, setup.first, "ana"), RoleMember)

	if err := setup.service.Reported(ctx, reportAbout(call, media.ReportDisconnected, participant.ID)); err != nil {
		t.Fatalf("Reported() error = %v", err)
	}

	if got := setup.participantNow(t, setup.first, participant.ID).Status; got != StatusLeft {
		t.Errorf("an application's participant whose connection went away is %q, want %q", got, StatusLeft)
	}
	if setup.callNow(t, setup.first, call.ID).Ended() {
		t.Error("an application's call was ended by its last connection going away")
	}
}

func TestAReportAboutASessionConviaDoesNotKnowIsIgnored(t *testing.T) {
	setup, _ := newMediaFixture(t)

	report := media.Report{
		Kind:          media.ReportDisconnected,
		Session:       media.Session{Reference: "room-for-call_2QRSTUVWXYZ234567ABCDEFGHI"},
		ParticipantID: "part_2QRSTUVWXYZ234567ABCDEFGHI",
	}
	if err := setup.service.Reported(context.Background(), report); err != nil {
		t.Errorf("Reported() error = %v, want a report about nothing Convia holds to be ignored", err)
	}
}

// TestAPersonSeesTheCallsInTheirRoomsAndNoOthers is what the Calls destination
// lists.
func TestAPersonSeesTheCallsInTheirRoomsAndNoOthers(t *testing.T) {
	setup, _ := newMediaFixture(t)
	ctx := context.Background()

	ana := setup.newUser(t, setup.first, "ana")
	bea := setup.newUser(t, setup.first, "bea")

	busy := setup.personalRoom(t, setup.first, ana)
	setup.personalRoom(t, setup.first, ana)
	elsewhere := setup.personalRoom(t, setup.first, bea)

	setup.seat(t, setup.first, busy.ID, ana, RoleModerator)
	setup.seat(t, setup.first, elsewhere.ID, bea, RoleModerator)

	personal := AsPerson(setup.service, setup.rooms, setup.users,
		sessions.Principal{ApplicationID: setup.first, UserID: ana})

	running, err := personal.Calls(ctx)
	if err != nil {
		t.Fatalf("Calls() error = %v", err)
	}
	if len(running) != 1 || running[0].RoomID != busy.ID {
		t.Errorf("Calls() = %+v, want only the call in the one room of hers that is holding one", running)
	}
}

// TestNoCallIsStartedWhereNobodyCouldBeHeard refuses before anything is written.
func TestNoCallIsStartedWhereNobodyCouldBeHeard(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	ana := setup.newUser(t, setup.first, "ana")
	room := setup.personalRoom(t, setup.first, ana)

	if _, _, err := setup.service.JoinRoom(ctx, setup.first, room.ID, ana, RoleModerator); !errors.Is(err, ErrNoMediaPlane) {
		t.Fatalf("JoinRoom() error = %v, want %v", err, ErrNoMediaPlane)
	}
	if _, err := setup.calls.Current(ctx, setup.first, room.ID); !errors.Is(err, calls.ErrNotFound) {
		t.Errorf("Current() error = %v, want no call left behind", err)
	}
}

/*
TestJoiningAsTheLastPersonLeavesNeverLandsInAnEndedCall is the race EndIfEmpty
takes the call's lock for.

Somebody arriving at the instant the last person leaves either finds the call
still running, and keeps it running, or finds it over and starts a new one. What
must never happen is being seated in a call that ended around them.
*/
func TestJoiningAsTheLastPersonLeavesNeverLandsInAnEndedCall(t *testing.T) {
	setup, _ := newMediaFixture(t)
	ctx := context.Background()

	ana := setup.newUser(t, setup.first, "ana")
	bea := setup.newUser(t, setup.first, "bea")
	room := setup.personalRoom(t, setup.first, ana, bea)

	for attempt := range 20 {
		setup.seat(t, setup.first, room.ID, ana, RoleModerator)

		var (
			group    sync.WaitGroup
			seat     Seat
			joinErr  error
			leaveErr error
		)

		group.Add(2)
		go func() {
			defer group.Done()
			seat, _, joinErr = setup.service.JoinRoom(ctx, setup.first, room.ID, bea, RoleMember)
		}()
		go func() {
			defer group.Done()
			leaveErr = setup.service.LeaveRoom(ctx, setup.first, room.ID, ana, calls.ActorPerson)
		}()
		group.Wait()

		if joinErr != nil || leaveErr != nil {
			t.Fatalf("attempt %d: JoinRoom() error = %v, LeaveRoom() error = %v", attempt, joinErr, leaveErr)
		}
		if setup.callNow(t, setup.first, seat.Call.ID).Ended() {
			t.Fatalf("attempt %d: somebody was seated in a call that had ended", attempt)
		}

		if err := setup.service.LeaveRoom(ctx, setup.first, room.ID, bea, calls.ActorPerson); err != nil {
			t.Fatalf("attempt %d: LeaveRoom() error = %v", attempt, err)
		}
	}
}
