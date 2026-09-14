package participants

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// columns is the projection every read shares.
const columns = `id, application_id, call_id, user_id, invitation_id, role, status,
                 removed_by, removed_by_participant_id, removal_reason,
                 created_at, updated_at, left_at`

/*
Store persists participants in PostgreSQL.

Every statement is scoped to one application. There is no method that reads a
participant by identifier alone, so a query cannot accidentally cross a tenant
boundary: the application is part of the lookup, not a filter applied later.
*/
type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

/*
row mirrors the projection so that NULL columns map to zero values rather than
forcing every caller to handle a pointer.
*/
type row struct {
	ID            string
	ApplicationID string
	CallID        string
	UserID        *string
	InvitationID  *string
	Role          Role
	Status        Status
	RemovedBy     *Remover
	RemovedByID   *string
	RemovalReason *string
	CreatedAt     time.Time
	UpdatedAt     time.Time
	LeftAt        *time.Time
}

func (record row) participant() Participant {
	participant := Participant{
		ID:            record.ID,
		ApplicationID: record.ApplicationID,
		CallID:        record.CallID,
		Role:          record.Role,
		Status:        record.Status,
		RemovedBy:     record.RemovedBy,
		CreatedAt:     record.CreatedAt.UTC(),
		UpdatedAt:     record.UpdatedAt.UTC(),
	}

	if record.UserID != nil {
		participant.UserID = *record.UserID
	}

	if record.InvitationID != nil {
		participant.InvitationID = *record.InvitationID
	}

	if record.RemovedByID != nil {
		participant.RemovedByID = *record.RemovedByID
	}

	if record.RemovalReason != nil {
		participant.RemovalReason = *record.RemovalReason
	}

	if record.LeftAt != nil {
		left := record.LeftAt.UTC()
		participant.LeftAt = &left
	}

	return participant
}

// optional renders a value for storage, mapping the empty string to NULL.
func optional(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

/*
Join admits someone to a call, reporting whether this request is what admitted
them.

The whole operation is one transaction that begins by taking a row lock on the
call. That lock is what makes capacity a real limit rather than a hopeful one:
counting the room and then inserting would let two simultaneous joins both see
a seat free and both take it, and no amount of application-level checking can
close that window. Serializing joins to one call closes it, and costs nothing
across different calls.

The call's own state is read under the same lock. A participant is only ever
inside a call, so the call is the consistency boundary this transaction is
drawn around: reading its status here is reading the state that owns the row,
not reaching into an unrelated domain. It also means a conversation cannot end
half way through someone joining it.

A false return means the person was already in the call. That is the
reconnection case, and it returns the participant that is already there rather
than creating a second.
*/
func (store *Store) Join(ctx context.Context, candidate Participant, capacity *int) (Participant, bool, error) {
	transaction, err := store.pool.Begin(ctx)
	if err != nil {
		return Participant{}, false, fmt.Errorf("begin join: %w", err)
	}
	// A commit makes this a no-op; anything else undoes the whole attempt.
	defer func() { _ = transaction.Rollback(ctx) }()

	if err := lockCall(ctx, transaction, candidate.ApplicationID, candidate.CallID); err != nil {
		return Participant{}, false, err
	}

	existing, err := presenceOf(ctx, transaction, candidate)
	switch {
	case err != nil:
		return Participant{}, false, err
	case existing != nil && existing.Status == StatusRemoved:
		return Participant{}, false, ErrRemoved
	case existing != nil:
		return *existing, false, nil
	}

	if err := checkCapacity(ctx, transaction, candidate.CallID, capacity); err != nil {
		return Participant{}, false, err
	}

	const statement = `INSERT INTO participants
	                   (id, application_id, call_id, user_id, invitation_id, role,
	                    status, created_at, updated_at)
	                   VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`

	_, err = transaction.Exec(ctx, statement,
		candidate.ID, candidate.ApplicationID, candidate.CallID,
		optional(candidate.UserID), optional(candidate.InvitationID),
		candidate.Role, StatusJoined, candidate.CreatedAt, candidate.UpdatedAt)
	if err != nil {
		return Participant{}, false, fmt.Errorf("insert participant: %w", err)
	}

	if err := transaction.Commit(ctx); err != nil {
		return Participant{}, false, fmt.Errorf("commit join: %w", err)
	}

	candidate.Status = StatusJoined
	return candidate, true, nil
}

/*
lockCall takes the call's row lock and refuses a conversation that is over.

The lock is released when the transaction ends. Ending a call takes the same
row lock, so a join and an ending cannot interleave.
*/
func lockCall(ctx context.Context, transaction pgx.Tx, applicationID, callID string) error {
	const statement = `SELECT status FROM calls
	                   WHERE application_id = $1 AND id = $2 FOR UPDATE`

	var status string
	err := transaction.QueryRow(ctx, statement, applicationID, callID).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrCallNotFound
	}
	if err != nil {
		return fmt.Errorf("lock call: %w", err)
	}
	if status != activeCall {
		return ErrCallEnded
	}
	return nil
}

