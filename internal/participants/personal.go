package participants

import (
	"context"
	"errors"
	"fmt"

	"convia/internal/api"
	"convia/internal/calls"
	"convia/internal/events"
	"convia/internal/media"
	"convia/internal/rooms"
	"convia/internal/sessions"
	"convia/internal/users"
)

const (
	// emptyReason is recorded on a call that ended because nobody was left in it.
	emptyReason = "Everyone left."

	// deletedRoomReason is recorded on a call that ended because its room was deleted.
	deletedRoomReason = "The room was deleted."
)

/*
Seat is what somebody joining a call from Convia's own product is given: the
call, their place in it, and what to connect with.
*/
type Seat struct {
	Call        calls.Call
	Participant Participant
	Credential  media.Credential
}

/*
JoinRoom seats a person in the call a room is holding, starting one when it
holds none, and reports whether this request is what started it.

**Starting and joining are one act here.** The call domain keeps them apart, and
rightly for an application, which starts a call and then admits people to it on
its own schedule. A person pressing a button wants to talk, and a call they
started without being in it would be one nobody is in. docs/adr/0014 records
the decision.

Two people starting at once is settled by the call domain's one-call-per-room
rule, and the one who loses joins the call the other started, which is what both
of them wanted. A call that ends between being found and being joined — its last
participant left at that instant — is looked for once more, and a new one is
started if it is gone.

There is nothing to start on a Convia with no media plane, and the refusal comes
before anything is written: a call that nobody could hear would be a call in the
room's history that never happened.
*/
func (service *Service) JoinRoom(
	ctx context.Context,
	applicationID,
	roomID,
	userID string,
	role Role,
) (Seat, bool, error) {
	if !service.calls.Carries() {
		return Seat{}, false, ErrNoMediaPlane
	}

	for range 2 {
		call, started, err := service.callIn(ctx, applicationID, roomID)
		if err != nil {
			return Seat{}, false, err
		}

		participant, _, err := service.Join(ctx, applicationID, call.ID,
			Admission{UserID: userID, Role: string(role)})
		if errors.Is(err, ErrCallEnded) && !started {
			continue
		}

		if err != nil {
			// A call this request started and nobody could join is ended rather
			// than left holding the room.
			if started {
				service.settle(ctx, call, calls.ActorPerson)
			}
			return Seat{}, false, err
		}

		_, credential, err := service.Session(ctx, applicationID, participant.ID)
		if err != nil {
			return Seat{}, false, err
		}

		return Seat{Call: call, Participant: participant, Credential: credential}, started, nil
	}

	return Seat{}, false, ErrCallEnded
}

// callIn finds the call a room is holding, or starts one, reporting which.
func (service *Service) callIn(ctx context.Context, applicationID, roomID string) (calls.Call, bool, error) {
	call, err := service.calls.Current(ctx, applicationID, roomID)
	if !errors.Is(err, calls.ErrNotFound) {
		return call, false, err
	}

	call, err = service.calls.Start(ctx, applicationID, roomID, calls.Definition{}, calls.ActorPerson)
	if errors.Is(err, calls.ErrCallInProgress) {
		call, err = service.calls.Current(ctx, applicationID, roomID)
		return call, false, err
	}

	return call, err == nil, err
}

/*
RoomCall returns the call a room is holding, and ErrCallNotFound when it holds
none.
*/
func (service *Service) RoomCall(ctx context.Context, applicationID, roomID string) (calls.Call, error) {
	call, err := service.calls.Current(ctx, applicationID, roomID)
	switch {
	case errors.Is(err, calls.ErrNotFound):
		return calls.Call{}, ErrCallNotFound
	case errors.Is(err, calls.ErrRoomNotFound):
		return calls.Call{}, ErrRoomNotFound
	}
	return call, err
}

// CallsIn returns the calls running in any of the named rooms, newest first.
func (service *Service) CallsIn(ctx context.Context, applicationID string, roomIDs []string) ([]calls.Call, error) {
	return service.calls.ActiveIn(ctx, applicationID, roomIDs)
}

