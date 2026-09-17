package messages

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"convia/internal/transaction"
)

// columns is the projection every read shares.
const columns = `id, application_id, room_id, sequence, author_user_id,
                 author_invitation_id, body, created_at, edited_at, deleted_at, deleted_by`

/*
openRoom is the room state that accepts new messages.

It is a literal rather than an import so that this package does not take a
compile-time dependency on the rooms package for one string, which is the same
trade `participants` makes for the call state it checks under its own lock.
*/
const openRoom = "open"

// deletedRoom is the room state that is gone from the API.
const deletedRoom = "deleted"

/*
Store persists messages in PostgreSQL.

Every statement is scoped to one application, except the sequence allocation,
which is scoped to one room — and a room is reached only through a method that
was given an application. There is no method that reads a message by identifier
alone, so a query cannot cross a tenant boundary by omission.
*/
type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

/*
Atomically runs work in one transaction, together with the events it announces.
See package transaction.
*/
func (store *Store) Atomically(ctx context.Context, work func(ctx context.Context) error) error {
	return transaction.Run(ctx, store.pool, work)
}

// db is the transaction the context carries, or the pool; see package transaction.
func (store *Store) db(ctx context.Context) transaction.Querier {
	return transaction.On(ctx, store.pool)
}

/*
row mirrors the projection so that the nullable columns map to the domain's
shapes rather than forcing every caller to unwrap them.
*/
type row struct {
	ID                 string
	ApplicationID      string
	RoomID             string
	Sequence           int64
	AuthorUserID       *string
	AuthorInvitationID *string
	Body               *string
	CreatedAt          time.Time
	EditedAt           *time.Time
	DeletedAt          *time.Time
	DeletedBy          *string
}

func (record row) message() Message {
	message := Message{
		ID:            record.ID,
		ApplicationID: record.ApplicationID,
		RoomID:        record.RoomID,
		Sequence:      record.Sequence,
		CreatedAt:     record.CreatedAt.UTC(),
	}

	if record.AuthorUserID != nil {
		message.Author.UserID = *record.AuthorUserID
	}

	if record.AuthorInvitationID != nil {
		message.Author.InvitationID = *record.AuthorInvitationID
	}

	if record.Body != nil {
		message.Body = *record.Body
	}

	if record.EditedAt != nil {
		at := record.EditedAt.UTC()
		message.EditedAt = &at
	}

	if record.DeletedAt != nil {
		at := record.DeletedAt.UTC()
		message.DeletedAt = &at
	}

	if record.DeletedBy != nil {
		message.DeletedBy = Remover(*record.DeletedBy)
	}

	return message
}