/*
activeCall is the call state that admits participants.

It is a literal rather than an import so that the participants schema does not
take a compile-time dependency on the calls package for one string. The
constraint on the calls table is what keeps the vocabulary honest, and a test
joins a real call rather than trusting this constant.
*/
const activeCall = "active"

/*
presenceOf finds what the call already knows about this person.

Only a presence that blocks or satisfies a join is interesting: someone still
in the call, or someone who was removed from it. A person who left is free to
come back, and does so as a new participation so the call keeps both stints.
*/
/*
presenceOf finds an existing participation for whoever is arriving.

It looks by whichever identity the candidate carries — the person for a known
user, the invitation for a guest — because those are the two things a
participation can be unique by. Removed participations are included on purpose:
that is what makes a removal terminal for that call, for a guest exactly as
much as for a user.
*/
func presenceOf(ctx context.Context, transaction pgx.Tx, candidate Participant) (*Participant, error) {
	statement := `SELECT ` + columns + ` FROM participants
	              WHERE call_id = $1 AND user_id = $2 AND status IN ($3, $4)
	              ORDER BY created_at DESC LIMIT 1`
	identity := candidate.UserID

	if candidate.Guest() {
		statement = `SELECT ` + columns + ` FROM participants
		             WHERE call_id = $1 AND invitation_id = $2 AND status IN ($3, $4)
		             ORDER BY created_at DESC LIMIT 1`
		identity = candidate.InvitationID
	}

	rows, err := transaction.Query(ctx, statement, candidate.CallID, identity, StatusJoined, StatusRemoved)
	if err != nil {
		return nil, fmt.Errorf("query presence: %w", err)
	}

	record, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByPos[row])
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read presence: %w", err)
	}

	participant := record.participant()
	return &participant, nil
}

/*
checkCapacity refuses a join that would exceed what the room declared.

A room without a stated capacity admits anyone, because absent means the
application declined to set a limit rather than that it set one of zero.
*/
func checkCapacity(ctx context.Context, transaction pgx.Tx, callID string, capacity *int) error {
	if capacity == nil {
		return nil
	}

	const statement = `SELECT count(*) FROM participants WHERE call_id = $1 AND status = $2`

	var present int
	if err := transaction.QueryRow(ctx, statement, callID, StatusJoined).Scan(&present); err != nil {
		return fmt.Errorf("count participants: %w", err)
	}
	if present >= *capacity {
		return ErrCallFull
	}
	return nil
}

// Get returns one participant within its application.
func (store *Store) Get(ctx context.Context, applicationID, id string) (Participant, error) {
	const statement = `SELECT ` + columns + ` FROM participants
	                   WHERE application_id = $1 AND id = $2`

	rows, err := store.pool.Query(ctx, statement, applicationID, id)
	if err != nil {
		return Participant{}, fmt.Errorf("query participant: %w", err)
	}

	record, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByPos[row])
	if errors.Is(err, pgx.ErrNoRows) {
		return Participant{}, ErrNotFound
	}
	if err != nil {
		return Participant{}, fmt.Errorf("read participant: %w", err)
	}
	return record.participant(), nil
}

