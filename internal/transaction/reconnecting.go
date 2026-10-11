package transaction

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

/*
reconnecting is the pool, with one second chance for a statement that provably
never ran.

It is `M25-006`'s finding. When PostgreSQL restarts, fails over, or has its
backends terminated, every connection the pool holds is dead, and the pool does
not know until each one is used: a test that terminated four pooled
connections saw the next four queries fail, one per corpse. On an instance with
forty connections that is forty requests answered with 500 for nothing they did.

**What is retried is what the server never read.** A terminated backend leaves
a FATAL notice on its connection -- `57P01` when an administrator ended it,
`57P02` when the server crashed, `57P03` while it is starting -- and that notice
is what the next statement reads instead of an answer, because the statement
went to a socket nobody was listening on. pgx's own "safe to retry" covers the
other case, a statement that failed before a byte of it was sent. Neither has
run, so running it once more on a fresh connection is not running it twice.

**How many times depends on which proof it is.** A termination notice comes only
from a connection the pool already held, and pgx destroys a connection that
delivered one, so each attempt removes one corpse: the first test of this
retried once and still failed twice, because the retry was handed the next dead
connection in the same pool. So a notice is retried until the pool has had the
chance to hand out every connection it holds, which ends by itself. "Safe to
retry" also covers failing to connect at all, which is what a database that is
really down looks like, and that is retried once: repeating a connection
timeout forty times is not recovering, it is waiting forty times as long to say
the same thing.
*/
type reconnecting struct {
	pool *pgxpool.Pool
}

// terminated reports the notice a connection the server ended leaves behind.
func terminated(err error) bool {
	var failure *pgconn.PgError
	if !errors.As(err, &failure) || failure.Severity != "FATAL" {
		return false
	}

	switch failure.Code {
	case "57P01", "57P02", "57P03":
		return true
	}

	return false
}

/*
again runs attempt until it succeeds or fails for a reason that is not a second
chance: once more after "safe to retry", and once per connection the pool holds
after a termination notice.
*/
func again[T any](held int32, attempt func() (T, error)) (T, error) {
	result, err := attempt()
	for tries := int32(0); err != nil && tries <= held; tries++ {
		switch {
		case terminated(err):
		case tries == 0 && pgconn.SafeToRetry(err):
		default:
			return result, err
		}
		result, err = attempt()
	}
	return result, err
}

func (pool reconnecting) Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error) {
	return again(pool.pool.Config().MaxConns, func() (pgconn.CommandTag, error) { return pool.pool.Exec(ctx, sql, arguments...) })
}

func (pool reconnecting) Query(ctx context.Context, sql string, arguments ...any) (pgx.Rows, error) {
	return again(pool.pool.Config().MaxConns, func() (pgx.Rows, error) { return pool.pool.Query(ctx, sql, arguments...) })
}

func (pool reconnecting) QueryRow(ctx context.Context, sql string, arguments ...any) pgx.Row {
	return retriedRow{pool: pool.pool, ctx: ctx, sql: sql, arguments: arguments}
}

func (pool reconnecting) Begin(ctx context.Context) (pgx.Tx, error) {
	return again(pool.pool.Config().MaxConns, func() (pgx.Tx, error) { return pool.pool.Begin(ctx) })
}

/*
retriedRow defers the query to Scan, which is where pgx does the work of a
single-row query and therefore where a dead connection is found out.
*/
type retriedRow struct {
	pool      *pgxpool.Pool
	ctx       context.Context
	sql       string
	arguments []any
}

func (row retriedRow) Scan(destinations ...any) error {
	_, err := again(row.pool.Config().MaxConns, func() (struct{}, error) {
		return struct{}{}, row.pool.QueryRow(row.ctx, row.sql, row.arguments...).Scan(destinations...)
	})
	return err
}
