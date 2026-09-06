package credentials

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

// columns is the projection every read shares. The digest is never among them.
const columns = "id, application_id, name, scopes, created_at, expires_at, revoked_at"

/*
Store persists credentials in PostgreSQL.

Every operator-facing statement is scoped to one application, as in the other
domains. Authentication is the single exception, and it is explicit: see
ForAuthentication.
*/
type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

/*
row mirrors the projection.

Scopes are read as plain strings and converted afterwards, so that the storage
layer does not depend on the driver knowing about a domain type.
*/
type row struct {
	ID            string
	ApplicationID string
	Name          string
	Scopes        []string
	CreatedAt     time.Time
	ExpiresAt     *time.Time
	RevokedAt     *time.Time
}

func (record row) credential() Credential {
	scopes := make([]Scope, 0, len(record.Scopes))
	for _, scope := range record.Scopes {
		scopes = append(scopes, Scope(scope))
	}

	return Credential{
		ID:            record.ID,
		ApplicationID: record.ApplicationID,
		Name:          record.Name,
		Scopes:        scopes,
		CreatedAt:     record.CreatedAt.UTC(),
		ExpiresAt:     inUTC(record.ExpiresAt),
		RevokedAt:     inUTC(record.RevokedAt),
	}
}

/*
inUTC normalizes an optional stored timestamp.

PostgreSQL returns a timestamptz in the session's time zone, which would make
two equal instants compare as different values. Every timestamp inside Convia
carries UTC, and only the transport layer formats it.
*/
func inUTC(moment *time.Time) *time.Time {
	if moment == nil {
		return nil
	}

	normalized := moment.UTC()
	return &normalized
}

// texts renders scopes for storage.
func texts(scopes []Scope) []string {
	values := make([]string, 0, len(scopes))
	for _, scope := range scopes {
		values = append(values, string(scope))
	}
	return values
}

// Create issues a credential, storing the digest of its secret rather than the secret.
func (store *Store) Create(ctx context.Context, credential Credential, digest []byte) error {
	const statement = `INSERT INTO credentials (id, application_id, name, secret_hash, scopes, created_at, expires_at)
	                   VALUES ($1, $2, $3, $4, $5, $6, $7)`

	_, err := store.pool.Exec(ctx, statement,
		credential.ID,
		credential.ApplicationID,
		credential.Name,
		digest,
		texts(credential.Scopes),
		credential.CreatedAt,
		credential.ExpiresAt,
	)
	if err != nil {
		return fmt.Errorf("insert credential: %w", err)
	}
	return nil
}

// Get returns one credential within its application.
func (store *Store) Get(ctx context.Context, applicationID, id string) (Credential, error) {
	const statement = `SELECT ` + columns + ` FROM credentials WHERE application_id = $1 AND id = $2`

	rows, err := store.pool.Query(ctx, statement, applicationID, id)
	if err != nil {
		return Credential{}, fmt.Errorf("query credential: %w", err)
	}

	record, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByPos[row])
	if errors.Is(err, pgx.ErrNoRows) {
		return Credential{}, ErrNotFound
	}

	if err != nil {
		return Credential{}, fmt.Errorf("read credential: %w", err)
	}

	return record.credential(), nil
}

/*
ForAuthentication returns a credential and its digest by identifier alone.

This is the one lookup in Convia that is not scoped to an application, and it
has to be: authentication is precisely the act of discovering which application
a key belongs to, so there is no tenant to scope by until it has run. The
identifier comes from the presented key and is checked for shape before this is
called, and the digest it returns is compared in constant time by the caller.

Nothing else may use it. Every operation that acts on behalf of an application
takes that application from the authenticated result, never from the request.
*/
func (store *Store) ForAuthentication(ctx context.Context, id string) (Credential, []byte, error) {
	const statement = `SELECT ` + columns + `, secret_hash FROM credentials WHERE id = $1`

	type authenticating struct {
		row
		SecretHash []byte
	}

	rows, err := store.pool.Query(ctx, statement, id)
	if err != nil {
		return Credential{}, nil, fmt.Errorf("query credential for authentication: %w", err)
	}

	record, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByPos[authenticating])
	if errors.Is(err, pgx.ErrNoRows) {
		return Credential{}, nil, ErrNotFound
	}
	if err != nil {
		return Credential{}, nil, fmt.Errorf("read credential for authentication: %w", err)
	}
	return record.credential(), record.SecretHash, nil
}

/*
List returns a page of one application's credentials, newest first.

Revoked and expired credentials are listed alongside active ones. Unlike a
deleted tenant, a withdrawn key is an operational fact an operator needs to
see: it answers what was issued, what was withdrawn, and when.
*/
func (store *Store) List(ctx context.Context, applicationID string, cursor *Cursor, limit int) ([]Credential, bool, error) {
	statement := `SELECT ` + columns + ` FROM credentials WHERE application_id = $1`
	arguments := []any{applicationID}

	if cursor != nil {
		statement += ` AND (created_at, id) < ($2, $3)`
		arguments = append(arguments, cursor.CreatedAt, cursor.ID)
	}
	statement += ` ORDER BY created_at DESC, id DESC LIMIT ` + strconv.Itoa(limit+1)

	rows, err := store.pool.Query(ctx, statement, arguments...)
	if err != nil {
		return nil, false, fmt.Errorf("query credentials: %w", err)
	}

	records, err := pgx.CollectRows(rows, pgx.RowToStructByPos[row])
	if err != nil {
		return nil, false, fmt.Errorf("read credentials: %w", err)
	}

	page := make([]Credential, 0, len(records))
	for _, record := range records {
		page = append(page, record.credential())
	}

	if len(page) > limit {
		return page[:limit], true, nil
	}
	return page, false, nil
}

/*
Revoke withdraws a credential.

The timestamp is only set once, so revoking an already-revoked credential
succeeds and reports that nothing changed. That keeps a repeated request safe
and the audit trail honest about when the key actually stopped working.
*/
func (store *Store) Revoke(ctx context.Context, applicationID, id string, at time.Time) (bool, error) {
	const statement = `UPDATE credentials SET revoked_at = $1
	                   WHERE application_id = $2 AND id = $3 AND revoked_at IS NULL`

	tag, err := store.pool.Exec(ctx, statement, at, applicationID, id)
	if err != nil {
		return false, fmt.Errorf("revoke credential: %w", err)
	}
	if tag.RowsAffected() > 0 {
		return true, nil
	}

	// Nothing was updated: either the credential is already revoked, or it is
	// not this application's to revoke.
	if _, err := store.Get(ctx, applicationID, id); err != nil {
		return false, err
	}
	return false, nil
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
