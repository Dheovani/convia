package applications

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
const columns = "id, name, status, created_at, updated_at"

// Store persists applications in PostgreSQL.
type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// Create inserts a new application.
func (store *Store) Create(ctx context.Context, application Application) error {
	const statement = `INSERT INTO applications (` + columns + `) VALUES ($1, $2, $3, $4, $5)`

	_, err := store.pool.Exec(ctx, statement,
		application.ID,
		application.Name,
		application.Status,
		application.CreatedAt,
		application.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("insert application: %w", err)
	}
	return nil
}

/*
Get returns one application by identifier.

A deleted application is reported as ErrNotFound: deletion removes a tenant
from the API surface even while its row is retained for the erasure window.
*/
func (store *Store) Get(ctx context.Context, id string) (Application, error) {
	const statement = `SELECT ` + columns + ` FROM applications WHERE id = $1 AND status <> $2`

	rows, err := store.pool.Query(ctx, statement, id, StatusDeleted)
	if err != nil {
		return Application{}, fmt.Errorf("query application: %w", err)
	}

	application, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByPos[Application])
	if errors.Is(err, pgx.ErrNoRows) {
		return Application{}, ErrNotFound
	}
	if err != nil {
		return Application{}, fmt.Errorf("read application: %w", err)
	}
	return inUTC(application), nil
}

/*
inUTC normalizes the timestamps of a stored application.

PostgreSQL returns a timestamptz in the session's time zone, which would make
two equal instants compare as different values. Every application inside Convia
therefore carries UTC, and only the transport layer formats it.
*/
func inUTC(application Application) Application {
	application.CreatedAt = application.CreatedAt.UTC()
	application.UpdatedAt = application.UpdatedAt.UTC()
	return application
}

/*
List returns a page of applications, newest first.

Paging is keyset based rather than offset based, so a page stays stable while
applications are being created. It reads one row beyond the requested limit to
discover whether a further page exists, without a second count query.
*/
func (store *Store) List(ctx context.Context, cursor *Cursor, limit int) ([]Application, bool, error) {
	statement := `SELECT ` + columns + ` FROM applications WHERE status <> $1`
	arguments := []any{StatusDeleted}

	if cursor != nil {
		statement += ` AND (created_at, id) < ($2, $3)`
		arguments = append(arguments, cursor.CreatedAt, cursor.ID)
	}
	statement += ` ORDER BY created_at DESC, id DESC LIMIT ` + strconv.Itoa(limit+1)

	rows, err := store.pool.Query(ctx, statement, arguments...)
	if err != nil {
		return nil, false, fmt.Errorf("query applications: %w", err)
	}

	page, err := pgx.CollectRows(rows, pgx.RowToStructByPos[Application])
	if err != nil {
		return nil, false, fmt.Errorf("read applications: %w", err)
	}

	for index, application := range page {
		page[index] = inUTC(application)
	}

	if len(page) > limit {
		return page[:limit], true, nil
	}
	return page, false, nil
}

/*
Rename changes the display name of an application.

When guard is set, the update applies only while the stored row still carries
that timestamp, which makes the check and the write one atomic step rather than
a read followed by a hopeful write.
*/
func (store *Store) Rename(ctx context.Context, id, name string, updatedAt time.Time, guard *time.Time) (Application, error) {
	return store.update(ctx, `UPDATE applications SET name = $1, updated_at = $2`,
		[]any{name, updatedAt}, id, guard)
}

// SetStatus moves an application to a new lifecycle state.
func (store *Store) SetStatus(ctx context.Context, id string, status Status, updatedAt time.Time, guard *time.Time) (Application, error) {
	return store.update(ctx, `UPDATE applications SET status = $1, updated_at = $2`,
		[]any{status, updatedAt}, id, guard)
}

/*
update applies one conditional change and returns the stored result.

A deleted application is never updated: it has left the API surface, so it is
reported as missing rather than silently revived.
*/
func (store *Store) update(ctx context.Context, statement string, arguments []any, id string, guard *time.Time) (Application, error) {
	arguments = append(arguments, id, StatusDeleted)
	statement += ` WHERE id = $` + strconv.Itoa(len(arguments)-1) + ` AND status <> $` + strconv.Itoa(len(arguments))

	if guard != nil {
		arguments = append(arguments, *guard)
		statement += ` AND updated_at = $` + strconv.Itoa(len(arguments))
	}
	statement += ` RETURNING ` + columns

	rows, err := store.pool.Query(ctx, statement, arguments...)
	if err != nil {
		return Application{}, fmt.Errorf("update application: %w", err)
	}

	application, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByPos[Application])
	if errors.Is(err, pgx.ErrNoRows) {
		return Application{}, store.explainMissingUpdate(ctx, id, guard)
	}
	if err != nil {
		return Application{}, fmt.Errorf("read updated application: %w", err)
	}
	return inUTC(application), nil
}

/*
explainMissingUpdate decides why a conditional update matched no row.

A guarded update that matches nothing means either the application is gone or
another request changed it first, and the two must be reported differently.
*/
func (store *Store) explainMissingUpdate(ctx context.Context, id string, guard *time.Time) error {
	if guard == nil {
		return ErrNotFound
	}

	if _, err := store.Get(ctx, id); err != nil {
		return err
	}
	return ErrPreconditionFailed
}

/*
Delete removes an application from the API surface.

The row is retained so that the deletion stays recoverable and the data can be
erased on a schedule. Deleting an already-deleted application succeeds, because
a repeated delete must not fail; it reports that nothing changed, so that the
audit trail records one deletion rather than one per attempt.
*/
func (store *Store) Delete(ctx context.Context, id string, updatedAt time.Time) (bool, error) {
	_, err := store.SetStatus(ctx, id, StatusDeleted, updatedAt, nil)
	if errors.Is(err, ErrNotFound) {
		return false, store.confirmAlreadyDeleted(ctx, id)
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// confirmAlreadyDeleted distinguishes a repeated delete from an unknown application.
func (store *Store) confirmAlreadyDeleted(ctx context.Context, id string) error {
	var exists bool
	err := store.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM applications WHERE id = $1)`, id).Scan(&exists)
	if err != nil {
		return fmt.Errorf("check application existence: %w", err)
	}
	if !exists {
		return ErrNotFound
	}
	return nil
}

/*
Cursor is the position of a keyset page.

It pairs the ordering columns so that paging stays correct when several
applications share a creation timestamp.
*/
type Cursor struct {
	CreatedAt time.Time
	ID        string
}

/*
Encode renders a cursor as the opaque token published to clients.

The encoding is deliberately undocumented and may change at any time: the
compatibility policy forbids clients from decoding or constructing cursors.
*/
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
