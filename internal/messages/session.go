package messages

import (
	"context"
	"fmt"

	"convia/internal/rooms"
	"convia/internal/sessions"
)

/*
membership is the behavior this package needs to know where somebody belongs.

It arrives with the session surface and for no other reason. An application's
key carries authority over all of its own rooms, so the tenant surface has never
had to ask; a person has authority over none of them except the ones they are
in, so this is the first caller that does.

Only reading is needed, which keeps the dependency one-directional: the rooms
domain still knows nothing about messages.
*/
type membership interface {
	IsMember(ctx context.Context, applicationID, roomID, userID string) (bool, error)
	RoomsOf(ctx context.Context, applicationID, userID string, options rooms.MembershipOptions) (rooms.Membership, error)
	Many(ctx context.Context, applicationID string, ids []string) (map[string]rooms.Room, error)
}

/*
Personal is the messages service acting as one signed-in person.

**The author is the principal, and there is no field that could say otherwise.**
On the tenant surface an application names which of its people is speaking,
because it is the application acting on their behalf and it is the only party
that knows. Here the person is acting themselves, so naming somebody would be
naming somebody else — and rather than validating that a request does not do
that, the request has nowhere to put it. Posting as another person is
unrepresentable, not forbidden.

**Membership is what it checks, and a stranger is told the room is not there.**
Not 403: a refusal that distinguishes "this room is not yours" from "this room
does not exist" tells somebody outside a conversation that the conversation is
happening, which is most of what they wanted to know.
*/
type Personal struct {
	service   service
	rooms     membership
	principal sessions.Principal
}

/*
AsPerson binds the service to the authority of one verified session.

It takes a sessions.Principal and produces something that can read and write
messages. It does **not** produce a credentials.Principal, here or anywhere:
that shortcut would let anybody who signed in act with the first-party
application's full authority, and M18 made it unrepresentable rather than
merely forbidden. See docs/adr/0007.
*/
func AsPerson(service service, membership membership, principal sessions.Principal) *Personal {
	return &Personal{service: service, rooms: membership, principal: principal}
}

/*
Room is one room somebody belongs to, with what they have not read in it.

It is the sidebar's row. The unread count lives here rather than being fetched
per room because a sidebar is redrawn constantly and a query per row on the
screen is the shape that makes an interface feel slow.
*/
type Room struct {
	Room   rooms.Room
	Unread int64
}

// Sidebar is one page of the rooms somebody is in.
type Sidebar struct {
	Rooms      []Room
	NextCursor string
}

/*
Rooms returns the rooms this person belongs to, with unread counts.

It lives in this package rather than in rooms, because what makes it a sidebar
rather than a list of rooms is the unread count, and unread is this domain's
idea. The dependency stays one-directional: this asks rooms where somebody
belongs, and rooms asks nothing of this.
*/
func (personal *Personal) Rooms(ctx context.Context, options rooms.MembershipOptions) (Sidebar, error) {
	membership, err := personal.rooms.RoomsOf(ctx,
		personal.principal.ApplicationID, personal.principal.UserID, options)
	if err != nil {
		return Sidebar{}, fmt.Errorf("read the rooms somebody is in: %w", err)
	}

	identifiers := make([]string, 0, len(membership.Members))
	for _, member := range membership.Members {
		identifiers = append(identifiers, member.RoomID)
	}

	unread, err := personal.unread(ctx, identifiers)
	if err != nil {
		return Sidebar{}, err
	}

	resolved, err := personal.rooms.Many(ctx, personal.principal.ApplicationID, identifiers)
	if err != nil {
		return Sidebar{}, fmt.Errorf("read the rooms in the sidebar: %w", err)
	}

	page := Sidebar{Rooms: make([]Room, 0, len(identifiers)), NextCursor: membership.NextCursor}
	for _, identifier := range identifiers {
		/*
			A membership whose room is gone is skipped rather than reported.
			Rooms are deleted softly and memberships outlive them until
			erasure, so this is an ordinary state rather than a broken one --
			and a sidebar that failed because one row had been deleted would
			be a whole screen lost to a room nobody can open anyway.
		*/
		room, present := resolved[identifier]
		if !present {
			continue
		}
		page.Rooms = append(page.Rooms, Room{Room: room, Unread: unread[identifier]})
	}
	return page, nil
}

// History returns one window of a room this person is in.
func (personal *Personal) History(ctx context.Context, roomID string,
	options HistoryOptions) (Page, error) {
	if err := personal.requireMembership(ctx, roomID); err != nil {
		return Page{}, err
	}
	return personal.service.History(ctx, personal.principal.ApplicationID, roomID, options)
}

// Post records something this person said in a room they are in.
func (personal *Personal) Post(ctx context.Context, roomID, body string) (Message, error) {
	if err := personal.requireMembership(ctx, roomID); err != nil {
		return Message{}, err
	}
	return personal.service.Post(ctx, personal.principal.ApplicationID, roomID, personal.author(), body)
}

/*
Edit replaces what this person said.

Membership is not re-checked. The author check the domain already makes is
stricter: somebody who wrote a message was in the room when they wrote it, and
being removed since does not make their own words somebody else's to edit.
*/
func (personal *Personal) Edit(ctx context.Context, id, body string) (Message, error) {
	return personal.service.Edit(ctx, personal.principal.ApplicationID, id, personal.author(), body)
}

// Delete withdraws something this person said.
func (personal *Personal) Delete(ctx context.Context, id string) (Message, error) {
	return personal.service.Delete(ctx, personal.principal.ApplicationID, id, personal.author())
}

// MarkRead records how far this person has read in a room they are in.
func (personal *Personal) MarkRead(ctx context.Context, roomID string, sequence int64) (ReadState, error) {
	if err := personal.requireMembership(ctx, roomID); err != nil {
		return ReadState{}, err
	}
	return personal.service.MarkRead(ctx, personal.principal.ApplicationID, roomID,
		personal.principal.UserID, sequence)
}

// ReadState reports how far this person has read in a room they are in.
func (personal *Personal) ReadState(ctx context.Context, roomID string) (ReadState, error) {
	if err := personal.requireMembership(ctx, roomID); err != nil {
		return ReadState{}, err
	}
	return personal.service.ReadState(ctx, personal.principal.ApplicationID, roomID,
		personal.principal.UserID)
}

/*
author is who this person is, and it is the only author this type can produce.

There is no parameter and no field feeding it. That is what makes writing as
somebody else impossible here rather than refused.
*/
func (personal *Personal) author() Author {
	return Author{UserID: personal.principal.UserID}
}

/*
requireMembership refuses a room this person is not in, as though it were not
there.

**ErrRoomNotFound, never a forbidden.** A refusal that separates "not yours"
from "does not exist" confirms to somebody outside a conversation that the
conversation is happening, which is most of what they were asking. The
application surface is entitled to the distinction and gets it; a person is not.
*/
func (personal *Personal) requireMembership(ctx context.Context, roomID string) error {
	member, err := personal.rooms.IsMember(ctx,
		personal.principal.ApplicationID, roomID, personal.principal.UserID)
	if err != nil {
		return fmt.Errorf("check membership: %w", err)
	}
	if !member {
		return ErrRoomNotFound
	}
	return nil
}

// unread counts what this person has not read across the rooms named.
func (personal *Personal) unread(ctx context.Context, roomIDs []string) (map[string]int64, error) {
	return personal.service.UnreadByRoom(ctx, personal.principal.ApplicationID,
		personal.principal.UserID, roomIDs)
}
