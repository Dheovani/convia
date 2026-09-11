/*
Package events is Convia's control-event vocabulary and its live delivery.

M14 asks which events cannot be served adequately by polling or by webhooks.
The answer is not "the ones that are interesting": it is the ones whose value
decays in seconds, where a client's picture of a conversation that is happening
right now goes stale without them. That is a much smaller set than the audit
trail, and the difference is deliberate.

What streams:

  - a call starting and ending, because everything else in a conversation is
    scoped to it;
  - somebody joining, leaving, being removed, or having their role changed,
    because that is the roster, and a roster that is seconds out of date shows
    people who are not there;
  - whether somebody is available, because that is the shortest-lived thing
    Convia holds at all.

What does not, and why:

  - **Rooms, users, credentials, and applications.** These change because the
    application changed them, through a request that already returned the new
    state. Announcing it back would tell a client what it just did.
  - **A connection credential being issued.** It is the result of a request the
    subscriber made, and it is a fact about a secret. Nothing learns anything
    it did not already have.
  - **An invitation being issued or withdrawn.** Same reason as rooms: the
    application did it.
  - **An invitation being redeemed.** This one is somebody else's act, but it
    is already delivered as `participant.joined`, which carries the invitation
    that let them in. Publishing both would report one arrival twice.

Declining is the exception among invitations and does stream: it is the
invitee's own act, it is the only signal that somebody is not coming, and
nothing else observes it.

Presence is the other exception, and it is the interesting one, because an
application asserts it — which is the very reason rooms and users are absent.
The difference is that **the assertion is not the change**. What a subscriber is
told is the aggregate across a person's devices, and the moment it lapses on a
timer; the instance that sent the heartbeat knows neither. See internal/presence.

Events are Convia's own. Nothing here names a media provider, and the values
carried are the ones Convia assigned — identifiers, states, roles, flags —
never text an application composed, which may describe a person.
*/
package events

import (
	"crypto/rand"
	"fmt"
	"slices"
	"time"
)

const (
	// idPrefix marks a public identifier as an event identifier.
	idPrefix = "evt_"

	/*
		Version is the envelope version every event carries.

		It is on each event rather than negotiated once per connection, so an
		event forwarded somewhere else — into a log, into a queue, into a
		webhook when M15 arrives — stays interpretable away from the stream it
		was delivered on.
	*/
	Version = 1
)

/*
Type names what happened.

The vocabulary is closed and the values are the same strings the audit trail
records, because an event and its audit entry describe one occurrence. A type
is part of the public contract: clients branch on it, so an existing one must
not change meaning.
*/
type Type string

const (
	// CallStarted reports a conversation beginning in a room.
	CallStarted Type = "call.started"
	// CallEnded reports a conversation finishing, whoever ended it.
	CallEnded Type = "call.ended"
	// ParticipantJoined reports somebody taking part in a call.
	ParticipantJoined Type = "participant.joined"
	// ParticipantLeft reports somebody leaving of their own accord.
	ParticipantLeft Type = "participant.left"
	// ParticipantRemoved reports somebody being put out of a call.
	ParticipantRemoved Type = "participant.removed"
	// ParticipantRoleChanged reports a change in what somebody may do.
	ParticipantRoleChanged Type = "participant.role_changed"
	/*
		InvitationDeclined reports an invitee saying they are not coming.

		It is the only invitation event that streams, and the package
		documentation says why: it is the invitee's own act and nothing else
		observes it.
	*/
	InvitationDeclined Type = "invitation.declined"
	/*
		MessagePosted reports that somebody said something in a room.

		It streams for the reason rooms and users do not. A room being renamed
		is the application telling itself what it just did; a message is the
		application's *other* instances learning what one of them did, and the
		people they are holding connections for are waiting on it. Since M16 a
		backend is several processes, and the one that handled the post is
		almost never the one holding the socket of the person who needs to see
		it. That is the criterion this package opens with -- value that decays
		in seconds -- and a chat message is its clearest case.

		**It carries no body.** An event names the message and the room; what
		was said is read back through the API by a caller that was allowed to
		read it anyway. Data is documented as carrying only values Convia
		assigned, and a conversation is the last thing that should travel to
		every registered webhook destination.
	*/
	MessagePosted Type = "message.posted"
	/*
		MessageEdited reports that a message now reads differently.

		Carrying no body is what makes this one safe to retry. A delivery that
		arrives late says only that message X changed, and a subscriber that
		re-reads it gets the current text rather than an older one it would
		otherwise have written over the newer.
	*/
	MessageEdited Type = "message.edited"
	// MessageDeleted reports that a message was withdrawn and is now a
	// tombstone. It keeps its place in the history.
	MessageDeleted Type = "message.deleted"
	/*
		PresenceChanged reports that Convia will now say something different
		about whether one of an application's people is available.

		It is the exception to the rule stated above, and the exception is
		principled rather than convenient. Every other application-asserted
		fact is absent because announcing it would tell a client what it just
		did. Here the assertion is not the change: what changed is the
		aggregate across a person's devices, or the lapse of a claim on a
		timer, and neither of those is known to the instance that sent the
		heartbeat.

		It is also the only event type Convia refuses to deliver by webhook.
		See [Durable].
	*/
	PresenceChanged Type = "presence.changed"
)

