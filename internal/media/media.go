/*
Package media is the boundary between Convia's control plane and whatever
transports real-time audio and video.

Convia owns rooms, calls, and participants. It does not own the transport of
media, and the two must stay separable: the intended first implementation is
LiveKit, and Convia must be able to replace or supplement it without
redesigning its public API. This package is the seam that makes that possible.

**It is not a multi-provider abstraction.** There is one intended provider, and
designing for hypothetical others would be inventing requirements nothing has.
What this package does is narrower and more useful: it names the operations
Convia's implemented flows actually need, in Convia's own vocabulary, so that
nothing about a provider reaches the domain or the public contract.

The operations here are the ones the **call lifecycle** requires today: a call
begins, so a place for it must be realized; a call ends, so that place must be
released. Issuing a participant the credentials to connect, and disconnecting
one who was removed, are deliberately absent — nobody can connect yet, so
their shape would be a guess. They arrive with the join sessions of M13.

See docs/adr/0001-control-plane-media-plane-boundary.md.
*/
package media

import (
	"context"
	"errors"
)

/*
ErrUnavailable reports a media plane that could not be reached or did not
answer in time.

It is **retryable**: the request was well-formed and may succeed on another
attempt. Convia reports it to a caller as a temporary unavailability rather
than as an internal error, because the two call for different behavior — one
invites a retry and the other does not.
*/
var ErrUnavailable = errors.New("the media plane is unavailable")

/*
ErrRejected reports a media plane that understood the request and refused it.

It is **terminal**: retrying changes nothing. It means Convia asked for
something the provider will not do — misconfiguration, a credential the
provider does not accept, a request it considers invalid. An operator has to
act, so it is logged with its detail and reported to the caller as an internal
condition rather than as something to retry.
*/
var ErrRejected = errors.New("the media plane refused the request")

/*
Retryable reports whether another attempt could succeed.

The distinction is the whole reason there are two errors rather than one.
Treating every provider failure as retryable turns a misconfiguration into an
infinite retry loop; treating none as retryable turns a one-second outage into
a failed call.
*/
func Retryable(err error) bool {
	return errors.Is(err, ErrUnavailable)
}

/*
SessionRequest is what Convia asks the media plane to realize.

A call is identified and nothing more is said. The room's capacity, the
participants, and what any of them may publish are all decided in the control
plane, so restating them here would create two places to be right about the
same rule.

Convia's identifier is what travels, never a provider's naming scheme: the
adapter is what translates one into the other, in whichever direction its
provider needs.
*/
type SessionRequest struct {
	CallID string
}

/*
Session is Convia's handle on a realized media session.

Reference is opaque and belongs to whichever provider realized it. Convia
stores it so that it can release the session later, compares it to nothing, and
**never publishes it**. It does not appear in any public representation, and it
is deliberately absent from the domain's own call type: the only way to read it
is to ask the store for it, so a handler has no field to leak.

An empty Reference means no session was realized, which is what a Convia
running without a media plane returns.
*/
type Session struct {
	Reference string
}

// Realized reports whether a session actually exists to be released.
func (session Session) Realized() bool {
	return session.Reference != ""
}

/*
Absent is the media plane of a Convia that has none.

It is the implementation this project ships with today, and it is not a stub
standing in for something missing: Convia is a control plane, and a control
plane with no media transport configured is a coherent thing to be. Rooms,
calls, and participants all work; nobody can connect, because there is nothing
to connect to.

Every operation succeeds and does nothing, so the call lifecycle behaves
exactly as it did before this boundary existed. A media plane that failed
instead would make the control plane depend on infrastructure it does not have.
*/
type Absent struct{}

// OpenSession realizes nothing and says so, by returning a session that is not realized.
func (Absent) OpenSession(context.Context, SessionRequest) (Session, error) {
	return Session{}, nil
}

// CloseSession has nothing to release.
func (Absent) CloseSession(context.Context, Session) error {
	return nil
}
