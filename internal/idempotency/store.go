package idempotency

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"convia/internal/transaction"
)

// Store persists idempotency keys in PostgreSQL.
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

// errNoRecord reports a key nothing is stored under.
var errNoRecord = errors.New("no record for this idempotency key")

// record is one stored attempt.
type record struct {
	RequestDigest string
	Status        *int
	Headers       map[string]string
	Body          []byte
	CompletedAt   *time.Time
}

// completed reports whether the attempt finished and has a response to replay.
func (stored record) completed() bool {
	return stored.CompletedAt != nil && stored.Status != nil
}

/*
Reserve claims a key for one request, reporting whether the claim succeeded.

The claim is a single statement so that two requests racing for the same key
cannot both win. An expired row is taken over by the same statement rather
than swept first, which is what makes the contract's retention window hold
without a background job standing between a caller and its answer.

A false return means the key is held by a live attempt, whose record the
caller reads to decide what to answer.
*/
func (store *Store) Reserve(ctx context.Context, attempt Attempt, at time.Time) (bool, error) {
	const statement = `INSERT INTO idempotency_keys
	                   (scope, key, request_digest, created_at, expires_at)
	                   VALUES ($1, $2, $3, $4, $5)
	                   ON CONFLICT (scope, key) DO UPDATE
	                   SET request_digest = EXCLUDED.request_digest,
	                       created_at = EXCLUDED.created_at,
	                       expires_at = EXCLUDED.expires_at,
	                       response_status = NULL,
	                       response_headers = NULL,
	                       response_body = NULL,
	                       completed_at = NULL
	                   WHERE idempotency_keys.expires_at <= EXCLUDED.created_at`

	tag, err := store.db(ctx).Exec(ctx, statement,
		attempt.Scope, attempt.Key, attempt.digest(), at, at.Add(Retention))
	if err != nil {
		return false, fmt.Errorf("reserve idempotency key: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// get returns what is stored under a key.
func (store *Store) get(ctx context.Context, scope, key string) (record, error) {
	const statement = `SELECT request_digest, response_status, response_headers, response_body, completed_at
	                   FROM idempotency_keys WHERE scope = $1 AND key = $2`

	rows, err := store.db(ctx).Query(ctx, statement, scope, key)
	if err != nil {
		return record{}, fmt.Errorf("query idempotency key: %w", err)
	}

	stored, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByPos[record])
	if errors.Is(err, pgx.ErrNoRows) {
		return record{}, errNoRecord
	}
	if err != nil {
		return record{}, fmt.Errorf("read idempotency key: %w", err)
	}
	return stored, nil
}

/*
Complete stores the response an attempt produced, so a repeat can replay it.

Only an open reservation is written, so a stored answer is never replaced by a
later one. What the condition does not do is identify which attempt is writing:
a request that held the key, lost it to expiry, and only then reported its
outcome would write into the reservation that reclaimed it. That needs a
request to outlive the retention window, which is a day against a write timeout
of thirty seconds, so the row is not given an owner to check.
*/
func (store *Store) Complete(ctx context.Context, scope, key string, result Result, at time.Time) error {
	const statement = `UPDATE idempotency_keys
	                   SET response_status = $3, response_headers = $4, response_body = $5, completed_at = $6
	                   WHERE scope = $1 AND key = $2 AND completed_at IS NULL`

	_, err := store.db(ctx).Exec(ctx, statement,
		scope, key, result.Status, headers(result.Headers), result.Body, at)
	if err != nil {
		return fmt.Errorf("complete idempotency key: %w", err)
	}
	return nil
}

/*
Release gives an unfinished key back.

It is how a request that could not answer definitively -- an outage, a refusal
to serve right now -- stops holding a key whose retry deserves a real attempt
rather than a replayed failure.
*/
func (store *Store) Release(ctx context.Context, scope, key string) error {
	const statement = `DELETE FROM idempotency_keys
	                   WHERE scope = $1 AND key = $2 AND completed_at IS NULL`

	if _, err := store.db(ctx).Exec(ctx, statement, scope, key); err != nil {
		return fmt.Errorf("release idempotency key: %w", err)
	}
	return nil
}

/*
headers renders a header set for storage.

An attempt that set no headers stores an empty object rather than NULL, so a
reader never has to tell "none" from "not recorded".
*/
func headers(set map[string]string) map[string]string {
	if set == nil {
		return map[string]string{}
	}
	return set
}
