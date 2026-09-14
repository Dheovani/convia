package rooms

import (
	"errors"
	"time"
)

/*
Member is one person's place in one room.

It is the answer to "may this person open this room", and it exists because M18
made the question Convia's to answer. Before sessions, only an application ever
addressed a room, and the application already knew who its people were;
`00009` recorded that and declined to model membership on exactly that ground.
A person presenting a session is the case that reasoning does not cover.

It carries no lifecycle, because membership is current state rather than the
record of an occasion. Whether a member owns the room is recorded on the room,
which has at most one owner; see `00021`.
*/
type Member struct {
	ApplicationID string
	RoomID        string
	UserID        string
	CreatedAt     time.Time
}

// Role is what a member is in a room a person opened.
type Role string

const (
	// RoleOwner moderates the room: see Personal.
	RoleOwner Role = "owner"
	// RoleMember takes part without moderating.
	RoleMember Role = "member"
)

/*
Ban is one person kept out of one room.

It outlives the membership it ended. Anybody may bring back somebody who was
removed; nobody may bring back somebody who is banned until the owner lifts it.
*/
type Ban struct {
	ApplicationID string
	RoomID        string
	UserID        string
	CreatedAt     time.Time
}

// Bans is one page of the people kept out of a room.
type Bans struct {
	Bans       []Ban
	NextCursor string
}

/*
ErrNotOwner reports somebody in a room trying to do what only its owner may.

Only a member is ever told this. Somebody outside the room is told the room is
not there, as on every route a person reaches, because a refusal naming the
owner would confirm that the room exists.
*/
var ErrNotOwner = errors.New("only the room's owner may do that")

// ErrBanned reports somebody the room's owner has kept out of it.
var ErrBanned = errors.New("the person is banned from the room")

/*
ErrNotAMember reports that somebody is not in the room they addressed.

It is deliberately **not** distinguishable from a missing room by anything a
caller sees. The transport turns both into the same answer, because telling
somebody that a room exists but is not theirs is telling them something about a
conversation they are not in.
*/
var ErrNotAMember = errors.New("not a member of the room")

/*
ErrUserNotFound reports that the person named is not one of the application's.

It is distinct from ErrNotAMember because the remedy differs: one is an
identifier that names nobody, the other names somebody who has not been added.
Both are reported to the *application*, which is entitled to know the difference
about its own people. The session surface never returns either, because a person
never names anybody but themselves.
*/
var ErrUserNotFound = errors.New("user not found")

/*
ErrUserUnavailable reports that the person named cannot be given a place.

Suspension is the case: somebody an application has suspended must not be
handed a room to write in. It is separate from a missing user so that an
operator reading the log can tell an unknown identifier from a withdrawn one.
*/
var ErrUserUnavailable = errors.New("user is not active")

/*
Membership is one page of who belongs to a room, or of what rooms somebody
belongs to.

The two directions share a type because they share a row. Which one a caller
asked for is obvious from the endpoint, and giving each its own page type would
be two names for the same list.
*/
type Membership struct {
	Members    []Member
	NextCursor string
}
