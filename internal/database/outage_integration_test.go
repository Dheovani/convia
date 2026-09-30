package database

import (
	"context"
	"errors"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

/*
TestThePoolRecoversWhenPostgreSQLDropsEveryConnection is `M24-013` for the
dependency Convia cannot do without.

Every other failure here is tested by handing a store an error and reading the
answer. **This one cannot be**: what it asks is whether a pool that was working,
and then had every connection taken away underneath it, works again afterwards —
and the answer is a property of pgxpool and of how Convia configures it rather
than of any code in this repository.

It matters because the alternative is an installation that survives a database
restart as a process and not as a service: up, answering, and failing every
request until somebody notices and restarts it. That failure looks like Convia
being broken and is invisible to a liveness check.

The connections are dropped the way an operator's restart drops them — from
inside PostgreSQL — because a test that closed the pool would be testing the
pool's own Close.
*/
func TestThePoolRecoversWhenPostgreSQLDropsEveryConnection(t *testing.T) {
	databaseURL := newTestDatabase(t)
	pool := openTestPool(t, databaseURL)
	ctx := context.Background()

	var before int
	if err := pool.QueryRow(ctx, "SELECT 1").Scan(&before); err != nil {
		t.Fatalf("the pool did not work before the outage: %v", err)
	}

	/*
		Every backend on this database except the one doing the terminating.
		A pool holds connections idle, so this is what an operator restarting
		PostgreSQL does to them.
	*/
	execute(t, maintenanceURL(t), `
		SELECT pg_terminate_backend(pid)
		FROM pg_stat_activity
		WHERE datname = `+quoted(nameOf(t, databaseURL))+` AND pid <> pg_backend_pid()`)

	/*
		The first query after may fail: the pool hands out a connection it does
		not yet know is dead, and finds out when it writes to it. What must not
		happen is that it keeps failing.
	*/
	var recovered bool
	var last error
	for attempt := range 5 {
		var after int
		if err := pool.QueryRow(ctx, "SELECT 1").Scan(&after); err == nil {
			recovered = true
			t.Logf("the pool recovered on attempt %d", attempt+1)
			break
		} else {
			last = err
		}
		time.Sleep(100 * time.Millisecond)
	}

	if !recovered {
		t.Fatalf("the pool never recovered from losing its connections: %v", last)
	}
}

/*
TestAQueryAgainstADatabaseThatWentAwayFailsRatherThanHangs.

An outage that returns an error is an incident. An outage that blocks is an
incident that also consumes every goroutine that touches it, and the second is
how one dependency being down becomes an installation being down.

The query timeout is what separates them, so this is really a test that the
timeout Convia configures reaches the query rather than being set and ignored.
*/
func TestAQueryAgainstADatabaseThatWentAwayFailsRatherThanHangs(t *testing.T) {
	pool := openTestPool(t, newTestDatabase(t))

	/*
		pg_sleep outlasts the five-second query timeout the fixture sets, which
		is the same shape as a database that accepted a query and stopped
		answering.
	*/
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	start := time.Now()
	_, err := pool.Exec(ctx, "SELECT pg_sleep(15)")
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("a query that should have outlasted its timeout succeeded")
	}
	if errors.Is(err, context.DeadlineExceeded) && elapsed >= 15*time.Second {
		t.Fatalf("the query ran to completion in %s and only the caller's context stopped it", elapsed)
	}
	if elapsed > 10*time.Second {
		t.Errorf("the query took %s to fail, want the configured timeout to end it sooner", elapsed)
	}
}

/*
maintenanceURL is where a test connects to act on another database.

Dropping a pool's connections has to be done from outside the database holding
them, because the statement that does it would otherwise be terminating the
connection running it.
*/
func maintenanceURL(t *testing.T) string {
	t.Helper()

	raw := strings.TrimSpace(os.Getenv(testDatabaseURLEnvironment))
	if raw == "" {
		t.Skipf("set %s to run the outage tests", testDatabaseURLEnvironment)
	}
	return raw
}

// nameOf reads the database a URL points at, which is what pg_stat_activity
// matches on.
func nameOf(t *testing.T, databaseURL string) string {
	t.Helper()

	parsed, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatalf("parse the test database URL: %v", err)
	}
	return strings.TrimPrefix(parsed.Path, "/")
}

// quoted makes a SQL string literal. The name is this test's own and is
// generated, so this is about producing valid SQL rather than about safety.
func quoted(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}
