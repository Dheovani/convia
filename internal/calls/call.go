/*
Package calls owns the conversations held in an application's rooms.

A call is the **occasion**; the room is the place. A room outlives every
conversation held in it, which is why the two are separate resources: creating
a room is an administrative act done once, and joining a call is something
people do repeatedly.

Convia is authoritative for the identifier, the owning application, the room,
the lifecycle state, the actors, and the timestamps. The metadata belongs to
the application and is stored without interpretation.

Nothing here names a media provider. A call is a Convia concept that the media
plane will later be asked to realize, never the other way round.
*/
package calls

import (
	"crypto/rand"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	// idPrefix marks a public identifier as a call identifier.
	idPrefix = "call_"

	// idRandomLength is the number of random characters crypto/rand.Text emits.
	idRandomLength = 26

	// maxEndReasonLength bounds the explanation a caller may record.
	maxEndReasonLength = 200

	/*
		Metadata is application-owned, so it is bounded in every dimension a
		caller could grow. The limits are the ones rooms and users already use,
		because metadata means the same thing in all three places.
	*/
	maxMetadataEntries   = 16
	maxMetadataKeyLength = 40
	maxMetadataValueSize = 256
	maxMetadataTotalSize = 4096
)

// ErrNotFound reports that no call matches the request within its application.
var ErrNotFound = errors.New("call not found")

/*
ErrRoomNotFound reports that the room a call was asked for does not exist.

It is distinct from ErrNotFound so that a caller learns which part of the path
was wrong, without either answer revealing anything about another tenant.
*/
var ErrRoomNotFound = errors.New("room not found")

/*
ErrApplicationNotFound reports that the owning application does not exist.

An application Convia has stopped serving is reported the same way as one that
never existed, so a suspended tenant learns nothing about another.
*/
var ErrApplicationNotFound = errors.New("application not found")

/*
ErrRoomClosed reports a room that will not host a new call.

Closing a room means exactly this and nothing more: it stops new conversations
without touching one already in progress. Ending a conversation people are
having because an administrator tidied a listing would be the wrong default.
*/
var ErrRoomClosed = errors.New("room does not accept new calls")

/*
ErrCallInProgress reports a room that is already hosting a conversation.

A room holds one call at a time. A caller that wants the current one asks for
it rather than starting a second, which is why this is refused instead of
returning the running call: starting and finding are different questions.
*/
var ErrCallInProgress = errors.New("the room already has an active call")

/*
Status is the lifecycle state of a call.

There are two, and both are reachable. A conversation is happening, or it is
over.

Convia deliberately does not model `ringing`, `ending`, or `failed` yet.
Ringing describes someone being summoned, which needs the invitations of M10.
Ending describes a teardown in flight, which needs a media plane to be tearing
anything down. Failed describes a call that could not be established, which
needs something that can fail to establish one. Each would be a state nothing
could enter, and a lifecycle with unreachable states teaches a client rules
that are not true.

Why a call ended is recorded as a reason rather than as a state, so the record
says what happened without the vocabulary pretending to more than Convia can
observe.
*/
type Status string

const (
	// StatusActive means the conversation is happening.
	StatusActive Status = "active"
	// StatusEnded means the conversation is over. It is terminal.
	StatusEnded Status = "ended"
)

// Statuses returns every state Convia recognizes, for the contract test.
func Statuses() []Status {
	return []Status{StatusActive, StatusEnded}
}

/*
Actor is who caused a transition.

It names the authority that made the request, not the person who was talking.
Who was in the call is a participant, which is M10.

A `system` actor arrives with the reconciliation described in
docs/calls.md, and is deliberately absent until something produces it.
*/
type Actor string

const (
	// ActorApplication means the application acted on its own call.
	ActorApplication Actor = "application"
	// ActorOperator means an operator acted on a tenant's call.
	ActorOperator Actor = "operator"
)

// Actors returns every actor Convia recognizes, for the contract test.
func Actors() []Actor {
	return []Actor{ActorApplication, ActorOperator}
}

/*
Call is one conversation held in a room.

It starts when it is created, which is why there is no separate started_at:
CreatedAt is when the conversation began, and two names for one instant would
only invite them to disagree.
*/
type Call struct {
	ID            string
	ApplicationID string
	RoomID        string
	Status        Status
	Metadata      map[string]string
	StartedBy     Actor
	EndedBy       *Actor
	EndReason     string
	CreatedAt     time.Time
	UpdatedAt     time.Time
	EndedAt       *time.Time
}

// Ended reports whether the conversation is over.
func (call Call) Ended() bool {
	return call.Status == StatusEnded
}

