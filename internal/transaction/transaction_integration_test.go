package transaction

import (
	"context"
	"crypto/rand"
	"errors"
	"io"
	"log/slog"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"convia/internal/config"
	"convia/internal/database"
)

/*
This package had no tests at all, which `M24-009` found by measuring rather
than by anybody noticing: it is a hundred lines, it looks like plumbing, and
every write in Convia goes through it.

What it actually holds is the rule that an event is announced only if the
change it describes committed. A subscriber told about a message that was
rolled back is told something that did not happen, and there is no later
correction — the event is the record.
*/

const testDatabaseURLEnvironment = "CONVIA_TEST_DATABASE_URL"

// pooled opens a pool against a database of this test's own.
func pooled(t *testing.T) *pgxpool.Pool {
	t.Helper()

	maintenance := strings.TrimSpace(os.Getenv(testDatabaseURLEnvironment))
	if maintenance == "" {
		t.Skipf("set %s to run the transaction integration tests", testDatabaseURLEnvironment)
	}

	name := "convia_test_" + strings.ToLower(rand.Text()[:16])
	run(t, maintenance, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize())
	t.Cleanup(func() { run(t, maintenance, "DROP DATABASE "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)") })

	parsed, err := url.Parse(maintenance)
	if err != nil {
		t.Fatalf("parse %s: %v", testDatabaseURLEnvironment, err)
	}
	parsed.Path = "/" + name

	pool, err := database.Open(context.Background(), config.Database{
		URL:            parsed.String(),
		MaxConnections: 4,
		ConnectTimeout: 10 * time.Second,
		QueryTimeout:   5 * time.Second,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(pool.Close)

	// One table, so the tests can watch work appear and disappear.
	if _, err := pool.Exec(context.Background(),
		`CREATE TABLE written (note TEXT PRIMARY KEY)`); err != nil {
		t.Fatalf("create the test table: %v", err)
	}

	/*
		And one whose uniqueness is checked at COMMIT rather than at INSERT.

		It is the only way to make a transaction whose work succeeds and whose
		commit fails, which is the case that separates announcing after the
		commit from announcing after the work.
	*/
	if _, err := pool.Exec(context.Background(),
		`CREATE TABLE deferred (
			note TEXT,
			CONSTRAINT deferred_note_key UNIQUE (note) DEFERRABLE INITIALLY DEFERRED
		)`); err != nil {
		t.Fatalf("create the deferred table: %v", err)
	}
	return pool
}

func run(t *testing.T, databaseURL, statement string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	connection, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = connection.Close(ctx) }()

	if _, err := connection.Exec(ctx, statement); err != nil {
		t.Fatalf("execute %q: %v", statement, err)
	}
}

// notes reads back what survived.
func notes(t *testing.T, pool *pgxpool.Pool) []string {
	t.Helper()

	rows, err := pool.Query(context.Background(), `SELECT note FROM written ORDER BY note`)
	if err != nil {
		t.Fatalf("read the table: %v", err)
	}

	read, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatalf("collect the rows: %v", err)
	}
	return read
}

// write puts one row through whatever the context carries.
func write(ctx context.Context, pool *pgxpool.Pool, note string) error {
	_, err := On(ctx, pool).Exec(ctx, `INSERT INTO written (note) VALUES ($1)`, note)
	return err
}

/*
TestNothingAfterCommitRunsWhenTheWorkFails is the property the package exists
for.

Convia announces an event by registering it here and letting the transaction
decide. If a callback ran when the work was rolled back, a subscriber would be
told about a message nobody wrote — and there is no later correction, because
the event *is* the record.
*/
func TestNothingAfterCommitRunsWhenTheWorkFails(t *testing.T) {
	pool := pooled(t)
	announced := 0

	failed := errors.New("the work decided against it")
	err := Run(context.Background(), pool, func(ctx context.Context) error {
		if err := write(ctx, pool, "a"); err != nil {
			return err
		}
		AfterCommit(ctx, func(context.Context) { announced++ })
		return failed
	})

	if !errors.Is(err, failed) {
		t.Fatalf("Run() error = %v, want the work's own", err)
	}
	if announced != 0 {
		t.Errorf("%d announcements were made about work that rolled back", announced)
	}
	if written := notes(t, pool); len(written) != 0 {
		t.Errorf("the rolled-back work survived: %v", written)
	}
}

// TestWhatCommittedIsAnnounced: the other half, so the first is not passing by
// announcing nothing ever.
func TestWhatCommittedIsAnnounced(t *testing.T) {
	pool := pooled(t)
	announced := 0

	if err := Run(context.Background(), pool, func(ctx context.Context) error {
		AfterCommit(ctx, func(context.Context) { announced++ })
		return write(ctx, pool, "a")
	}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if announced != 1 {
		t.Errorf("announcements = %d, want 1", announced)
	}
	if written := notes(t, pool); len(written) != 1 || written[0] != "a" {
		t.Errorf("the committed work is %v, want one row", written)
	}
}

/*
TestACommitThatFailsAnnouncesNothingEither.

The work succeeding is not the thing that makes an event true — **the commit
is**, and the two are not the same moment. A constraint checked at COMMIT is
the case that separates them: every statement succeeded, and the transaction
still did not happen.

Announcing after the work rather than after the commit passes every other test
in this file, which is why this one is here.
*/
func TestACommitThatFailsAnnouncesNothingEither(t *testing.T) {
	pool := pooled(t)
	announced := 0

	err := Run(context.Background(), pool, func(ctx context.Context) error {
		for range 2 {
			if _, err := On(ctx, pool).Exec(ctx,
				`INSERT INTO deferred (note) VALUES ('a')`); err != nil {
				return err
			}
		}
		AfterCommit(ctx, func(context.Context) { announced++ })
		return nil
	})

	if err == nil {
		t.Fatal("a transaction with a duplicate committed, so the constraint is not deferred")
	}
	if announced != 0 {
		t.Errorf("%d announcements were made about a transaction that failed to commit", announced)
	}

	var rows int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM deferred`).Scan(&rows); err != nil {
		t.Fatalf("count the deferred table: %v", err)
	}
	if rows != 0 {
		t.Errorf("%d rows survived a failed commit", rows)
	}
}

/*
TestAnAnnouncementCarriesNoTransaction.

The callback runs after the commit, so the transaction it was registered in is
closed. Handing it that transaction would give it a handle whose every use is
an error — and the work it does is its own, which is why it gets the context
the transaction was opened from.
*/
func TestAnAnnouncementCarriesNoTransaction(t *testing.T) {
	pool := pooled(t)
	var carried error

	if err := Run(context.Background(), pool, func(ctx context.Context) error {
		AfterCommit(ctx, func(after context.Context) {
			_, carried = Current(after)
		})
		return nil
	}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if !errors.Is(carried, ErrNone) {
		t.Errorf("the announcement was given a transaction: %v", carried)
	}
}

/*
TestNestingJoinsRatherThanNests.

A change that calls into another domain must commit or fail as one thing. The
inner Run joins the outer rather than opening a second transaction, so an inner
failure takes the outer's writes with it — which is the whole reason a domain
can call another without either knowing about transactions.
*/
func TestNestingJoinsRatherThanNests(t *testing.T) {
	pool := pooled(t)
	announced := 0

	failed := errors.New("the inner work decided against it")
	err := Run(context.Background(), pool, func(outer context.Context) error {
		if err := write(outer, pool, "outer"); err != nil {
			return err
		}
		AfterCommit(outer, func(context.Context) { announced++ })

		return Run(outer, pool, func(inner context.Context) error {
			if err := write(inner, pool, "inner"); err != nil {
				return err
			}
			return failed
		})
	})

	if !errors.Is(err, failed) {
		t.Fatalf("Run() error = %v, want the inner work's own", err)
	}
	if written := notes(t, pool); len(written) != 0 {
		t.Errorf("an inner failure left the outer's work behind: %v", written)
	}
	if announced != 0 {
		t.Errorf("%d announcements survived a rollback the inner work caused", announced)
	}
}

/*
TestWithoutATransactionThereIsNothingToWaitFor.

Not every write is part of a change that spans stores. Outside a transaction,
On answers with the pool and AfterCommit runs at once — otherwise a caller
that did not open one would register announcements nobody ever makes.
*/
func TestWithoutATransactionThereIsNothingToWaitFor(t *testing.T) {
	pool := pooled(t)
	ctx := context.Background()

	if _, err := Current(ctx); !errors.Is(err, ErrNone) {
		t.Errorf("Current() outside a transaction = %v, want ErrNone", err)
	}

	announced := 0
	AfterCommit(ctx, func(context.Context) { announced++ })
	if announced != 1 {
		t.Errorf("an announcement outside a transaction ran %d times, want at once", announced)
	}

	if err := write(ctx, pool, "a"); err != nil {
		t.Fatalf("a write outside a transaction failed: %v", err)
	}
	if written := notes(t, pool); len(written) != 1 {
		t.Errorf("a write outside a transaction is %v, want one row", written)
	}
}
