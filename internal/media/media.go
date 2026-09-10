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

The operations here are the ones Convia's implemented flows require. A call
begins, so a place for it must be realized; a call ends, so that place must be
released; a person Convia has admitted needs something to connect with, so a
credential must be issued.

Disconnecting someone who was removed is still absent. Convia already ends
their participation and stops issuing them credentials, and a credential is
short-lived, so the gap is bounded rather than open. Closing it means asking
the provider to eject a live connection, which is a fourth operation added when
the removal flow is finished rather than in anticipation of it.

See docs/adr/0001-control-plane-media-plane-boundary.md.
*/
package media

import (
	"context"
	"errors"
	"log/slog"
	"time"
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
Admission is Convia's decision that one person may connect to one call.

The decision is already made by the time this exists. Whether the caller was
authorized, whether the call is still running, whether the person is suspended,
whether they were removed — all of that is settled in the control plane, and
none of it is restated here. What the media plane is told is who, to which
session, and for how long.

ParticipantID is the identity the person appears under to the provider. It is
Convia's own opaque identifier rather than anything the application chose,
because it is unique, it reveals nothing about the person, and it is the handle
Convia already uses to remove them.

Lifetime belongs to the control plane rather than to the adapter, so that how
long a connection credential is worth anything is a Convia policy that can be
read in one place instead of a constant buried behind the boundary.
*/
type Admission struct {
	Session       Session
	ParticipantID string
	Lifetime      time.Duration
}

/*
Token is a credential a client presents to connect to a media session.

It is a distinct type for the same reason APISecret is: the plain string is a
bearer credential, and every incidental way Go has of rendering a value is
overridden so that printing, formatting, or logging one produces a placeholder.
Reaching the real bytes takes a deliberate Reveal, and there should be exactly
one such call — the one that writes it into the response the client asked for.

This one is shorter-lived than an API secret, which reduces what a leak costs
but does not change what a leak is.
*/
type Token string

// String hides the token from fmt and from anything that stringifies a value.
func (Token) String() string { return redacted }

// GoString hides the token from the %#v verb, which does not consult String.
func (Token) GoString() string { return redacted }

// LogValue hides the token from slog, which resolves this before formatting.
func (Token) LogValue() slog.Value { return slog.StringValue(redacted) }

/*
Reveal returns the token itself.

The single legitimate caller is the one serializing it into the response of the
client that asked for it. Anywhere else is a leak.
*/
func (token Token) Reveal() string { return string(token) }

/*
Credential is everything a client needs to reach a media session, and nothing
more.

URL and Token are both provider-shaped, which is exactly why they stop being
Convia's problem here: the public representation names them in Convia's own
words, and no provider concept is published alongside them. There is no room
name, no grant, no session reference — a client that has this can connect and
can learn nothing else.

An empty Token means no credential was issued, which is what a Convia running
without a media plane returns.
*/
type Credential struct {
	URL       string
	Token     Token
	ExpiresAt time.Time
}

// Issued reports whether a credential exists for a client to connect with.
func (credential Credential) Issued() bool {
	return credential.Token != ""
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

/*
IssueCredential has nowhere to let anyone in.

It returns a credential that was not issued rather than an error, keeping to
this type's rule that no operation fails. Refusing the request belongs to the
control plane, which is where the difference between "this deployment has no
media plane" and "the media plane is broken" is visible and can be explained to
a caller.
*/
func (Absent) IssueCredential(context.Context, Admission) (Credential, error) {
	return Credential{}, nil
}