/*
LeaveRoom takes a person out of the call a room is holding, and ends the call
when they were the last one in it.

Leaving a call they are not in succeeds and changes nothing, as leaving does
everywhere else.

by is who the ending is recorded against, if there is one: the person, when they
asked to leave; Convia, when they were taken out because their place in the room
went away and nobody asked for the call to end.
*/
func (service *Service) LeaveRoom(
	ctx context.Context,
	applicationID,
	roomID,
	userID string,
	by calls.Actor,
) error {
	call, err := service.calls.Current(ctx, applicationID, roomID)
	if errors.Is(err, calls.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}

	participant, err := service.store.PresentIn(ctx, applicationID, call.ID, userID)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}

	departed, left, err := service.changed(ctx, events.ParticipantLeft, call.RoomID,
		func(ctx context.Context) (Participant, bool, error) {
			return service.store.Leave(ctx, applicationID, participant.ID, now())
		})
	if err != nil {
		return err
	}
	if left {
		service.calls.Disconnect(ctx, call, departed.ID)
	}

	service.settle(ctx, call, by)
	return nil
}

/*
RemoveFromRoom puts somebody out of the call a room is holding, on the authority
of a moderator who is in it.

The acting person must be in the call, and as its moderator. It is the rule the
roster already enforces when an application names an acting participant,
applied to a person naming themselves, so there is one rule rather than two.

The person removed is disconnected at once and cannot come back to this call,
because a removal is terminal for the call it was made in. The next call in the
room is a new one.
*/
func (service *Service) RemoveFromRoom(
	ctx context.Context,
	applicationID,
	roomID,
	actingUserID,
	userID string,
) error {
	if actingUserID == userID {
		return ValidationError{Field: "user_id", Message: "Leave the call rather than removing yourself from it."}
	}

	call, err := service.RoomCall(ctx, applicationID, roomID)
	if err != nil {
		return err
	}

	acting, err := service.store.PresentIn(ctx, applicationID, call.ID, actingUserID)
	if errors.Is(err, ErrNotFound) {
		return ErrGone
	}

	if err != nil {
		return err
	}

	target, err := service.store.PresentIn(ctx, applicationID, call.ID, userID)
	if err != nil {
		return err
	}

	if _, err := service.Remove(ctx, applicationID, target.ID, RemoverParticipant, acting.ID, ""); err != nil {
		return err
	}

	service.settle(ctx, call, calls.ActorPerson)
	return nil
}

/*
RoomDeleted ends the call a deleted room was holding.

Nobody can find a call in a room that is gone, so leaving it running would
serve nobody. Whoever deleted the room asked for the room to go rather than for
the call to end, so the ending is recorded as Convia's own.

It is best-effort, because the deletion has already happened: a failure is
logged, and the media plane's report that the session finished ends the call
when the room's people have gone.
*/
func (service *Service) RoomDeleted(ctx context.Context, applicationID, roomID string) {
	err := service.calls.EndInRoom(ctx, applicationID, roomID, calls.ActorSystem, deletedRoomReason)
	if err != nil {
		service.logger.Error("the call in a deleted room was not ended",
			"error", err,
			"room_id", roomID,
			"application_id", applicationID,
			"request_id", api.RequestIDFromContext(ctx),
		)
	}
}

/*
MemberGone takes somebody out of a call in a room a person opened when their
place in that room goes away: they left it, or its owner removed or banned them.

Being in a room is what lets a person into its call, so losing the one loses the
other. Rooms an application created are left alone: an application admits people
to its calls on its own authority, and has never had to make them members first.

It is best-effort for the reason RoomDeleted gives.
*/
func (service *Service) MemberGone(ctx context.Context, applicationID, roomID, userID string) {
	room, err := service.rooms.Get(ctx, applicationID, roomID)
	if err == nil && !room.Personal {
		return
	}

	if err == nil {
		err = service.LeaveRoom(ctx, applicationID, roomID, userID, calls.ActorSystem)
	}

	if err != nil {
		service.logger.Error("somebody who lost their place in a room was not taken out of its call",
			"error", err,
			"room_id", roomID,
			"user_id", userID,
			"application_id", applicationID,
			"request_id", api.RequestIDFromContext(ctx),
		)
	}
}