/*
ValidationError reports a value that violates a domain rule.

It names the offending field so that the transport layer can report which part
of the request was rejected without the domain knowing about HTTP.
*/
type ValidationError struct {
	Field   string
	Message string
}

func (err ValidationError) Error() string {
	return fmt.Sprintf("%s: %s", err.Field, err.Message)
}

// NewID generates an opaque public identifier for a call.
func NewID() string {
	return idPrefix + rand.Text()
}

// ValidID reports whether an identifier has Convia's call identifier shape.
func ValidID(id string) bool {
	random, found := strings.CutPrefix(id, idPrefix)
	if !found || len(random) != idRandomLength {
		return false
	}

	for _, character := range random {
		isBase32 := (character >= 'A' && character <= 'Z') || (character >= '2' && character <= '7')
		if !isBase32 {
			return false
		}
	}
	return true
}

/*
NormalizeEndReason validates the explanation recorded with an ending.

It is optional and free text, because the reasons a conversation ends are not
Convia's to enumerate. It is bounded and rejects control characters for the
same reason every other free-text field does.

It is application-supplied, so it may say something about the people in the
call. Nothing audits it.
*/
func NormalizeEndReason(reason string) (string, error) {
	normalized := strings.TrimSpace(reason)
	if normalized == "" {
		return "", nil
	}

	switch {
	case utf8.RuneCountInString(normalized) > maxEndReasonLength:
		return "", ValidationError{
			Field:   "reason",
			Message: fmt.Sprintf("The reason must not exceed %d characters.", maxEndReasonLength),
		}
	case containsControl(normalized):
		return "", ValidationError{
			Field:   "reason",
			Message: "The reason must not contain control characters.",
		}
	}
	return normalized, nil
}

/*
NormalizeMetadata validates the application-owned annotations on a call.

The rules are deliberately identical to the rooms and users domains, down to
the key pattern. Metadata means the same thing in all three, and validating one
concept three different ways is exactly the drift that makes an API surprising.
*/
func NormalizeMetadata(metadata map[string]string) (map[string]string, error) {
	if len(metadata) == 0 {
		return map[string]string{}, nil
	}
	if len(metadata) > maxMetadataEntries {
		return nil, ValidationError{
			Field:   "metadata",
			Message: fmt.Sprintf("The metadata must not contain more than %d entries.", maxMetadataEntries),
		}
	}

	total := 0
	normalized := make(map[string]string, len(metadata))

	for key, value := range metadata {
		if err := validateMetadataKey(key); err != nil {
			return nil, err
		}
		if utf8.RuneCountInString(value) > maxMetadataValueSize {
			return nil, ValidationError{
				Field:   "metadata",
				Message: fmt.Sprintf("The metadata value for %q must not exceed %d characters.", key, maxMetadataValueSize),
			}
		}
		if containsControl(value) {
			return nil, ValidationError{
				Field:   "metadata",
				Message: fmt.Sprintf("The metadata value for %q must not contain control characters.", key),
			}
		}

		total += len(key) + len(value)
		normalized[key] = value
	}

	if total > maxMetadataTotalSize {
		return nil, ValidationError{
			Field:   "metadata",
			Message: fmt.Sprintf("The metadata must not exceed %d bytes in total.", maxMetadataTotalSize),
		}
	}
	return normalized, nil
}

// validateMetadataKey applies the same key rule as the rooms and users domains.
func validateMetadataKey(key string) error {
	invalid := ValidationError{
		Field: "metadata",
		Message: fmt.Sprintf(
			"Metadata keys must be lowercase letters, digits, and underscores, start with a letter, and be at most %d characters.",
			maxMetadataKeyLength),
	}

	if key == "" || len(key) > maxMetadataKeyLength {
		return invalid
	}
	if key[0] < 'a' || key[0] > 'z' {
		return invalid
	}
	for _, character := range key {
		lowercase := character >= 'a' && character <= 'z'
		digit := character >= '0' && character <= '9'
		if !lowercase && !digit && character != '_' {
			return invalid
		}
	}
	return nil
}

/*
ParseStatus reads a lifecycle state supplied as a listing filter.

Only states Convia recognizes are accepted, so a misspelled filter is reported
rather than silently returning everything.
*/
func ParseStatus(value string) (Status, error) {
	for _, status := range Statuses() {
		if string(status) == value {
			return status, nil
		}
	}

	return "", ValidationError{
		Field:   "status",
		Message: fmt.Sprintf("%q is not a call status Convia recognizes.", value),
	}
}

func containsControl(value string) bool {
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return true
		}
	}
	return false
}