/*
List returns a page of one call's participants, newest first.

Everyone is returned, including those who left, because a roster is also a
record of who was there. The status filter narrows it to who is present now.
*/
func (store *Store) List(ctx context.Context, applicationID, callID string,
	filter *Status, cursor *Cursor, limit int) ([]Participant, bool, error) {
	statement := `SELECT ` + columns + ` FROM participants
	              WHERE application_id = $1 AND call_id = $2`
	arguments := []any{applicationID, callID}

	if filter != nil {
		arguments = append(arguments, *filter)
		statement += ` AND status = $` + strconv.Itoa(len(arguments))
	}

	if cursor != nil {
		arguments = append(arguments, cursor.CreatedAt, cursor.ID)
		statement += ` AND (created_at, id) < ($` + strconv.Itoa(len(arguments)-1) +
			`, $` + strconv.Itoa(len(arguments)) + `)`
	}
	statement += ` ORDER BY created_at DESC, id DESC LIMIT ` + strconv.Itoa(limit+1)

	rows, err := store.pool.Query(ctx, statement, arguments...)
	if err != nil {
		return nil, false, fmt.Errorf("query participants: %w", err)
	}

	records, err := pgx.CollectRows(rows, pgx.RowToStructByPos[row])
	if err != nil {
		return nil, false, fmt.Errorf("read participants: %w", err)
	}

	page := make([]Participant, 0, len(records))
	for _, record := range records {
		page = append(page, record.participant())
	}

	if len(page) > limit {
		return page[:limit], true, nil
	}
	return page, false, nil
}

/*
Leave records that someone left of their own accord.

The write is conditional on the person still being present, so leaving twice
succeeds and changes nothing rather than moving the moment they left.
*/
func (store *Store) Leave(ctx context.Context, applicationID, id string, at time.Time) (Participant, bool, error) {
	const statement = `UPDATE participants
	                   SET status = $1, left_at = $2, updated_at = $2
	                   WHERE application_id = $3 AND id = $4 AND status = $5
	                   RETURNING ` + columns

	return store.depart(ctx, applicationID, id, statement,
		[]any{StatusLeft, at, applicationID, id, StatusJoined})
}

/*
PresentIn returns somebody's participation in a call while they are in it, and
ErrNotFound when they are not.

A person is in a call at most once at a time, which the unique index on present
participations guarantees, so this is one row or none.
*/
func (store *Store) PresentIn(ctx context.Context, applicationID, callID, userID string) (Participant, error) {
	const statement = `SELECT ` + columns + ` FROM participants
	                   WHERE application_id = $1 AND call_id = $2 AND user_id = $3 AND status = $4`

	rows, err := store.pool.Query(ctx, statement, applicationID, callID, userID, StatusJoined)
	if err != nil {
		return Participant{}, fmt.Errorf("query presence: %w", err)
	}

	record, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByPos[row])
	if errors.Is(err, pgx.ErrNoRows) {
		return Participant{}, ErrNotFound
	}

	if err != nil {
		return Participant{}, fmt.Errorf("read presence: %w", err)
	}

	return record.participant(), nil
}

/*
LeaveEveryone records that everybody still in a call has gone, and returns who that was.

It is for a media session that no longer exists: nobody can be connected to it,
so nobody is still in the call, and each of them is recorded as having left
rather than as having been removed, because nobody put them out.
*/
func (store *Store) LeaveEveryone(
	ctx context.Context,
	applicationID,
	callID string,
	at time.Time,
) ([]Participant, error) {
	const statement = `UPDATE participants
	                   SET status = $1, left_at = $2, updated_at = $2
	                   WHERE application_id = $3 AND call_id = $4 AND status = $5
	                   RETURNING ` + columns

	rows, err := store.pool.Query(ctx, statement, StatusLeft, at, applicationID, callID, StatusJoined)
	if err != nil {
		return nil, fmt.Errorf("record everybody leaving: %w", err)
	}

	records, err := pgx.CollectRows(rows, pgx.RowToStructByPos[row])
	if err != nil {
		return nil, fmt.Errorf("read who left: %w", err)
	}

	departed := make([]Participant, 0, len(records))
	for _, record := range records {
		departed = append(departed, record.participant())
	}
	return departed, nil
}

