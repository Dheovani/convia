package users

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
const columns = "id, application_id, external_subject, display_name, metadata, status, created_at, updated_at"

/*
Store persists users in PostgreSQL.

Every statement is scoped to one application. There is no method that reads a
user by identifier alone, so a query cannot accidentally cross a tenant
boundary: the application is part of the lookup, not a filter applied later.
*/
type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

/*
row mirrors the projection so that a NULL display name maps to an empty string
rather than forcing every caller to handle a pointer.
*/
type row struct {
	ID              string
	ApplicationID   string
	ExternalSubject string
	DisplayName     *string
	Metadata        map[string]string
	Status          Status
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

func (record row) user() User {
	user := User{
		ID:              record.ID,
		ApplicationID:   record.ApplicationID,
		ExternalSubject: record.ExternalSubject,
		Metadata:        record.Metadata,
		Status:          record.Status,
		CreatedAt:       record.CreatedAt.UTC(),
		UpdatedAt:       record.UpdatedAt.UTC(),
	}

	if record.DisplayName != nil {
		user.DisplayName = *record.DisplayName
	}

	if user.Metadata == nil {
		user.Metadata = map[string]string{}
	}

	return user
}

// displayName maps an empty display name to NULL rather than an empty string.
func displayName(name string) *string {
	if name == "" {
		return nil
	}
	return &name
}

/*
resolveAttempts bounds how often Resolve repeats a statement that lost a race.

One repetition is enough in principle, because a statement that neither
inserted nor found a user must have conflicted with a row that is committed by
the time it returns. The further attempts cost nothing in the common case and
keep an unforeseen interleaving from failing a request.
*/
const resolveAttempts = 3

/*
errResolveRaced reports that a resolving statement neither inserted a user nor
saw one. It never leaves this file: Resolve repeats the statement instead.
*/
var errResolveRaced = errors.New("resolve raced with a concurrent creation")

/*
Resolve returns the user an external subject maps to, creating it when the
mapping does not exist yet.

The insert and the lookup are one statement so that two concurrent requests for
the same subject cannot create two users: the unique index decides the winner.

The loser cannot always read the winner's row in that same statement, though.
PostgreSQL evaluates the whole statement against the snapshot it took before
the insert began waiting for the winner to commit, so a row committed during
that wait is invisible to the lookup and the statement returns nothing at all.
That is a lost race rather than a failure, and repeating the statement reads a
new snapshot which does include the winner's row.
*/
func (store *Store) Resolve(ctx context.Context, candidate User) (User, bool, error) {
	for range resolveAttempts {
		user, created, err := store.resolve(ctx, candidate)
		if errors.Is(err, errResolveRaced) {
			continue
		}
		if err != nil {
			return User{}, false, err
		}
		return user, created, nil
	}

	return User{}, false, fmt.Errorf("resolve user: lost the race to a concurrent creation %d times",
		resolveAttempts)
}

// resolve is one attempt at resolving an identity, against one snapshot.
func (store *Store) resolve(ctx context.Context, candidate User) (User, bool, error) {
	const statement = `
		WITH inserted AS (
		    INSERT INTO users (` + columns + `)
		    VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		    ON CONFLICT (application_id, external_subject) DO NOTHING
		    RETURNING ` + columns + `
		)
		SELECT ` + columns + `, TRUE AS created FROM inserted
		UNION ALL
		SELECT ` + columns + `, FALSE AS created FROM users
		WHERE application_id = $2 AND external_subject = $3 AND NOT EXISTS (SELECT 1 FROM inserted)`

	rows, err := store.pool.Query(ctx, statement,
		candidate.ID,
		candidate.ApplicationID,
		candidate.ExternalSubject,
		displayName(candidate.DisplayName),
		candidate.Metadata,
		candidate.Status,
		candidate.CreatedAt,
		candidate.UpdatedAt,
	)
	if err != nil {
		return User{}, false, fmt.Errorf("resolve user: %w", err)
	}

	type resolved struct {
		row
		Created bool
	}

	result, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByPos[resolved])
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, false, errResolveRaced
	}
	if err != nil {
		return User{}, false, fmt.Errorf("read resolved user: %w", err)
	}
	return result.user(), result.Created, nil
}

// Get returns one user within its application.
func (store *Store) Get(ctx context.Context, applicationID, id string) (User, error) {
	const statement = `SELECT ` + columns + ` FROM users
	                   WHERE application_id = $1 AND id = $2 AND status <> $3`

	rows, err := store.pool.Query(ctx, statement, applicationID, id, StatusDeleted)
	if err != nil {
		return User{}, fmt.Errorf("query user: %w", err)
	}

	record, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByPos[row])
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("read user: %w", err)
	}
	return record.user(), nil
}