// optional renders an identifier for a nullable column, so that an absent one
// stores NULL rather than an empty string the constraints would reject.
func optional(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

/*
Append writes a message and allocates its position in the room.

**The room is the consistency boundary**, so the transaction is drawn around it:
the row is locked, the position is taken, the message is written, and the lock
is released by the commit. A message's place in a history is a fact about the
room, so locking the room is reading the state that owns the ordering rather
than reaching into an unrelated domain — the same boundary `participants` draws
around a call when it admits somebody.

Taking the position optimistically was tried first and measured: sixteen
simultaneous appends to one room left nine or ten of them exhausting their
retries, because every writer that loses re-reads the same highest sequence as
every other loser and they collide again. The unique index is still there, and
under this lock it should now never fire; if it ever does, the ordering
guarantee is broken and the error says so rather than being retried away.

Locking the room also closes a race that would otherwise be real: closing a room
takes the same row lock, so a message cannot land in a room that was open when
the service checked and closed before the insert.
*/
func (store *Store) Append(ctx context.Context, message Message) (Message, error) {
	transaction, err := store.db(ctx).Begin(ctx)
	if err != nil {
		return Message{}, fmt.Errorf("begin append: %w", err)
	}
	// A commit makes this a no-op; anything else undoes the whole attempt.
	defer func() { _ = transaction.Rollback(ctx) }()

	if err := lockRoom(ctx, transaction, message.ApplicationID, message.RoomID); err != nil {
		return Message{}, err
	}

	const statement = `INSERT INTO messages
	                   (id, application_id, room_id, sequence, author_user_id,
	                    author_invitation_id, body, created_at)
	                   SELECT $1, $2, $3, coalesce(max(sequence), 0) + 1, $4, $5, $6, $7
	                   FROM messages WHERE room_id = $3
	                   RETURNING ` + columns

	rows, err := transaction.Query(
		ctx,
		statement,
		message.ID,
		message.ApplicationID,
		message.RoomID,
		optional(message.Author.UserID),
		optional(message.Author.InvitationID),
		message.Body,
		message.CreatedAt,
	)

	if err != nil {
		return Message{}, fmt.Errorf("insert message: %w", err)
	}

	/*
		pgx reads a result lazily, so a constraint violation on an
		INSERT ... RETURNING surfaces here rather than from Query above. An
		earlier version of this classified only Query's error and therefore
		never saw the one that mattered, which a concurrency test caught.
	*/
	record, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByPos[row])
	if err != nil {
		return Message{}, fmt.Errorf("insert message: %w", err)
	}

	if err := transaction.Commit(ctx); err != nil {
		return Message{}, fmt.Errorf("commit append: %w", err)
	}
	return record.message(), nil
}

/*
lockRoom takes the room's row lock and refuses one that takes nothing new.

The lock is released when the transaction ends. Closing or deleting a room takes
the same row lock, so an append and a closure cannot interleave.

A deleted room is reported as missing rather than as closed, because it is gone
from the API and a caller must not learn that an identifier once named
something.
*/
func lockRoom(ctx context.Context, transaction pgx.Tx, applicationID, roomID string) error {
	const statement = `SELECT status FROM rooms
	                   WHERE application_id = $1 AND id = $2 FOR UPDATE`

	var status string
	err := transaction.QueryRow(ctx, statement, applicationID, roomID).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrRoomNotFound
	}
	if err != nil {
		return fmt.Errorf("lock room: %w", err)
	}

	switch status {
	case openRoom:
		return nil
	case deletedRoom:
		return ErrRoomNotFound
	default:
		return ErrRoomClosed
	}
}

// Get returns one message within its application.
func (store *Store) Get(ctx context.Context, applicationID, id string) (Message, error) {
	const statement = `SELECT ` + columns + ` FROM messages WHERE application_id = $1 AND id = $2`

	rows, err := store.db(ctx).Query(ctx, statement, applicationID, id)
	if err != nil {
		return Message{}, fmt.Errorf("query message: %w", err)
	}

	record, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByPos[row])
	if errors.Is(err, pgx.ErrNoRows) {
		return Message{}, ErrNotFound
	}

	if err != nil {
		return Message{}, fmt.Errorf("read message: %w", err)
	}

	return record.message(), nil
}

/*
Page reads a room's history from a position, in one direction.

**The cursor is the sequence**, unlike the rooms listing, which encodes an
opaque pair. The difference is not an inconsistency: a room cursor wraps
`(created_at, id)`, an internal pair a client has no other use for, while a
message's sequence is already published in the message itself and is already
what read state is expressed in. Hiding a number the client is holding anyway
would be ceremony.

An absent cursor means the end of the history the direction starts from: the
newest message for Older, the very beginning for Newer.
*/
func (store *Store) Page(ctx context.Context, applicationID, roomID string,
	direction Direction, after *int64, limit int) ([]Message, bool, error) {
	statement := `SELECT ` + columns + ` FROM messages WHERE application_id = $1 AND room_id = $2`
	arguments := []any{applicationID, roomID}

	comparison, order := " < ", " DESC"
	if direction == Newer {
		comparison, order = " > ", " ASC"
	}

	if after != nil {
		arguments = append(arguments, *after)
		statement += ` AND sequence` + comparison + `$` + strconv.Itoa(len(arguments))
	}
	statement += ` ORDER BY sequence` + order + ` LIMIT ` + strconv.Itoa(limit+1)

	rows, err := store.db(ctx).Query(ctx, statement, arguments...)
	if err != nil {
		return nil, false, fmt.Errorf("query messages: %w", err)
	}

	records, err := pgx.CollectRows(rows, pgx.RowToStructByPos[row])
	if err != nil {
		return nil, false, fmt.Errorf("read messages: %w", err)
	}

	page := make([]Message, 0, len(records))
	for _, record := range records {
		page = append(page, record.message())
	}

	if len(page) > limit {
		return page[:limit], true, nil
	}
	return page, false, nil
}