/*
Types returns every event type Convia delivers.

The contract test uses it to prove that the specification and the
implementation describe the same vocabulary.
*/
func Types() []Type {
	return []Type{
		CallStarted, CallEnded,
		ParticipantJoined, ParticipantLeft, ParticipantRemoved, ParticipantRoleChanged,
		InvitationDeclined,
		MessagePosted, MessageEdited, MessageDeleted,
		PresenceChanged,
	}
}

/*
Durable reports whether an event may be delivered by webhook.

Every type may be streamed. One may not be recorded for later delivery, and it
is presence: a webhook is a delivery with attempts behind it, so a presence
report that failed once arrives after it stopped being true, and arrives after
the newer one that replaced it. Advisory and durable-with-retries are
contradictory promises, and the contradiction is refused here rather than left
for an application to discover from a roster that will not settle.

A stream has no such problem, because there is no second attempt: an event
reaches whoever is connected at that moment, in order, or not at all.
*/
func Durable(kind Type) bool {
	return kind != PresenceChanged
}

// Known reports whether a type is one Convia delivers.
func (kind Type) Known() bool {
	return slices.Contains(Types(), kind)
}

/*
SubjectType names the kind of thing an event is about.

It exists so that a client can route an event without parsing its type string,
and so that the subject identifier is never ambiguous about what it addresses.
*/
type SubjectType string

const (
	// SubjectCall means the subject identifier addresses a call.
	SubjectCall SubjectType = "call"
	// SubjectParticipant means the subject identifier addresses a participation.
	SubjectParticipant SubjectType = "participant"
	// SubjectInvitation means the subject identifier addresses an invitation.
	SubjectInvitation SubjectType = "invitation"
	// SubjectUser means the subject identifier addresses one of an
	// application's people.
	SubjectUser SubjectType = "user"

	// SubjectMessage means the subject identifier addresses a message.
	SubjectMessage SubjectType = "message"
)

/*
Subject is the thing an event is about.

The type is derived from the event type rather than supplied, so an event
naming a participant identifier while claiming to be about a call cannot be
constructed.
*/
type Subject struct {
	Type SubjectType `json:"type"`
	ID   string      `json:"id"`
}

/*
subjectOf reports what an event type is about.

An unknown type has no subject, which is how [New] refuses to build an event
Convia does not publish.
*/
func subjectOf(kind Type) (SubjectType, bool) {
	switch kind {
	case CallStarted, CallEnded:
		return SubjectCall, true
	case ParticipantJoined, ParticipantLeft, ParticipantRemoved, ParticipantRoleChanged:
		return SubjectParticipant, true
	case InvitationDeclined:
		return SubjectInvitation, true
	case MessagePosted, MessageEdited, MessageDeleted:
		return SubjectMessage, true
	case PresenceChanged:
		return SubjectUser, true
	default:
		return "", false
	}
}

/*
Data is what an event says beyond naming its subject.

Every value is one Convia assigned: an identifier, a state, a role, a flag.
Application-composed text stays out — a removal reason, an end reason, and call
metadata may all describe a person, and an event stream is the last place that
should be widened. Tests in the emitting packages assert each of them stays out.
*/
type Data map[string]any

/*
Event is one thing that happened, in the shape it is delivered in.

The correlation identifier is the request that caused it, so an event can be
matched with the access log line and the audit entry for the same occurrence.
It is absent when the cause was not a request.
*/
type Event struct {
	ID            string    `json:"id"`
	Version       int       `json:"version"`
	Type          Type      `json:"type"`
	OccurredAt    time.Time `json:"occurred_at"`
	ApplicationID string    `json:"application_id"`
	Subject       Subject   `json:"subject"`
	CorrelationID string    `json:"correlation_id,omitempty"`
	Data          Data      `json:"data,omitempty"`
}

/*
New builds an event of a known type.

An unknown type is a programming error rather than a runtime condition: the
vocabulary is closed and every caller is inside Convia, so building one is
refused loudly instead of publishing something no client can interpret.
*/
func New(kind Type, applicationID, subjectID, correlationID string, data Data) Event {
	subject, known := subjectOf(kind)
	if !known {
		panic(fmt.Sprintf("events: %q is not an event type Convia publishes", kind))
	}

	return Event{
		ID:            NewID(),
		Version:       Version,
		Type:          kind,
		OccurredAt:    time.Now().UTC().Truncate(time.Microsecond),
		ApplicationID: applicationID,
		Subject:       Subject{Type: subject, ID: subjectID},
		CorrelationID: correlationID,
		Data:          data,
	}
}

// NewID returns a fresh public event identifier.
func NewID() string {
	return idPrefix + rand.Text()
}
