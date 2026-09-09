package calls

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// uniqueViolation is the SQLSTATE PostgreSQL reports for a violated unique
// index. See https://www.postgresql.org/docs/current/errcodes-appendix.html.
const uniqueViolation = "23505"

// activeCallConstraint is the index that keeps one conversation in one room.
const activeCallConstraint = "calls_room_active_key"

// columns is the projection every read shares.
const columns = `id, application_id, room_id, status, metadata, started_by,
                 ended_by, end_reason, created_at, updated_at, ended_at`

/*
Store persists calls in PostgreSQL.

Every statement is scoped to one application. There is no method that reads a
call by identifier alone, so a query cannot accidentally cross a tenant
boundary: the application is part of the lookup, not a filter applied later.
*/
type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

/*
row mirrors the projection so that a NULL reason maps to an empty string rather
than forcing every caller to handle a pointer.
*/
type row struct {
	ID            string
	ApplicationID string
	RoomID        string
	Status        Status
	Metadata      map[string]string
	StartedBy     Actor
	EndedBy       *Actor
	EndReason     *string
	CreatedAt     time.Time
	UpdatedAt     time.Time
	EndedAt       *time.Time
}

func (record row) call() Call {
	call := Call{
		ID:            record.ID,
		ApplicationID: record.ApplicationID,
		RoomID:        record.RoomID,
		Status:        record.Status,
		Metadata:      record.Metadata,
		StartedBy:     record.StartedBy,
		EndedBy:       record.EndedBy,
		CreatedAt:     record.CreatedAt.UTC(),
		UpdatedAt:     record.UpdatedAt.UTC(),
	}

	if record.EndReason != nil {
		call.EndReason = *record.EndReason
	}
	if record.EndedAt != nil {
		ended := record.EndedAt.UTC()
		call.EndedAt = &ended
	}
	if call.Metadata == nil {
		call.Metadata = map[string]string{}
	}
	return call
}

/*
optionalReason renders an explanation for storage.

An ending with nothing to say stores NULL rather than an empty string, so a
reader never has to tell "no reason given" from "a reason that was blank".
*/
func optionalReason(reason string) *string {
	if reason == "" {
		return nil
	}
	return &reason
}

/*
Create starts a call, refusing a room that is already hosting one.

The refusal comes from the partial unique index rather than from a check this
code performs, which is what makes two simultaneous starts safe: one commits
and the other is told the room is busy, with no window between deciding and
writing.
*/
func (store *Store) Create(ctx context.Context, call Call) error {
	const statement = `INSERT INTO calls
	                   (id, application_id, room_id, status, metadata, started_by, created_at, updated_at)
	                   VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`

	_, err := store.pool.Exec(ctx, statement,
		call.ID,
		call.ApplicationID,
		call.RoomID,
		call.Status,
		call.Metadata,
		call.StartedBy,
		call.CreatedAt,
		call.UpdatedAt,
	)
	if err != nil {
		if violatesActiveCall(err) {
			return ErrCallInProgress
		}
		return fmt.Errorf("insert call: %w", err)
	}
	return nil
}

/*
violatesActiveCall reports whether an error is the one-active-call violation.

Both the SQLSTATE and the constraint name are checked, so a different unique
index added later cannot be mistaken for this one, and a change to PostgreSQL's
error wording cannot turn a conflict into an internal error.
*/
func violatesActiveCall(err error) bool {
	var pgError *pgconn.PgError
	return errors.As(err, &pgError) &&
		pgError.Code == uniqueViolation &&
		pgError.ConstraintName == activeCallConstraint
}

// Get returns one call within its application.
func (store *Store) Get(ctx context.Context, applicationID, id string) (Call, error) {
	const statement = `SELECT ` + columns + ` FROM calls WHERE application_id = $1 AND id = $2`

	rows, err := store.pool.Query(ctx, statement, applicationID, id)
	if err != nil {
		return Call{}, fmt.Errorf("query call: %w", err)
	}

	record, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByPos[row])
	if errors.Is(err, pgx.ErrNoRows) {
		return Call{}, ErrNotFound
	}
	if err != nil {
		return Call{}, fmt.Errorf("read call: %w", err)
	}
	return record.call(), nil
}

/*
List returns a page of an application's calls, newest first.

Unlike rooms, no state is hidden: a call history is the point of the listing,
so ended calls are returned by default and the status filter narrows to one
kind rather than revealing a kind that was suppressed.
*/
func (store *Store) List(ctx context.Context, applicationID string, roomID string,
	filter *Status, cursor *Cursor, limit int) ([]Call, bool, error) {
	statement := `SELECT ` + columns + ` FROM calls WHERE application_id = $1`
	arguments := []any{applicationID}

	if roomID != "" {
		arguments = append(arguments, roomID)
		statement += ` AND room_id = $` + strconv.Itoa(len(arguments))
	}
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
		return nil, false, fmt.Errorf("query calls: %w", err)
	}

	records, err := pgx.CollectRows(rows, pgx.RowToStructByPos[row])
	if err != nil {
		return nil, false, fmt.Errorf("read calls: %w", err)
	}

	page := make([]Call, 0, len(records))
	for _, record := range records {
		page = append(page, record.call())
	}

	if len(page) > limit {
		return page[:limit], true, nil
	}
	return page, false, nil
}

/*
End closes a conversation, reporting whether this request is what ended it.

The write is conditional on the call still being active, so two requests
ending the same call cannot both claim to have done it. The one that finds
nothing to change reads the stored call and returns it unchanged, which is what
makes ending repeatable rather than an error.
*/
func (store *Store) End(ctx context.Context, applicationID, id string,
	by Actor, reason string, at time.Time) (Call, bool, error) {
	const statement = `UPDATE calls
	                   SET status = $1, ended_at = $2, ended_by = $3, end_reason = $4, updated_at = $2
	                   WHERE application_id = $5 AND id = $6 AND status = $7
	                   RETURNING ` + columns

	rows, err := store.pool.Query(ctx, statement,
		StatusEnded, at, by, optionalReason(reason), applicationID, id, StatusActive)
	if err != nil {
		return Call{}, false, fmt.Errorf("end call: %w", err)
	}

	record, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByPos[row])
	if errors.Is(err, pgx.ErrNoRows) {
		/*
			Nothing changed: either the call is already over, or it is not this
			application's to end. Reading it decides which, and a call this
			application cannot see is reported as missing rather than as
			already ended.
		*/
		stored, err := store.Get(ctx, applicationID, id)
		if err != nil {
			return Call{}, false, err
		}
		return stored, false, nil
	}
	if err != nil {
		return Call{}, false, fmt.Errorf("read ended call: %w", err)
	}
	return record.call(), true, nil
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
