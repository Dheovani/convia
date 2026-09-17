/*
Package transaction lets one change span several stores, and the event it
announces, in a single PostgreSQL transaction.

Each store owns its statements and none takes a transaction from another
package. What a change needs to be atomic with — above all the record of the
event it announces — is carried in the context instead: [Run] opens a
transaction and puts it there, and every store reaches the database through
[On], which answers with that transaction when there is one and with the pool
when there is not. A store that opens its own transaction inside one gets a
savepoint, so its rollback undoes only its own work.

The alternative was a transaction parameter on every write method in every
domain, which would have coupled domains that do not otherwise know about each
other. See docs/adr/0017.
*/
package transaction

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNone reports work that must run inside a transaction and was not given one.
var ErrNone = errors.New("no transaction is open")

// Querier is what a store runs statements on.
type Querier interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, arguments ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, arguments ...any) pgx.Row
	Begin(ctx context.Context) (pgx.Tx, error)
}

type key struct{}

// open is the transaction a context carries, and what waits for it to commit.
type open struct {
	tx        pgx.Tx
	committed []func(ctx context.Context)
}

func from(ctx context.Context) (*open, bool) {
	current, found := ctx.Value(key{}).(*open)
	return current, found
}

// On returns the transaction the context carries, or the pool when it carries none.
func On(ctx context.Context, pool *pgxpool.Pool) Querier {
	if current, found := from(ctx); found {
		return current.tx
	}
	return pool
}

// Current returns the transaction the context carries, or ErrNone.
func Current(ctx context.Context) (pgx.Tx, error) {
	if current, found := from(ctx); found {
		return current.tx, nil
	}
	return nil, ErrNone
}

/*
AfterCommit runs something once the context's transaction has committed, and
never if it does not. It is given the context the transaction was opened from,
which carries no transaction, so what it does is its own work. Without a
transaction it runs at once, because there is nothing left to wait for.
*/
func AfterCommit(ctx context.Context, do func(ctx context.Context)) {
	if current, found := from(ctx); found {
		current.committed = append(current.committed, do)
		return
	}
	do(ctx)
}

/*
Run calls work inside a transaction on pool, and commits it if work succeeds.

A context that already carries a transaction joins it rather than opening
another, so a change that calls into another domain commits or fails as one.
*/
func Run(ctx context.Context, pool *pgxpool.Pool, work func(ctx context.Context) error) error {
	if _, found := from(ctx); found {
		return work(ctx)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin a transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	current := &open{tx: tx}
	if err := work(context.WithValue(ctx, key{}, current)); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit a transaction: %w", err)
	}

	for _, do := range current.committed {
		do(ctx)
	}
	return nil
}
