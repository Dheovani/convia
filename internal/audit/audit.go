/*
Package audit records what was done to this installation, by whom, and why.

Convia has had audit entries since `M05`, as structured log lines, and
`internal/applications` said what was wrong with that in its own comment: "an
operational record, not yet a queryable audit trail; M21 introduces durable
audit storage." A log line is good at being read while an incident is happening
and bad at every question asked afterwards. It is kept for as long as whoever
ships the logs decided, it cannot be searched by subject, and nothing in Convia
can tell whether it arrived at all.

**The event journal is not this and could not be.** It is pruned after a day,
because its job is to let a disconnected reader catch up, and it records which
application an event belongs to and no actor at all. A buffer that forgets is
the right shape for delivery and the wrong shape for accountability. See ADR
0017 for why an event is written with the change that caused it; an entry here
is written the same way and for a stronger reason.

Nothing in this package decides **who may read** the trail. That is one scope
on the operator surface and it lives in operator_authorized.go, which is the
split every other domain makes.
*/
package audit

import (
	"crypto/rand"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	// idPrefix marks a public identifier as an audit entry identifier.
	idPrefix = "aud_"

	// idRandomLength is the number of random characters crypto/rand.Text emits.
	idRandomLength = 26

	/*
		maxReasonLength bounds what an actor may say about why.

		It is a sentence, not a report. A reason is read in a list beside the
		action it explains, and a field with no ceiling is one that turns the
		trail into somewhere to put prose nobody will read.
	*/
	maxReasonLength = 500

	// maxNameLength bounds an action, a subject kind, and a subject identifier.
	maxNameLength = 100

	/*
		maxDetails and maxDetailLength bound what an entry may say beyond who
		did what to what.

		The bound is the point. Details exist for the few facts the rest of the
		row cannot reconstruct -- the scopes a credential was minted with --
		and a column with no ceiling is one that becomes somewhere to put
		payloads, which is exactly the copy of every table this is written not
		to be.
	*/
	maxDetails      = 10
	maxDetailLength = 200
)

/*
ErrReasonRequired reports a high-impact action attempted without saying why.

It is `M21-009`. The refusal belongs to whoever is performing the action rather
than to this package, which only knows that the entry it was handed has no
reason in it; what makes an action high-impact is a decision each surface makes
about its own operations.
*/
var ErrReasonRequired = errors.New("the action requires a reason")

/*
Kind is the authority an action was taken on.

`internal/calls` names four of these, and names them for the same reason, but it
stops at the kind on purpose: a call's history is not an audit trail, and
which person started a call is what a participant is for.

**This package cannot stop there.** "An operator suspended this tenant" with no
operator in it is the exact failure an audit trail exists to prevent -- it
records that authority was used and loses which hands held it -- so an
[Actor] carries an identifier beside the kind.
*/
type Kind string

const (
	// KindOperator means somebody acted with authority over Convia itself.
	KindOperator Kind = "operator"
	// KindApplication means a tenant's own key acted on the tenant's resources.
	KindApplication Kind = "application"
	// KindPerson means somebody signed in to Convia's own product acted.
	KindPerson Kind = "person"
	/*
		KindGuest means the holder of an invitation acted.

		A guest is somebody Convia knows nothing about beyond the invitation
		they redeemed, which is also the only identifier there is to record.
		It is a kind of its own rather than a person because it is a different
		authority: what a guest may do was fixed when the invitation was
		issued, and no account stands behind it.
	*/
	KindGuest Kind = "guest"
	/*
		KindPeer means another installation acted for one of its own people.

		The identifier is the account on that installation, which is what the
		signature proves and the most the trail can honestly claim: a visitor's
		home vouched for them, and this installation verified the vouching
		rather than the person.
	*/
	KindPeer Kind = "peer"
	/*
		KindSystem means Convia acted without being asked to.

		It is the one kind with no identifier, because nobody presented a
		credential: a janitor forgetting an erased person, a call ended because
		the media plane reported the last connection gone. An entry claiming
		the system acted *and* naming a credential would be describing two
		different things at once, which the schema refuses.
	*/
	KindSystem Kind = "system"
)

/*
Kinds returns every actor kind Convia records.

The list is what the schema's check constraint allows, and adding one is a
migration. That is the intended cost: a kind arriving that the trail cannot
store would be an action nobody can account for.
*/
func Kinds() []Kind {
	return []Kind{KindOperator, KindApplication, KindPerson, KindGuest, KindPeer, KindSystem}
}

// Known reports whether a kind is one Convia records.
func (kind Kind) Known() bool {
	for _, candidate := range Kinds() {
		if candidate == kind {
			return true
		}
	}
	return false
}

/*
Actor is who acted, in the two parts an audit trail needs.

The identifier is the public one -- an operator credential, an application
credential, an account -- and never a secret, a digest, or a presented token.
A trail that carried key material would be a second copy of the thing it exists
to protect.
*/
type Actor struct {
	Kind Kind
	ID   string
}

// System is the actor for a change Convia made on its own evidence.
func System() Actor {
	return Actor{Kind: KindSystem}
}

/*
Valid reports whether an actor can be recorded.

Everything but the system is identified, and the system is not. Both halves are
checked because the schema checks both, and a store returning a constraint
violation where a caller could have been told the field is a worse answer.
*/
func (actor Actor) Valid() bool {
	if !actor.Kind.Known() {
		return false
	}

	if actor.Kind == KindSystem {
		return actor.ID == ""
	}

	return actor.ID != "" && utf8.RuneCountInString(actor.ID) <= maxNameLength
}

// String renders an actor for a log line, as kind and identifier.
func (actor Actor) String() string {
	if actor.ID == "" {
		return string(actor.Kind)
	}

	return string(actor.Kind) + ":" + actor.ID
}