/*
List returns a page of the users of one application, newest first.

Paging is keyset based on the same ordering the index provides, so a page stays
stable while users are being created.
*/
func (store *Store) List(ctx context.Context, applicationID string, cursor *Cursor, limit int) ([]User, bool, error) {
	statement := `SELECT ` + columns + ` FROM users WHERE application_id = $1 AND status <> $2`
	arguments := []any{applicationID, StatusDeleted}

	if cursor != nil {
		statement += ` AND (created_at, id) < ($3, $4)`
		arguments = append(arguments, cursor.CreatedAt, cursor.ID)
	}
	statement += ` ORDER BY created_at DESC, id DESC LIMIT ` + strconv.Itoa(limit+1)

	rows, err := store.pool.Query(ctx, statement, arguments...)
	if err != nil {
		return nil, false, fmt.Errorf("query users: %w", err)
	}

	records, err := pgx.CollectRows(rows, pgx.RowToStructByPos[row])
	if err != nil {
		return nil, false, fmt.Errorf("read users: %w", err)
	}

	page := make([]User, 0, len(records))
	for _, record := range records {
		page = append(page, record.user())
	}

	if len(page) > limit {
		return page[:limit], true, nil
	}
	return page, false, nil
}

/*
UpdateAttributes replaces the attributes an application owns.

The caller supplies the complete new values rather than a partial change, so
that the stored shape is always exactly what the service decided, and metadata
never ends up half-merged by the database.
*/
func (store *Store) UpdateAttributes(ctx context.Context, applicationID, id, name string,
	metadata map[string]string, updatedAt time.Time, guard *time.Time) (User, error) {
	return store.update(ctx, `UPDATE users SET display_name = $1, metadata = $2, updated_at = $3`,
		[]any{displayName(name), metadata, updatedAt}, applicationID, id, guard)
}

// SetStatus moves a user to a new lifecycle state.
func (store *Store) SetStatus(ctx context.Context, applicationID, id string, status Status,
	updatedAt time.Time, guard *time.Time) (User, error) {
	return store.update(ctx, `UPDATE users SET status = $1, updated_at = $2`,
		[]any{status, updatedAt}, applicationID, id, guard)
}

/*
update applies one conditional change and returns the stored result.

The application is part of the WHERE clause rather than a filter applied
afterwards, so an update cannot reach another tenant's user even with a valid
identifier. A deleted user is never updated: it has left the API surface, so it
is reported as missing rather than silently revived.

When guard is set, the update applies only while the stored row still carries
that timestamp, which makes the check and the write one atomic step rather than
a read followed by a hopeful write.
*/
func (store *Store) update(ctx context.Context, statement string, arguments []any,
	applicationID, id string, guard *time.Time) (User, error) {
	arguments = append(arguments, applicationID, id, StatusDeleted)
	statement += ` WHERE application_id = $` + strconv.Itoa(len(arguments)-2) +
		` AND id = $` + strconv.Itoa(len(arguments)-1) +
		` AND status <> $` + strconv.Itoa(len(arguments))

	if guard != nil {
		arguments = append(arguments, *guard)
		statement += ` AND updated_at = $` + strconv.Itoa(len(arguments))
	}
	statement += ` RETURNING ` + columns

	rows, err := store.pool.Query(ctx, statement, arguments...)
	if err != nil {
		return User{}, fmt.Errorf("update user: %w", err)
	}

	record, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByPos[row])
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, store.explainMissingUpdate(ctx, applicationID, id, guard)
	}
	if err != nil {
		return User{}, fmt.Errorf("read updated user: %w", err)
	}
	return record.user(), nil
}

/*
explainMissingUpdate decides why a conditional update matched no row.

A guarded update that matches nothing means either the user is gone or another
request changed it first, and the two must be reported differently.
*/
func (store *Store) explainMissingUpdate(ctx context.Context, applicationID, id string, guard *time.Time) error {
	if guard == nil {
		return ErrNotFound
	}

	if _, err := store.Get(ctx, applicationID, id); err != nil {
		return err
	}
	return ErrPreconditionFailed
}

/*
Delete removes a user from the API surface.

The row is retained so that the deletion stays recoverable and the data can be
erased on a schedule. Deleting an already-deleted user succeeds, because a
repeated delete must not fail; it reports that nothing changed, so that the
audit trail records one deletion rather than one per attempt.
*/
func (store *Store) Delete(ctx context.Context, applicationID, id string, updatedAt time.Time) (bool, error) {
	_, err := store.SetStatus(ctx, applicationID, id, StatusDeleted, updatedAt, nil)
	if errors.Is(err, ErrNotFound) {
		return false, store.confirmAlreadyDeleted(ctx, applicationID, id)
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// confirmAlreadyDeleted distinguishes a repeated delete from an unknown user.
func (store *Store) confirmAlreadyDeleted(ctx context.Context, applicationID, id string) error {
	var exists bool
	err := store.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM users WHERE application_id = $1 AND id = $2)`,
		applicationID, id).Scan(&exists)
	if err != nil {
		return fmt.Errorf("check user existence: %w", err)
	}
	if !exists {
		return ErrNotFound
	}
	return nil
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

	timestamp, id, found := strings.Cut(string(decoded), ":")
	if !found || !ValidID(id) {
		return Cursor{}, invalid
	}

	microseconds, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return Cursor{}, invalid
	}
	return Cursor{CreatedAt: time.UnixMicro(microseconds).UTC(), ID: id}, nil
}
