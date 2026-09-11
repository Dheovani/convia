/*
Package messages owns what people said, and the order they said it in.

**A conversation is a room.** There is no separate entity and no table beside
`rooms` holding one: the rooms package already calls a room "the place" and a
call "the occasion", and the thing held in a place over time is exactly what a
conversation is. Messages therefore hang off the room rather than the call, and
survive it — the history is what somebody sees when they open a room where
nothing is happening.

Convia is authoritative for the identifier, the room, the owning application,
the ordering, the timestamps, and who the author was. The body belongs to
whoever wrote it and is stored without interpretation: no markdown is parsed,
no mention is extracted, no link is fetched. That is the same stance room
metadata takes, and for the same reason — Convia cannot be wrong about a value
it never reads.

Nothing here decides **who may** write or read. That is per-person
authorization, it is the first real use of a session principal, and it is
deliberately a separate concern from what a message is.
*/
package messages

import (
	"crypto/rand"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	// idPrefix marks a public identifier as a message identifier.
	idPrefix = "msg_"

	// idRandomLength is the number of random characters crypto/rand.Text emits.
	idRandomLength = 26

	/*
		maxBodyLength bounds what one message may carry.

		It is a storage bound rather than a product opinion: messages are the
		most abundant row Convia will ever hold, and a field with no ceiling is
		one an application can turn into a blob store. Four thousand characters
		is far above what a person types and far below what makes a row
		expensive to page through.
	*/
	maxBodyLength = 4000

	/*
		FirstSequence is the ordering position of a room's first message.

		Counting from one rather than zero means "the reader has seen up to 0"
		is expressible as a position nothing occupies, which is what a room
		nobody has read yet needs.
	*/
	FirstSequence int64 = 1
)

// ErrNotFound reports that no message matches the request within its application.
var ErrNotFound = errors.New("message not found")

/*
ErrRoomNotFound reports that the room a message was addressed to does not exist
within the application.

It is distinct from ErrNotFound so that a caller learns which half of the
address was wrong, without either answer revealing anything about another
tenant.
*/
var ErrRoomNotFound = errors.New("room not found")

/*
ErrRoomClosed reports an attempt to say something in a room that is finished.

Closing a room is reversible and lossless, so the history stays readable; what
stops is anything new being added to it. A closed room that still accepted
messages would make closing mean nothing.
*/
var ErrRoomClosed = errors.New("room is closed")

/*
ErrDeleted reports an attempt to change a message that is already a tombstone.

Deleting is the author's decision about their own words, and a later edit must
not be able to put something back where they removed it.
*/
var ErrDeleted = errors.New("message is deleted")

/*
ErrNotAuthor reports an attempt to change somebody else's message.

This is not the per-person authorization M31-009 defines, which is about who
may reach a room at all. It is narrower and it belongs here: editing another
person's words is not a permission anybody can hold, so it is refused by the
domain rather than by a policy that could be configured differently.
*/
var ErrNotAuthor = errors.New("the message was written by somebody else")

/*
Author is who said something.

It is a user, or a guest carrying nothing but the invitation they redeemed, and
exactly one of the two. The shape is taken from `participants` deliberately: it
is the same question about the same people, and Convia answering it two ways is
how a roster and a history would come to disagree about who was in the room.

**Convia learns nothing new about a guest here.** No name travels with a
message; the application knows who it sent the invitation to.
*/
type Author struct {
	UserID       string
	InvitationID string
}

// Guest reports an author Convia holds no user for.
func (author Author) Guest() bool {
	return author.InvitationID != ""
}

// Valid reports an author that names exactly one of the two identities.
func (author Author) Valid() bool {
	return (author.UserID != "") != (author.InvitationID != "")
}

/*
Message is one thing somebody said in a room.

Sequence, not CreatedAt, is the order. A timestamp records when the instance
that handled the request believed it happened, and since M16 a deployment is
several instances whose clocks are not the same clock. Sequence is allocated by
the database, and is the only answer to "which came first" that two instances
cannot disagree about.
*/
type Message struct {
	ID            string
	ApplicationID string
	RoomID        string
	Sequence      int64
	Author        Author
	Body          string
	CreatedAt     time.Time
	EditedAt      *time.Time
	DeletedAt     *time.Time
}

/*
Deleted reports a message that has been withdrawn.

The row stays and keeps its sequence. A history that closed over the hole would
move every later message's position, and a client paging through it would
silently skip whatever slid past its cursor.
*/
func (message Message) Deleted() bool {
	return message.DeletedAt != nil
}

/*
Edited reports a message whose body was replaced after it was written.

Convia records **that** it was edited, not what it said before. Keeping the
previous text would double the storage of the most abundant row in the system
and create a second copy to find when somebody asks to be erased.
*/
func (message Message) Edited() bool {
	return message.EditedAt != nil
}

/*
Direction is which way through a room's history a page runs.

Both exist because the two readings are genuinely different: opening a room
reads backwards from the newest, and a client that was away and is catching up
reads forwards from where it stopped.
*/
type Direction string

const (
	// Older pages backwards, newest first. This is what opening a room does.
	Older Direction = "older"
	// Newer pages forwards, oldest first. This is what catching up does.
	Newer Direction = "newer"
)

// Directions returns every direction Convia recognizes, for the contract test.
func Directions() []Direction {
	return []Direction{Older, Newer}
}

/*
ParseDirection reads a direction supplied as a listing parameter.

Only directions Convia recognizes are accepted, so a misspelled one is reported
rather than silently reading the history the other way round.
*/
func ParseDirection(value string) (Direction, error) {
	for _, direction := range Directions() {
		if string(direction) == value {
			return direction, nil
		}
	}

	return "", ValidationError{
		Field:   "direction",
		Message: fmt.Sprintf("%q is not a direction Convia recognizes.", value),
	}
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

// NewID generates an opaque public identifier for a message.
func NewID() string {
	return idPrefix + rand.Text()
}

// ValidID reports whether an identifier has Convia's message identifier shape.
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
NormalizeBody validates what somebody wrote.

Surrounding whitespace is trimmed, because a message that is spaces reads as
empty to the person receiving it and should be refused as empty rather than
stored as something.

**A line break is allowed, and this is the first field in Convia where one is.**
Every other text a caller supplies — an alias, a name, a metadata value — is a
label, and a control character in a label is either a mistake or an attempt to
make two values render identically. A message is the first field meant to hold
more than one line, so newline and tab are what it may carry, and every other
control character is still refused.
*/
func NormalizeBody(body string) (string, error) {
	normalized := strings.TrimSpace(body)

	switch {
	case normalized == "":
		return "", ValidationError{Field: "body", Message: "The message must not be empty."}
	case utf8.RuneCountInString(normalized) > maxBodyLength:
		return "", ValidationError{
			Field:   "body",
			Message: fmt.Sprintf("The message must not exceed %d characters.", maxBodyLength),
		}
	case !utf8.ValidString(normalized):
		return "", ValidationError{
			Field:   "body",
			Message: "The message must be valid UTF-8.",
		}
	case containsForbiddenControl(normalized):
		return "", ValidationError{
			Field:   "body",
			Message: "The message must not contain control characters other than line breaks and tabs.",
		}
	}
	return normalized, nil
}

/*
containsForbiddenControl reports a control character a message may not carry.

Newline, carriage return and tab are the three a person types on purpose.
Everything else below the space, and the delete character, is refused.
*/
func containsForbiddenControl(value string) bool {
	for _, character := range value {
		if character == '\n' || character == '\r' || character == '\t' {
			continue
		}
		if character < 0x20 || character == 0x7f {
			return true
		}
	}
	return false
}
