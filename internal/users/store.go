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
Resolve returns the user an external subject maps to, creating it when the
mapping does not exist yet.

The insert and the lookup are one statement so that two concurrent requests for
the same subject cannot create two users: the unique index decides the winner,
and the loser reads the row the winner wrote.
*/
func (store *Store) Resolve(ctx context.Context, candidate User) (User, bool, error) {
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
