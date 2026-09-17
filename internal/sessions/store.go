package sessions

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"convia/internal/transaction"
)

// columns is the projection every read shares. The digest is never among them.
const columns = "id, account_id, created_at, last_seen_at, absolute_expires_at, revoked_at"

// Store persists sessions in PostgreSQL.
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

// row mirrors the projection above.
type row struct {
	ID                string
	AccountID         string
	CreatedAt         time.Time
	LastSeenAt        time.Time
	AbsoluteExpiresAt time.Time
	RevokedAt         *time.Time
}

func (record row) session() Session {
	return Session{
		ID:                record.ID,
		AccountID:         record.AccountID,
		CreatedAt:         record.CreatedAt.UTC(),
		LastSeenAt:        record.LastSeenAt.UTC(),
		AbsoluteExpiresAt: record.AbsoluteExpiresAt.UTC(),
		RevokedAt:         inUTC(record.RevokedAt),
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

// Create stores a new session, with the account's key wrapped for it.
func (store *Store) Create(ctx context.Context, session Session, digest, wrappedIdentity []byte) error {
	const statement = `
		INSERT INTO sessions (id, account_id, secret_hash, wrapped_identity, created_at, last_seen_at,
		                      absolute_expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`

	_, err := store.db(ctx).Exec(ctx, statement, session.ID, session.AccountID, digest, wrappedIdentity,
		session.CreatedAt, session.LastSeenAt, session.AbsoluteExpiresAt)
	if err != nil {
		return fmt.Errorf("insert session: %w", err)
	}
	return nil
}

/*
WrappedIdentity reads the key a session holds, still wrapped.

It is a statement of its own, like the digest's, so that nothing that lists or
touches sessions ever carries one.
*/
func (store *Store) WrappedIdentity(ctx context.Context, id string) ([]byte, error) {
	var wrapped []byte
	err := store.db(ctx).QueryRow(ctx, `SELECT wrapped_identity FROM sessions WHERE id = $1`, id).Scan(&wrapped)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return nil, ErrNotFound
	case err != nil:
		return nil, fmt.Errorf("read session key: %w", err)
	}
	return wrapped, nil
}

/*
Credentials reads what verifying a presented token needs.

It is the one statement that reads a digest, and it answers for a session in
any state — revoked, idle, expired. Deciding what those mean is the service's
job: filtering here would make the store's choice of rows the thing that
distinguishes a revoked session from an unknown one, and the whole point is
that a caller cannot tell them apart.
*/
func (store *Store) Credentials(ctx context.Context, id string) (Session, []byte, error) {
	const statement = `SELECT ` + columns + `, secret_hash FROM sessions WHERE id = $1`

	var (
		record row
		digest []byte
	)
	err := store.db(ctx).QueryRow(ctx, statement, id).Scan(&record.ID, &record.AccountID,
		&record.CreatedAt, &record.LastSeenAt, &record.AbsoluteExpiresAt, &record.RevokedAt, &digest)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return Session{}, nil, ErrNotFound
	case err != nil:
		return Session{}, nil, fmt.Errorf("read session: %w", err)
	}

	return record.session(), digest, nil
}

/*
Touch advances the last-use timestamp, but only if it is stale.

The staleness test is in the statement rather than in Go, deliberately. A SPA
issues several requests at once, so a read-then-write in the service would have
them racing to write the same value — harmless in outcome and pure contention in
practice. Here the database decides, one round trip, and concurrent requests
collapse into at most one write.
*/
func (store *Store) Touch(ctx context.Context, id string, at time.Time) error {
	const statement = `
		UPDATE sessions SET last_seen_at = $2
		WHERE id = $1 AND last_seen_at < $3`

	if _, err := store.db(ctx).Exec(ctx, statement, id, at, at.Add(-RefreshInterval)); err != nil {
		return fmt.Errorf("touch session: %w", err)
	}
	return nil
}

/*
Revoke ends one session.

Revoking one that is already revoked changes nothing and is not an error: a
person pressing sign out twice, or a client retrying after a lost response,
must not be told that their own tidying-up failed.
*/
func (store *Store) Revoke(ctx context.Context, id string, at time.Time) error {
	const statement = `UPDATE sessions SET revoked_at = $2 WHERE id = $1 AND revoked_at IS NULL`

	if _, err := store.db(ctx).Exec(ctx, statement, id, at); err != nil {
		return fmt.Errorf("revoke session: %w", err)
	}
	return nil
}

/*
RevokeAllFor ends every session an account holds, optionally sparing one.

The exception exists for changing a password: the person doing it should not be
signed out of the browser they are doing it in, while every other session —
including one an attacker may hold — must end.
*/
func (store *Store) RevokeAllFor(ctx context.Context, accountID string, at time.Time, except string) (int, error) {
	const statement = `
		UPDATE sessions SET revoked_at = $2
		WHERE account_id = $1 AND revoked_at IS NULL AND id <> $3`

	tag, err := store.db(ctx).Exec(ctx, statement, accountID, at, except)
	if err != nil {
		return 0, fmt.Errorf("revoke sessions: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

/*
Live lists the sessions an account currently holds, oldest use first.

The order is what makes eviction sensible: when somebody reaches the ceiling,
the session they have not used in longest is the one to end.
*/
func (store *Store) Live(ctx context.Context, accountID string, at time.Time) ([]Session, error) {
	const statement = `
		SELECT ` + columns + ` FROM sessions
		WHERE account_id = $1 AND revoked_at IS NULL AND absolute_expires_at > $2
		ORDER BY last_seen_at ASC`

	rows, err := store.db(ctx).Query(ctx, statement, accountID, at)
	if err != nil {
		return nil, fmt.Errorf("query sessions: %w", err)
	}

	records, err := pgx.CollectRows(rows, pgx.RowToStructByPos[row])
	if err != nil {
		return nil, fmt.Errorf("read sessions: %w", err)
	}

	live := make([]Session, 0, len(records))
	for _, record := range records {
		if session := record.session(); session.Live(at) {
			live = append(live, session)
		}
	}
	return live, nil
}

/*
Prune deletes sessions that stopped mattering some time ago.

Deriving lifecycle state from timestamps means expiry needs no scheduled job to
take *effect* — which is true, and is not the same as saying the rows go away.
One row per sign-in per device accumulates forever otherwise, and every listing
and revocation walks the dead ones.

The grace period is deliberate: a row is kept for a while after it dies so that
an operator investigating an incident can still see that the session existed.
*/
func (store *Store) Prune(ctx context.Context, before time.Time) (int, error) {
	const statement = `
		DELETE FROM sessions
		WHERE absolute_expires_at < $1
		   OR (revoked_at IS NOT NULL AND revoked_at < $1)
		   OR last_seen_at < $2`

	tag, err := store.db(ctx).Exec(ctx, statement, before, before.Add(-IdleLifetime))
	if err != nil {
		return 0, fmt.Errorf("prune sessions: %w", err)
	}
	return int(tag.RowsAffected()), nil
}