/*
settle ends a call nobody is in any more, when it is a call in a room a person
opened.

An application's calls are left alone. An application decides when its own
conversations end, and a call with nobody in it is an ordinary state for one:
started before the first person arrives, or waiting between two.

It is best-effort, because the departure that prompted it has already been
recorded. A call that should have ended and did not is ended when the media
plane reports that its session finished.
*/
func (service *Service) settle(ctx context.Context, call calls.Call, by calls.Actor) {
	room, err := service.rooms.Get(ctx, call.ApplicationID, call.RoomID)
	if err == nil && !room.Personal {
		return
	}

	if err == nil {
		_, _, err = service.calls.EndIfEmpty(ctx, call.ApplicationID, call.ID, by, emptyReason)
	}

	if err != nil {
		service.logger.Error("a call nobody is in was not ended",
			"error", err,
			"call_id", call.ID,
			"application_id", call.ApplicationID,
			"request_id", api.RequestIDFromContext(ctx),
		)
	}
}

/*
personalService is what a signed-in person's calls need from this package.

It is an interface so that the handler depends on behavior rather than on the
service, as every handler in this repository does.
*/
type personalService interface {
	JoinRoom(ctx context.Context, applicationID, roomID, userID string, role Role) (Seat, bool, error)
	LeaveRoom(ctx context.Context, applicationID, roomID, userID string, by calls.Actor) error
	RemoveFromRoom(ctx context.Context, applicationID, roomID, actingUserID, userID string) error
	RoomCall(ctx context.Context, applicationID, roomID string) (calls.Call, error)
	CallsIn(ctx context.Context, applicationID string, roomIDs []string) ([]calls.Call, error)
	List(ctx context.Context, applicationID, callID string, options ListOptions) (Page, error)
}

/*
membership is how a person's calls learn which rooms they are in, whether they
are in one, and who owns it.
*/
type membership interface {
	IsMember(ctx context.Context, applicationID, roomID, userID string) (bool, error)
	RoomIDsOf(ctx context.Context, applicationID, userID string) ([]string, error)
	Get(ctx context.Context, applicationID, id string) (rooms.Room, error)
	Moderating(ctx context.Context, applicationID, roomID, userID string) (bool, error)
}

// directory is how a person learns what to call the people in a call.
type directory interface {
	Many(ctx context.Context, applicationID string, ids []string) (map[string]users.User, error)
}

/*
Personal is the participants service acting as one signed-in person.

What a person may do with a call follows from the room it is held in:

  - **any member** of a room sees its call and who is in it, and joins it —
    starting one when the room has none;
  - **anybody in the call** leaves it, and the call ends when its last
    participant has gone: nobody ends it for everybody;
  - **the room's owner** joins as the call's moderator, and a moderator puts
    somebody out of it.

Somebody who is not in the room is told the room is not there, as on every route
a person reaches. docs/adr/0014 records the decisions.
*/
type Personal struct {
	service   personalService
	rooms     membership
	directory directory
	principal sessions.Principal
}

// AsPerson binds the service to the authority of one verified session.
func AsPerson(
	service personalService,
	places membership,
	people directory,
	principal sessions.Principal,
) *Personal {
	return &Personal{service: service, rooms: places, directory: people, principal: principal}
}

// Present is somebody in a call, by the name they go by.
type Present struct {
	Participant Participant
	DisplayName string
}

// Roster is one page of who is in a call.
type Roster struct {
	People     []Present
	NextCursor string
}

/*
Calls returns the calls running in the rooms this person is in, newest first.

Every room they are in is asked about at once, which is the set their event
stream already reads, so a person with many rooms costs two statements rather
than one per room.
*/
func (personal *Personal) Calls(ctx context.Context) ([]calls.Call, error) {
	roomIDs, err := personal.rooms.RoomIDsOf(ctx, personal.principal.ApplicationID, personal.principal.UserID)
	if err != nil {
		return nil, fmt.Errorf("read the rooms this person is in: %w", err)
	}
	return personal.service.CallsIn(ctx, personal.principal.ApplicationID, roomIDs)
}

// Call returns the call a room this person is in is holding.
func (personal *Personal) Call(ctx context.Context, roomID string) (calls.Call, error) {
	room, err := personal.room(ctx, roomID)
	if err != nil {
		return calls.Call{}, err
	}
	return personal.service.RoomCall(ctx, personal.principal.ApplicationID, room.ID)
}

