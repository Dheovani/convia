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
