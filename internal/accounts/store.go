package accounts

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// uniqueViolation is the SQLSTATE PostgreSQL reports for a violated unique
// index. See https://www.postgresql.org/docs/current/errcodes-appendix.html.
const uniqueViolation = "23505"

// usernameUniqueIndex is the index that keeps one username naming one account.
const usernameUniqueIndex = "accounts_username_idx"

/*
columns is the projection every read shares.

The password digest and the sealed key are deliberately absent. One statement
reads them, by username, for the single purpose of verifying a password or
opening a key, and they never travel inside an [Account] — so neither can reach
a response by somebody forgetting to exclude a field.
*/
const columns = "id, username, public_key, user_id, status, created_at, updated_at"

// Store persists accounts in PostgreSQL.
type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// row mirrors the projection above.
type row struct {
	ID        string
	Username  string
	PublicKey []byte
	UserID    string
	Status    string
	CreatedAt time.Time
	UpdatedAt time.Time
}

func (record row) account() Account {
	return Account{
		ID:        record.ID,
		Username:  record.Username,
		PublicKey: record.PublicKey,
		UserID:    record.UserID,
		Status:    Status(record.Status),
		CreatedAt: record.CreatedAt.UTC(),
		UpdatedAt: record.UpdatedAt.UTC(),
	}
}

/*
Create stores a new account.

A duplicate username is reported as [ErrUsernameTaken] rather than as a driver
error, because it is an ordinary outcome of somebody registering rather than a
fault.
*/
func (store *Store) Create(ctx context.Context, account Account, digest Digest, sealed SealedKey) error {
	const statement = `
		INSERT INTO accounts (id, username, password_digest, public_key, sealed_private_key,
		                      user_id, status, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`

	_, err := store.pool.Exec(ctx, statement,
		account.ID, account.Username, string(digest), []byte(account.PublicKey), string(sealed),
		account.UserID, string(account.Status), account.CreatedAt, account.UpdatedAt)
	switch {
	case isUsernameTaken(err):
		return ErrUsernameTaken
	case err != nil:
		return fmt.Errorf("insert account: %w", err)
	}
	return nil
}

/*
UsernameTaken reports whether a username already names an account.

Registering asks this before doing any of its expensive work, so that a name
somebody already holds costs a lookup rather than two argon2id derivations and
a user row nobody will use. The unique index is still what decides a race.
*/
func (store *Store) UsernameTaken(ctx context.Context, username string) (bool, error) {
	const statement = `SELECT EXISTS (SELECT 1 FROM accounts WHERE username = $1)`

	var taken bool
	if err := store.pool.QueryRow(ctx, statement, username).Scan(&taken); err != nil {
		return false, fmt.Errorf("check username: %w", err)
	}
	return taken, nil
}

/*
Credentials reads what signing in and changing a password need: the account, the
digest to compare against, and the sealed key.

It is the one statement that reads either secret, and it is separate from [Get]
so that every other read in Convia is structurally incapable of returning one.
It answers for an account in any lifecycle state — deciding what a suspended one
means is the service's job, and doing it here would let the store leak the
difference through which rows it returns.
*/
func (store *Store) Credentials(ctx context.Context, username string) (Account, Digest, SealedKey, error) {
	const statement = `SELECT ` + columns + `, password_digest, sealed_private_key
	                   FROM accounts WHERE username = $1`

	var (
		record row
		digest string
		sealed string
	)
	err := store.pool.QueryRow(ctx, statement, username).Scan(&record.ID, &record.Username,
		&record.PublicKey, &record.UserID, &record.Status, &record.CreatedAt, &record.UpdatedAt,
		&digest, &sealed)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return Account{}, "", "", ErrNotFound
	case err != nil:
		return Account{}, "", "", fmt.Errorf("read account: %w", err)
	}

	return record.account(), Digest(digest), SealedKey(sealed), nil
}

// Get returns one account by identifier, whatever its lifecycle state.
func (store *Store) Get(ctx context.Context, id string) (Account, error) {
	const statement = `SELECT ` + columns + ` FROM accounts WHERE id = $1`

	rows, err := store.pool.Query(ctx, statement, id)
	if err != nil {
		return Account{}, fmt.Errorf("query account: %w", err)
	}

	record, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByPos[row])
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return Account{}, ErrNotFound
	case err != nil:
		return Account{}, fmt.Errorf("read account: %w", err)
	}
	return record.account(), nil
}

/*
SetSecrets replaces the stored digest and the sealed key together.

Together, because they are derived from one password: a digest from the new one
beside a key sealed by the old would sign somebody in and then fail to open
their own key. It takes the moment as an argument rather than reading a clock,
so that the change and what happens because of it — revoking the other sessions,
rotating this one — share one timestamp.
*/
func (store *Store) SetSecrets(ctx context.Context, id string, digest Digest, sealed SealedKey, at time.Time) error {
	const statement = `UPDATE accounts SET password_digest = $2, sealed_private_key = $3, updated_at = $4
	                   WHERE id = $1`

	tag, err := store.pool.Exec(ctx, statement, id, string(digest), string(sealed), at)
	switch {
	case err != nil:
		return fmt.Errorf("update secrets: %w", err)
	case tag.RowsAffected() == 0:
		return ErrNotFound
	}
	return nil
}

// SetStatus changes an account's lifecycle state.
func (store *Store) SetStatus(ctx context.Context, id string, status Status, at time.Time) (Account, error) {
	const statement = `UPDATE accounts SET status = $2, updated_at = $3 WHERE id = $1 RETURNING ` + columns

	rows, err := store.pool.Query(ctx, statement, id, string(status), at)
	if err != nil {
		return Account{}, fmt.Errorf("update account: %w", err)
	}

	record, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByPos[row])
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return Account{}, ErrNotFound
	case err != nil:
		return Account{}, fmt.Errorf("read account: %w", err)
	}
	return record.account(), nil
}

/*
Delete removes an account, which frees its username. Its sessions and its
pointers to rooms elsewhere go with it, by the foreign keys.
*/
func (store *Store) Delete(ctx context.Context, id string) error {
	tag, err := store.pool.Exec(ctx, `DELETE FROM accounts WHERE id = $1`, id)
	switch {
	case err != nil:
		return fmt.Errorf("delete account: %w", err)
	case tag.RowsAffected() == 0:
		return ErrNotFound
	}
	return nil
}

/*
isUsernameTaken reports the unique index on usernames.

Both the SQLSTATE and the index name are checked, the way internal/rooms
already does it, so that a different unique violation — the primary key, which
two keys with one fingerprint would hit — is not reported as a taken name. The
SQLSTATE is matched rather than the message, which PostgreSQL localizes.
*/
func isUsernameTaken(err error) bool {
	var pgError *pgconn.PgError
	return errors.As(err, &pgError) &&
		pgError.Code == uniqueViolation &&
		pgError.ConstraintName == usernameUniqueIndex
}
