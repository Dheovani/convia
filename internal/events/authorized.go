package events

import (
	"errors"

	"convia/internal/credentials"
	"convia/internal/sessions"
)

/*
ErrForbidden reports an operation the caller's credential does not permit.

It covers both refusals a stream can meet: a credential not granted the stream
at all, and one granted the stream but nothing it could carry. They are the
same answer on purpose — telling a caller which of the two it is would describe
the scopes of a key to whoever presented it, and the remedy is identical.
*/
var ErrForbidden = errors.New("credential does not permit this operation")

/*
readingScopeFor names the scope that already permits reading what an event is
about.

This is what makes the stream carry no authority of its own. Every event
describes something the API already exposes, so the question "may this
credential be told about it" has an answer already, and inventing a second one
here would be a way for the two to disagree.
*/
func readingScopeFor(kind Type) credentials.Scope {
	switch kind {
	case CallStarted, CallEnded:
		return credentials.ScopeCallsRead
	case ParticipantJoined, ParticipantLeft, ParticipantRemoved, ParticipantRoleChanged:
		return credentials.ScopeParticipantsRead
	case InvitationDeclined:
		return credentials.ScopeInvitationsRead
	case MessagePosted, MessageEdited, MessageDeleted:
		/*
			messages:read rather than rooms:read, and the split is the point:
			listing rooms is administration, while being told what was said in
			one is being told about a conversation. A credential that may not
			read a history must not learn its shape from the stream either.
		*/
		return credentials.ScopeMessagesRead
	case MemberAdded, MemberRemoved, MemberRoleChanged:
		// The scope that already lists who is in a room, on either side of the
		// change.
		return credentials.ScopeMembersRead
	case RoomUpdated, RoomClosed, RoomReopened, RoomDeleted:
		return credentials.ScopeRoomsRead
	case PresenceChanged:
		return credentials.ScopePresenceRead
	default:
		/*
			Reaching this means a type was added to the vocabulary without
			deciding who may see it. The empty scope is the answer that grants
			nothing: a credential can only carry scopes Convia recognizes, and
			this is not one, so the type is delivered to nobody until somebody
			chooses. A test names every type here so the omission is found at
			build time rather than in production.
		*/
		return credentials.Scope("")
	}
}

/*
Authorized is the event broker acting with the authority of one verified
application.

The application is taken from the verified principal and never from the
request, so a subscriber receives its own tenant's events and there is no field
anywhere — in the path, in a query, in a message over the socket — that could
name another.
*/
type Authorized struct {
	broker    *Broker
	principal credentials.Principal
}

// Authorize binds the broker to the authority of a verified caller.
func Authorize(broker *Broker, principal credentials.Principal) *Authorized {
	return &Authorized{broker: broker, principal: principal}
}

/*
Subscribe opens the stream this credential is entitled to.

The set of types is decided here and then fixed. A client cannot ask for more
after connecting, because the connection carries nothing upstream at all: what
it receives was settled while its authority was still being checked.

A credential granted the stream but no read scope receives no connection rather
than an empty one. An empty stream looks exactly like a quiet one, and a client
would sit waiting for events that were never going to come.
*/
func (authorized *Authorized) Subscribe() (*Stream, error) {
	if !authorized.principal.Allows(credentials.ScopeEventsRead) {
		return nil, ErrForbidden
	}

	permitted := make([]Type, 0, len(Types()))
	for _, kind := range Types() {
		if authorized.principal.Allows(readingScopeFor(kind)) {
			permitted = append(permitted, kind)
		}
	}

	if len(permitted) == 0 {
		return nil, ErrForbidden
	}

	return authorized.broker.Subscribe(authorized.principal.ApplicationID, permitted)
}

/*
personTypes are the events a person's stream carries.

The rule is the tenant's, applied to a person: **a stream carries only what its
subscriber could already read.** A person reads the messages and the members of
the rooms they are in, and the rooms themselves, so those are the types, and
each is delivered only when it names such a room.

What is absent is absent for that reason and no other. Calls and their rosters
have no route on the session surface yet, so a person cannot read them, and a
participant event names a call rather than a room besides. Presence is about a
person rather than a room. Each arrives when the interface can read what it is
about — calls with M18-004 — and not before.
*/
func personTypes() []Type {
	return []Type{
		MessagePosted, MessageEdited, MessageDeleted,
		MemberAdded, MemberRemoved, MemberRoleChanged,
		RoomUpdated, RoomClosed, RoomReopened, RoomDeleted,
		CallStarted, CallEnded,
		ParticipantJoined, ParticipantLeft, ParticipantRemoved, ParticipantRoleChanged,
	}
}

/*
Personal is the event broker acting for one signed-in person.

The application and the person both come from the verified session, so there is
nothing in a request that could name another person's rooms — the same
property the tenant's stream has, one level narrower.
*/
type Personal struct {
	broker    *Broker
	principal sessions.Principal
}

// AsPerson binds the broker to the authority of a verified session. It
// produces no credentials.Principal; see docs/adr/0007.
func AsPerson(broker *Broker, principal sessions.Principal) *Personal {
	return &Personal{broker: broker, principal: principal}
}

/*
Subscribe opens the person's stream.

It covers no rooms yet. Which rooms is a read, and reading is the caller's to do
with [Stream.Reconcile] before handing the stream to anybody, because this
package holds no store to read from.
*/
func (personal *Personal) Subscribe() (*Stream, error) {
	return personal.broker.subscribePerson(personal.principal.ApplicationID, personal.principal.UserID,
		personTypes())
}
