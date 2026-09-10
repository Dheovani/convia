package events

import (
	"errors"

	"convia/internal/credentials"
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
