package events

import (
	"context"
	"errors"
	"strconv"
	"strings"
)

/*
Cursor is where an event sits in the order Convia recorded it.

It is the transaction that recorded the event and the event's place within the
journal, compared in that order. PostgreSQL gives out positions when rows are
inserted and transactions finish in any order, so positions alone would let an
event commit behind one a reader has already passed. Ordering by the recording
transaction first, and reading only transactions older than any still running,
is what makes the order a reader sees final. See docs/adr/0017.

Its text form is opaque to clients: they keep the latest one they received and
hand it back to resume.
*/
type Cursor struct {
	Transaction uint64
	Position    int64
}

// ErrInvalidCursor reports a cursor Convia did not write.
var ErrInvalidCursor = errors.New("the cursor is not one Convia gave out")

/*
ErrCursorTooOld reports a cursor further back than the journal keeps.

What happened between it and the oldest event still kept cannot be told, so the
subscriber has to re-read over REST, exactly as after falling behind.
*/
var ErrCursorTooOld = errors.New("the cursor is older than the events Convia keeps")

// String is the cursor as clients see it.
func (cursor Cursor) String() string {
	return strconv.FormatUint(cursor.Transaction, 10) + "-" + strconv.FormatInt(cursor.Position, 10)
}

// IsZero reports the cursor before every event.
func (cursor Cursor) IsZero() bool { return cursor == Cursor{} }

// Before reports whether cursor comes before other.
func (cursor Cursor) Before(other Cursor) bool {
	if cursor.Transaction != other.Transaction {
		return cursor.Transaction < other.Transaction
	}

	return cursor.Position < other.Position
}

// ParseCursor reads a cursor a client handed back.
func ParseCursor(text string) (Cursor, error) {
	transaction, position, found := strings.Cut(text, "-")
	if !found {
		return Cursor{}, ErrInvalidCursor
	}

	parsedTransaction, err := strconv.ParseUint(transaction, 10, 64)
	if err != nil {
		return Cursor{}, ErrInvalidCursor
	}

	parsedPosition, err := strconv.ParseInt(position, 10, 64)
	if err != nil || parsedPosition < 1 {
		return Cursor{}, ErrInvalidCursor
	}

	cursor := Cursor{Transaction: parsedTransaction, Position: parsedPosition}
	if cursor.String() != text {
		return Cursor{}, ErrInvalidCursor
	}

	return cursor, nil
}

/*
cursorOf reads the cursor an event carries. An event that carries none, such as
presence, is never part of a replay and never skipped for being one.
*/
func cursorOf(event Event) (Cursor, bool) {
	if event.Cursor == "" {
		return Cursor{}, false
	}

	cursor, err := ParseCursor(event.Cursor)
	return cursor, err == nil
}

/*
Journal is where events that must not be lost are recorded, inside the
transaction that made them happen. internal/events/journal implements it; the
interface lives here so that this package stays a leaf.
*/
type Journal interface {
	// Record writes an event within the context's transaction and returns it with its cursor.
	Record(ctx context.Context, event Event) (Event, error)
}

/*
Replayer hands a reconnecting subscriber what it missed.

Position is how far this instance has delivered to its live streams. A stream
subscribes first and asks for the position second, so everything up to it is
replayed from the journal and everything after it arrives live; an event that
comes both ways is recognised by its cursor and written once.
*/
type Replayer interface {
	Position() Cursor
	Replay(ctx context.Context, applicationID string, after, until Cursor, each func(Event) error) error
}
