package operator

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

	"convia/internal/transaction"
)

// columns is the projection every read shares. The digest is never among them.
const columns = "id, name, scopes, created_at, expires_at, revoked_at"

/*
Store persists operator credentials in PostgreSQL.

No statement here mentions an application, because an operator credential
belongs to none. That is the structural half of the separation: this store
reads one table, the credentials store reads another, and neither can return
the other's rows however a query is written.
*/
type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// db is the transaction the context carries, or the pool; see package transaction.
func (store *Store) db(ctx context.Context) transaction.Querier {
	return transaction.On(ctx, store.pool)
}

// row mirrors the projection. Scopes are read as plain strings and converted
// afterwards, so storage does not depend on the driver knowing a domain type.
type row struct {
	ID        string
	Name      string
	Scopes    []string
	CreatedAt time.Time
	ExpiresAt *time.Time
	RevokedAt *time.Time
}

func (record row) credential() Credential {
	scopes := make([]Scope, 0, len(record.Scopes))
	for _, scope := range record.Scopes {
		scopes = append(scopes, Scope(scope))
	}

	return Credential{
		ID:        record.ID,
		Name:      record.Name,
		Scopes:    scopes,
		CreatedAt: record.CreatedAt.UTC(),
		ExpiresAt: inUTC(record.ExpiresAt),
		RevokedAt: inUTC(record.RevokedAt),
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

// Create issues an operator credential, storing the digest rather than the secret.
func (store *Store) Create(ctx context.Context, credential Credential, digest []byte) error {
	const statement = `INSERT INTO operator_credentials (id, name, secret_hash, scopes, created_at, expires_at)
	                   VALUES ($1, $2, $3, $4, $5, $6)`

	_, err := store.db(ctx).Exec(ctx, statement,
		credential.ID,
		credential.Name,
		digest,
		texts(credential.Scopes),
		credential.CreatedAt,
		credential.ExpiresAt,
	)
	if err != nil {
		return fmt.Errorf("insert operator credential: %w", err)
	}
	return nil
}

// Get returns one operator credential.
func (store *Store) Get(ctx context.Context, id string) (Credential, error) {
	const statement = `SELECT ` + columns + ` FROM operator_credentials WHERE id = $1`

	rows, err := store.db(ctx).Query(ctx, statement, id)
	if err != nil {
		return Credential{}, fmt.Errorf("query operator credential: %w", err)
	}

	record, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByPos[row])
	if errors.Is(err, pgx.ErrNoRows) {
		return Credential{}, ErrNotFound
	}
	if err != nil {
		return Credential{}, fmt.Errorf("read operator credential: %w", err)
	}
	return record.credential(), nil
}

/*
ForAuthentication returns a credential and its digest by identifier.

The digest leaves the store only here, and only to be compared in constant time
by the caller. The identifier comes from a presented key whose shape was
already checked, so a malformed one never reaches this query.
*/
func (store *Store) ForAuthentication(ctx context.Context, id string) (Credential, []byte, error) {
	const statement = `SELECT ` + columns + `, secret_hash FROM operator_credentials WHERE id = $1`

	type authenticating struct {
		row
		SecretHash []byte
	}

	rows, err := store.db(ctx).Query(ctx, statement, id)
	if err != nil {
		return Credential{}, nil, fmt.Errorf("query operator credential for authentication: %w", err)
	}

	record, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByPos[authenticating])
	if errors.Is(err, pgx.ErrNoRows) {
		return Credential{}, nil, ErrNotFound
	}
	if err != nil {
		return Credential{}, nil, fmt.Errorf("read operator credential for authentication: %w", err)
	}
	return record.credential(), record.SecretHash, nil
}

/*
List returns a page of operator credentials, newest first.

Revoked and expired credentials are listed alongside active ones, because what
was issued and when it stopped working is exactly what an operator needs during
an incident.
*/
func (store *Store) List(ctx context.Context, cursor *Cursor, limit int) ([]Credential, bool, error) {
	statement := `SELECT ` + columns + ` FROM operator_credentials`
	arguments := []any{}

	if cursor != nil {
		statement += ` WHERE (created_at, id) < ($1, $2)`
		arguments = append(arguments, cursor.CreatedAt, cursor.ID)
	}
	statement += ` ORDER BY created_at DESC, id DESC LIMIT ` + strconv.Itoa(limit+1)

	rows, err := store.db(ctx).Query(ctx, statement, arguments...)
	if err != nil {
		return nil, false, fmt.Errorf("query operator credentials: %w", err)
	}

	records, err := pgx.CollectRows(rows, pgx.RowToStructByPos[row])
	if err != nil {
		return nil, false, fmt.Errorf("read operator credentials: %w", err)
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
Revoke withdraws an operator credential.

The timestamp is only set once, so revoking an already-revoked credential
succeeds and reports that nothing changed. That keeps a repeated request safe
and the record honest about when the key actually stopped working.
*/
func (store *Store) Revoke(ctx context.Context, id string, at time.Time) (bool, error) {
	const statement = `UPDATE operator_credentials SET revoked_at = $1
	                   WHERE id = $2 AND revoked_at IS NULL`

	tag, err := store.db(ctx).Exec(ctx, statement, at, id)
	if err != nil {
		return false, fmt.Errorf("revoke operator credential: %w", err)
	}
	if tag.RowsAffected() > 0 {
		return true, nil
	}

	// Nothing was updated: either the credential is already revoked, or it
	// does not exist.
	if _, err := store.Get(ctx, id); err != nil {
		return false, err
	}
	return false, nil
}

/*
CountActive reports how many operator credentials currently authenticate.

It exists for the bootstrap check: Convia warns at startup when no operator can
reach the administrative API, because that is a state an operator almost never
intends and would otherwise discover only when locked out.
*/
func (store *Store) CountActive(ctx context.Context, at time.Time) (int, error) {
	const statement = `SELECT count(*) FROM operator_credentials
	                   WHERE revoked_at IS NULL AND (expires_at IS NULL OR expires_at > $1)`

	var count int
	if err := store.db(ctx).QueryRow(ctx, statement, at).Scan(&count); err != nil {
		return 0, fmt.Errorf("count active operator credentials: %w", err)
	}
	return count, nil
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