/*
Remove records that someone was put out of a call.

Like leaving, it is conditional on the person being present, so a repeated
removal does not overwrite who removed them the first time.
*/
func (store *Store) Remove(ctx context.Context, applicationID, id string,
	by Remover, byParticipantID, reason string, at time.Time) (Participant, bool, error) {
	const statement = `UPDATE participants
	                   SET status = $1, left_at = $2, updated_at = $2,
	                       removed_by = $3, removed_by_participant_id = $4, removal_reason = $5
	                   WHERE application_id = $6 AND id = $7 AND status = $8
	                   RETURNING ` + columns

	return store.depart(ctx, applicationID, id, statement,
		[]any{StatusRemoved, at, by, optional(byParticipantID), optional(reason),
			applicationID, id, StatusJoined})
}

/*
depart completes a conditional departure.

A statement that matched nothing means the person is already gone or was never
this application's. Reading the row decides which, so a repeat is answered with
the participant as it stands rather than with an error.
*/
func (store *Store) depart(ctx context.Context, applicationID, id, statement string,
	arguments []any) (Participant, bool, error) {
	rows, err := store.pool.Query(ctx, statement, arguments...)
	if err != nil {
		return Participant{}, false, fmt.Errorf("record departure: %w", err)
	}

	record, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByPos[row])
	if errors.Is(err, pgx.ErrNoRows) {
		stored, err := store.Get(ctx, applicationID, id)
		if err != nil {
			return Participant{}, false, err
		}
		return stored, false, nil
	}
	if err != nil {
		return Participant{}, false, fmt.Errorf("read departure: %w", err)
	}
	return record.participant(), true, nil
}

/*
SetRole changes what a participant may do, while they are still present.

Changing the role of someone who has left would record an authority they can no
longer use, so it is refused rather than written.
*/
func (store *Store) SetRole(ctx context.Context, applicationID, id string,
	role Role, at time.Time) (Participant, error) {
	const statement = `UPDATE participants SET role = $1, updated_at = $2
	                   WHERE application_id = $3 AND id = $4 AND status = $5
	                   RETURNING ` + columns

	rows, err := store.pool.Query(ctx, statement, role, at, applicationID, id, StatusJoined)
	if err != nil {
		return Participant{}, fmt.Errorf("set role: %w", err)
	}

	record, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByPos[row])
	if errors.Is(err, pgx.ErrNoRows) {
		// Either the participant is gone, or they were never this application's.
		if _, err := store.Get(ctx, applicationID, id); err != nil {
			return Participant{}, err
		}
		return Participant{}, ErrGone
	}
	if err != nil {
		return Participant{}, fmt.Errorf("read updated participant: %w", err)
	}
	return record.participant(), nil
}

// Cursor is the position of a keyset page.
type Cursor struct {
	CreatedAt time.Time
	ID        string
}

// Encode renders a cursor as the opaque token published to clients.
func (cursor Cursor) Encode() string {
	payload := strconv.FormatInt(cursor.CreatedAt.UnixMicro(), 10) + ":" + cursor.ID
	return base64.RawURLEncoding.EncodeToString([]byte(payload))
}

// DecodeCursor parses a client-supplied continuation token.
func DecodeCursor(value string) (Cursor, error) {
	invalid := ValidationError{Field: "cursor", Message: "The cursor is not valid."}

	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return Cursor{}, invalid
	}

	micros, id, found := strings.Cut(string(decoded), ":")
	if !found || !ValidID(id) {
		return Cursor{}, invalid
	}

	parsed, err := strconv.ParseInt(micros, 10, 64)
	if err != nil {
		return Cursor{}, invalid
	}
	return Cursor{CreatedAt: time.UnixMicro(parsed).UTC(), ID: id}, nil
}