/*
Edit replaces the body of a message that has not been deleted.

The deletion check is in the statement rather than in a prior read, so an edit
racing a deletion cannot land after it. A write that matches nothing is
explained by a follow-up read rather than guessed at.
*/
func (store *Store) Edit(ctx context.Context, applicationID, id, body string,
	at time.Time) (Message, error) {
	const statement = `UPDATE messages SET body = $1, edited_at = $2
	                   WHERE application_id = $3 AND id = $4 AND deleted_at IS NULL
	                   RETURNING ` + columns

	return store.write(ctx, statement, []any{body, at, applicationID, id}, applicationID, id)
}

/*
Delete turns a message into a tombstone.

The body is cleared rather than the row removed. The row keeps its position, so
a history does not close over the hole and shift every later message past a
client's cursor, and the fact that something was said and withdrawn stays
visible — which is what a reader expects to see.

Deleting twice is the same outcome as deleting once: the statement matches
nothing the second time, and the existing tombstone is returned rather than an
error, because the caller asked for a state the message is already in.
*/
func (store *Store) Delete(
	ctx context.Context,
	applicationID,
	id string,
	at time.Time,
	by Remover,
) (Message, error) {
	const statement = `UPDATE messages SET body = NULL, deleted_at = $1, deleted_by = $2
	                   WHERE application_id = $3 AND id = $4 AND deleted_at IS NULL
	                   RETURNING ` + columns

	message, err := store.write(ctx, statement, []any{at, by, applicationID, id}, applicationID, id)
	if errors.Is(err, ErrDeleted) {
		return store.Get(ctx, applicationID, id)
	}
	return message, err
}

/*
write runs a conditional update and explains a write that matched nothing.

An update that changes no row is ambiguous on its own: the message may not
exist, or it may exist and be deleted. Reading it afterwards is what separates
the two, and reading only on the unusual path keeps the ordinary one at a single
statement.
*/
func (store *Store) write(ctx context.Context, statement string, arguments []any,
	applicationID, id string) (Message, error) {
	rows, err := store.db(ctx).Query(ctx, statement, arguments...)
	if err != nil {
		return Message{}, fmt.Errorf("write message: %w", err)
	}

	record, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByPos[row])
	if errors.Is(err, pgx.ErrNoRows) {
		return Message{}, store.explainMissingWrite(ctx, applicationID, id)
	}

	if err != nil {
		return Message{}, fmt.Errorf("read written message: %w", err)
	}

	return record.message(), nil
}

// explainMissingWrite says why a conditional update matched nothing.
func (store *Store) explainMissingWrite(ctx context.Context, applicationID, id string) error {
	message, err := store.Get(ctx, applicationID, id)
	if err != nil {
		return err
	}

	if message.Deleted() {
		return ErrDeleted
	}

	/*
		The row exists, is not deleted, and still did not match. Nothing in the
		statement can produce this, so reporting it as a missing message would
		be a lie that hides a bug.
	*/
	return fmt.Errorf("message %s did not accept a write for no reason the store can name", id)
}