/*
Subject is what was acted on.

Its kind is recorded beside its identifier rather than derived from the action,
because "everything that happened to this room" is the question an incident
actually asks, and an action name is not something to parse.
*/
type Subject struct {
	Kind string
	ID   string
}

/*
Entry is one thing that happened, as the trail keeps it.

There is deliberately nowhere here for what changed -- no before and no after.
An entry answers who did what to what, and why where Convia required an answer.
Carrying the shape of every change as well would make this a second copy of
every table, kept by code that cannot know when one of them changes meaning,
and the first thing anybody would trust about it is the part most likely to be
wrong.

[Entry.Details] is the narrow exception and exists for one reason: moving from
log lines to rows must not lose what the lines already said. A credential audit
line carries the scopes the key was minted with, and no action name can say
that a key can suspend every tenant.
*/
type Entry struct {
	ID            string
	Action        string
	Actor         Actor
	ApplicationID string
	Subject       Subject
	Reason        string
	Details       map[string]string
	RequestID     string
	RecordedAt    time.Time
}

// ValidationError reports a request this package refuses to record.
type ValidationError struct {
	Field   string
	Message string
}

func (err ValidationError) Error() string {
	return fmt.Sprintf("%s: %s", err.Field, err.Message)
}

// NewID generates an opaque public identifier for an entry.
func NewID() string {
	return idPrefix + rand.Text()
}

// ValidID reports whether a value has the shape of an audit entry identifier.
func ValidID(id string) bool {
	if !strings.HasPrefix(id, idPrefix) {
		return false
	}
	return validRandom(strings.TrimPrefix(id, idPrefix))
}

/*
validRandom reports whether a value is what crypto/rand.Text emits.

It is the base32 alphabet without padding, at the one length the generator
produces, so an identifier of the right shape and the wrong length is refused
before it reaches a query.
*/
func validRandom(value string) bool {
	if len(value) != idRandomLength {
		return false
	}
	for _, character := range value {
		switch {
		case character >= 'A' && character <= 'Z':
		case character >= '2' && character <= '7':
		default:
			return false
		}
	}
	return true
}

/*
ValidAction reports whether an action is named the way the trail names them.

The vocabulary is **open**, which is the one place this package differs from
every other closed vocabulary in Convia, and it is a consequence rather than a
preference: entries exist for changes no event covers -- an operator reading a
tenant's rooms announces nothing to anybody -- so closing the set here would
mean every domain's action names living in this package, and a domain that
could not record anything until this one was edited.

What is checked instead is the shape, which catches the mistakes that are worth
catching: an empty action, a sentence where a name belongs, and a name that does
not read as `noun.verb` the way the event types it shares its names with do.
*/
func ValidAction(action string) bool {
	if action == "" || utf8.RuneCountInString(action) > maxNameLength {
		return false
	}

	noun, verb, found := strings.Cut(action, ".")
	if !found {
		return false
	}
	return validName(noun) && validName(verb)
}

// validName reports whether a part of an action is lowercase and unspaced.
func validName(part string) bool {
	if part == "" {
		return false
	}
	for _, character := range part {
		switch {
		case character >= 'a' && character <= 'z':
		case character == '_':
		default:
			return false
		}
	}
	return true
}

/*
Record prepares an entry for storage, or says why it cannot be stored.

It is the one place a caller's own words are bounded and trimmed. A reason is
whitespace-trimmed first, so that a field holding only spaces is refused as the
blank it is rather than stored as a reason nobody can read.
*/
func Record(
	action string,
	actor Actor,
	subject Subject,
	applicationID,
	reason string,
	details map[string]string,
	requestID string,
	at time.Time,
) (Entry, error) {
	if !ValidAction(action) {
		return Entry{}, ValidationError{
			Field:   "action",
			Message: "The action must be named as noun.verb in lowercase.",
		}
	}

	if !actor.Valid() {
		return Entry{}, ValidationError{
			Field:   "actor",
			Message: "Every actor but the system carries an identifier, and the system carries none.",
		}
	}

	if subject.Kind == "" || subject.ID == "" {
		return Entry{}, ValidationError{
			Field:   "subject",
			Message: "An entry records what was acted on, by kind and identifier.",
		}
	}

	if utf8.RuneCountInString(subject.Kind) > maxNameLength || utf8.RuneCountInString(subject.ID) > maxNameLength {
		return Entry{}, ValidationError{
			Field:   "subject",
			Message: fmt.Sprintf("A subject kind and identifier are at most %d characters.", maxNameLength),
		}
	}

	if len(details) > maxDetails {
		return Entry{}, ValidationError{
			Field:   "details",
			Message: fmt.Sprintf("An entry carries at most %d details.", maxDetails),
		}
	}

	for name, value := range details {
		if name == "" || utf8.RuneCountInString(name) > maxDetailLength ||
			utf8.RuneCountInString(value) > maxDetailLength {
			return Entry{}, ValidationError{
				Field:   "details",
				Message: fmt.Sprintf("A detail is named, and neither its name nor its value exceeds %d characters.", maxDetailLength),
			}
		}
	}

	trimmed := strings.TrimSpace(reason)
	if utf8.RuneCountInString(trimmed) > maxReasonLength {
		return Entry{}, ValidationError{
			Field:   "reason",
			Message: fmt.Sprintf("A reason is at most %d characters.", maxReasonLength),
		}
	}

	return Entry{
		ID:            NewID(),
		Action:        action,
		Actor:         actor,
		ApplicationID: applicationID,
		Subject:       subject,
		Reason:        trimmed,
		Details:       details,
		RequestID:     requestID,
		RecordedAt:    at.UTC().Truncate(time.Microsecond),
	}, nil
}
