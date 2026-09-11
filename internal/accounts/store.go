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

// emailUniqueIndex is the index that keeps one address identifying one account.
const emailUniqueIndex = "accounts_email_idx"

/*
columns is the projection every read shares.

The password digest is deliberately absent. One statement reads it, by email,
for the single purpose of verifying a sign-in, and it never travels inside an
[Account] — so a digest cannot reach a response by somebody forgetting to
exclude a field.
*/
const columns = "id, email, display_name, user_id, status, created_at, updated_at"

// Store persists accounts in PostgreSQL.
type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// row mirrors the projection above.
type row struct {
	ID          string
	Email       string
	DisplayName string
	UserID      string
	Status      string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

func (record row) account() Account {
	return Account{
		ID:          record.ID,
		Email:       record.Email,
		DisplayName: record.DisplayName,
		UserID:      record.UserID,
		Status:      Status(record.Status),
		CreatedAt:   record.CreatedAt.UTC(),
		UpdatedAt:   record.UpdatedAt.UTC(),
	}
}

/*
Create stores a new account.

A duplicate email is reported as [ErrEmailTaken] rather than as a driver error,
because it is an ordinary outcome of a request rather than a fault: the address
is the identity, and the caller needs to tell it apart from a failure.
*/
func (store *Store) Create(ctx context.Context, account Account, digest Digest) error {
	const statement = `
		INSERT INTO accounts (id, email, password_digest, display_name, user_id, status, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`

	_, err := store.pool.Exec(ctx, statement,
		account.ID, account.Email, string(digest), account.DisplayName,
		account.UserID, string(account.Status), account.CreatedAt, account.UpdatedAt)
	switch {
	case isEmailTaken(err):
		return ErrEmailTaken
	case err != nil:
		return fmt.Errorf("insert account: %w", err)
	}
	return nil
}

/*
Credentials reads what signing in needs: the account, and the digest to compare
against.

It is the one statement that reads a digest, and it is separate from [Get] so
that every other read in Convia is structurally incapable of returning one. It
answers for an account in any lifecycle state — deciding what a suspended one
means is the service's job, and doing it here would let the store leak the
difference through which rows it returns.
*/
func (store *Store) Credentials(ctx context.Context, email string) (Account, Digest, error) {
	const statement = `SELECT ` + columns + `, password_digest FROM accounts WHERE email = $1`

	rows, err := store.pool.Query(ctx, statement, email)
	if err != nil {
		return Account{}, "", fmt.Errorf("query account: %w", err)
	}

	found, err := pgx.CollectExactlyOneRow(rows, func(scanned pgx.CollectableRow) (struct {
		row
		Digest string
	}, error) {
		var record struct {
			row
			Digest string
		}
		return record, scanned.Scan(&record.ID, &record.Email, &record.DisplayName, &record.UserID,
			&record.Status, &record.CreatedAt, &record.UpdatedAt, &record.Digest)
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return Account{}, "", ErrNotFound
	case err != nil:
		return Account{}, "", fmt.Errorf("read account: %w", err)
	}

	return found.row.account(), Digest(found.Digest), nil
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
SetPassword replaces the stored digest.

It takes the moment as an argument rather than reading a clock, so that the
change and everything that happens because of it — revoking the other sessions,
rotating this one — share one timestamp and cannot be interleaved into an order
that did not happen.
*/
func (store *Store) SetPassword(ctx context.Context, id string, digest Digest, at time.Time) error {
	const statement = `UPDATE accounts SET password_digest = $2, updated_at = $3 WHERE id = $1`

	tag, err := store.pool.Exec(ctx, statement, id, string(digest), at)
	switch {
	case err != nil:
		return fmt.Errorf("update password: %w", err)
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

// CountActive reports how many accounts can sign in, for the startup advisory.
func (store *Store) CountActive(ctx context.Context) (int, error) {
	const statement = `SELECT count(*) FROM accounts WHERE status = $1`

	var total int
	if err := store.pool.QueryRow(ctx, statement, string(StatusActive)).Scan(&total); err != nil {
		return 0, fmt.Errorf("count accounts: %w", err)
	}
	return total, nil
}

/*
isEmailTaken reports the one unique index this table has.

Both the SQLSTATE and the index name are checked, the way internal/rooms
already does it, so that a different unique violation added later is not
silently reported as a taken address. The SQLSTATE is matched rather than the
message, which PostgreSQL localizes.
*/
func isEmailTaken(err error) bool {
	var pgError *pgconn.PgError
	return errors.As(err, &pgError) &&
		pgError.Code == uniqueViolation &&
		pgError.ConstraintName == emailUniqueIndex
}
