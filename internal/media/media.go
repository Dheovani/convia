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
credential must be issued; a person Convia put out of a call must stop being in
it, so their connection must be closed; and a report that somebody went away
has to be checked against whether they are still there.

The media plane also tells Convia what happened, which is a [Report]. It is
evidence, never an instruction: Convia decides what a report means for a call,
and a report that did not come from the media plane is refused before anybody
reads it.

See docs/adr/0001-control-plane-media-plane-boundary.md and
docs/adr/0014-a-call-in-a-room-ends-when-its-people-leave.md.
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
ErrUnverified reports a report that cannot be shown to have come from the media
plane.

Every way a report can fail to prove where it came from is this one error: no
signature, a signature made with another key, one that expired, a body that is
not the one that was signed. Telling them apart would tell somebody probing the
endpoint which part of a forgery to fix next.
*/
var ErrUnverified = errors.New("the report did not come from the media plane")

/*
ReportKind is what the media plane says happened.

There are three, because those are the three Convia acts on. A provider says a
great deal more — tracks published, recordings started, connections degrading —
and none of it changes a call or a roster, so it is read as nothing rather than
translated into a vocabulary nothing consumes.
*/
type ReportKind string

const (
	// ReportConnected means somebody connected to a session.
	ReportConnected ReportKind = "connected"
	// ReportDisconnected means somebody's connection to a session went away.
	ReportDisconnected ReportKind = "disconnected"
	// ReportFinished means the session itself is gone.
	ReportFinished ReportKind = "finished"
)

/*
Report is one thing the media plane observed.

It names the session and, when it is about somebody, the identity they connected
under — which is Convia's own participant identifier, because that is what an
admission gives them. Nothing else crosses: no provider identifier for the
connection, no tracks, no timing.

A Report with no Kind is one Convia has nothing to do about, and it is answered
as received rather than refused, so that a provider which says more than Convia
listens to is not told it is doing something wrong.
*/
type Report struct {
	Kind          ReportKind
	Session       Session
	ParticipantID string
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

// Disconnect has nobody to disconnect, because nobody could connect.
func (Absent) Disconnect(context.Context, Session, string) error {
	return nil
}

// Connected reports nobody, for the same reason.
func (Absent) Connected(context.Context, Session, string) (bool, error) {
	return false, nil
}