/*
Roster returns one page of who is in the call a room this person is in is
holding.

Only people who are in the call now are listed. The call's history is the
application's to read, and a person looking at a call wants to know who they
would be talking to.
*/
func (personal *Personal) Roster(ctx context.Context, roomID string, options ListOptions) (Roster, error) {
	call, err := personal.Call(ctx, roomID)
	if err != nil {
		return Roster{}, err
	}

	options.Status = string(StatusJoined)
	page, err := personal.service.List(ctx, personal.principal.ApplicationID, call.ID, options)
	if err != nil {
		return Roster{}, err
	}

	identifiers := make([]string, 0, len(page.Participants))
	for _, participant := range page.Participants {
		identifiers = append(identifiers, participant.UserID)
	}

	found, err := personal.directory.Many(ctx, personal.principal.ApplicationID, identifiers)
	if err != nil {
		return Roster{}, fmt.Errorf("read the people in the call: %w", err)
	}

	/*
		Somebody whose user is gone is skipped rather than shown nameless, as
		the room's own member list does: a blank row is not something anybody
		can act on.
	*/
	roster := Roster{People: make([]Present, 0, len(page.Participants)), NextCursor: page.NextCursor}
	for _, participant := range page.Participants {
		user, named := found[participant.UserID]
		if !named {
			continue
		}
		roster.People = append(roster.People, Present{Participant: participant, DisplayName: user.DisplayName})
	}
	return roster, nil
}

/*
Join seats this person in the call a room they are in is holding, starting one
if it holds none. The room's owner and its moderators join as the call's
moderators. A role is decided when somebody joins, so a moderator named during a
call moderates it from their next join.
*/
func (personal *Personal) Join(ctx context.Context, roomID string) (Seat, bool, error) {
	room, err := personal.room(ctx, roomID)
	if err != nil {
		return Seat{}, false, err
	}

	role := RoleMember
	moderating, err := personal.rooms.Moderating(ctx, personal.principal.ApplicationID, room.ID,
		personal.principal.UserID)
	if err != nil {
		return Seat{}, false, fmt.Errorf("check who moderates the room: %w", err)
	}
	if room.OwnerUserID == personal.principal.UserID || moderating {
		role = RoleModerator
	}

	return personal.service.JoinRoom(ctx, personal.principal.ApplicationID, room.ID,
		personal.principal.UserID, role)
}

// Leave takes this person out of the call a room they are in is holding.
func (personal *Personal) Leave(ctx context.Context, roomID string) error {
	room, err := personal.room(ctx, roomID)
	if err != nil {
		return err
	}

	return personal.service.LeaveRoom(ctx, personal.principal.ApplicationID, room.ID,
		personal.principal.UserID, calls.ActorPerson)
}

// Remove puts somebody out of the call a room this person is in is holding, as
// its moderator.
func (personal *Personal) Remove(ctx context.Context, roomID, userID string) error {
	room, err := personal.room(ctx, roomID)
	if err != nil {
		return err
	}

	return personal.service.RemoveFromRoom(ctx, personal.principal.ApplicationID, room.ID,
		personal.principal.UserID, userID)
}

// room resolves a room this person is in, refusing any other as not there.
func (personal *Personal) room(ctx context.Context, roomID string) (rooms.Room, error) {
	member, err := personal.rooms.IsMember(ctx, personal.principal.ApplicationID, roomID,
		personal.principal.UserID)
	if err != nil {
		return rooms.Room{}, fmt.Errorf("check membership: %w", err)
	}
	if !member {
		return rooms.Room{}, ErrRoomNotFound
	}

	room, err := personal.rooms.Get(ctx, personal.principal.ApplicationID, roomID)
	if errors.Is(err, rooms.ErrNotFound) {
		return rooms.Room{}, ErrRoomNotFound
	}
	if err != nil {
		return rooms.Room{}, fmt.Errorf("read the room: %w", err)
	}

	if room.Status == rooms.StatusDeleted {
		return rooms.Room{}, ErrRoomNotFound
	}
	return room, nil
}
