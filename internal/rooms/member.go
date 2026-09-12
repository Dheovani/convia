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

It carries no role and no lifecycle, and `00018` says why at length: a room
member has no power to hold that a role could name, and membership is current
state rather than the record of an occasion.
*/
type Member struct {
	ApplicationID string
	RoomID        string
	UserID        string
	CreatedAt     time.Time
}

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
