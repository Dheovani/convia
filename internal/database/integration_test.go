package database

import (
	"context"
	"crypto/rand"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"convia/internal/config"
)

/*
testDatabaseURLEnvironment points these tests at a PostgreSQL instance.

The tests are skipped when it is unset, so `go test ./...` stays runnable
without infrastructure, and CI always sets it so the coverage is never optional
where it matters. The URL must grant permission to create databases: every test
runs against its own database and drops it afterwards, which keeps test runs
isolated from each other and from developer data.
*/
const testDatabaseURLEnvironment = "CONVIA_TEST_DATABASE_URL"

// newTestDatabase creates an isolated database and returns its connection URL.
func newTestDatabase(t *testing.T) string {
	t.Helper()

	maintenanceURL := strings.TrimSpace(os.Getenv(testDatabaseURLEnvironment))
	if maintenanceURL == "" {
		t.Skipf("set %s to run the database integration tests", testDatabaseURLEnvironment)
	}

	name := "convia_test_" + strings.ToLower(rand.Text()[:16])
	execute(t, maintenanceURL, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize())
	t.Cleanup(func() {
		execute(t, maintenanceURL, "DROP DATABASE "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)")
	})

	parsed, err := url.Parse(maintenanceURL)
	if err != nil {
		t.Fatalf("parse %s: %v", testDatabaseURLEnvironment, err)
	}
	parsed.Path = "/" + name
	return parsed.String()
}

// execute runs one statement on the maintenance database.
func execute(t *testing.T, databaseURL, statement string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	connection, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect to the maintenance database: %v", err)
	}
	defer func() {
		if err := connection.Close(ctx); err != nil {
			t.Errorf("close the maintenance connection: %v", err)
		}
	}()

	if _, err := connection.Exec(ctx, statement); err != nil {
		t.Fatalf("execute %q: %v", statement, err)
	}
}

func openTestPool(t *testing.T, databaseURL string) *pgxpool.Pool {
	t.Helper()

	pool, err := Open(context.Background(), config.Database{
		URL:            databaseURL,
		MaxConnections: 4,
		ConnectTimeout: 10 * time.Second,
		QueryTimeout:   5 * time.Second,
	}, discardLogger())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(pool.Close)

	return pool
}

// TestMigrateCreatesTheApplicationsTable verifies the first migration end to end.
func TestMigrateCreatesTheApplicationsTable(t *testing.T) {
	databaseURL := newTestDatabase(t)

	if err := Migrate(context.Background(), databaseURL, discardLogger()); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	columns := columnTypes(t, openTestPool(t, databaseURL), "applications")
	expected := map[string]string{
		"id":         "text",
		"name":       "text",
		"status":     "text",
		"created_at": "timestamp with time zone",
		"updated_at": "timestamp with time zone",
	}

	for column, dataType := range expected {
		if columns[column] != dataType {
			t.Errorf("column %s has type %q, want %q", column, columns[column], dataType)
		}
	}
	if len(columns) != len(expected) {
		t.Errorf("applications has %d columns, want %d", len(columns), len(expected))
	}
}

/*
TestApplicationsConstraintsRejectInvalidRows proves that the invariants encoded
in the migration are enforced by PostgreSQL rather than only by convention.
*/
func TestApplicationsConstraintsRejectInvalidRows(t *testing.T) {
	databaseURL := newTestDatabase(t)
	ctx := context.Background()

	if err := Migrate(ctx, databaseURL, discardLogger()); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	pool := openTestPool(t, databaseURL)

	const insert = `INSERT INTO applications (id, name, status, created_at, updated_at)
	                VALUES ($1, $2, $3, $4, $5)`
	now := time.Now().UTC()
	const valid = "app_MXHJAY4MJNX2FO22XWJ3XNCKHT"

	if _, err := pool.Exec(ctx, insert, valid, "Workspace Town", "active", now, now); err != nil {
		t.Fatalf("insert a valid application: %v", err)
	}

	rejected := map[string][]any{
		"sequential identifier":  {"1", "Orbit", "active", now, now},
		"unprefixed identifier":  {"MXHJAY4MJNX2FO22XWJ3XNCKHT", "Orbit", "active", now, now},
		"lowercase identifier":   {"app_mxhjay4mjnx2fo22xwj3xnckht", "Orbit", "active", now, now},
		"duplicate identifier":   {valid, "Orbit", "active", now, now},
		"empty name":             {"app_AXHJAY4MJNX2FO22XWJ3XNCKHT", "", "active", now, now},
		"unknown status":         {"app_BXHJAY4MJNX2FO22XWJ3XNCKHT", "Orbit", "archived", now, now},
		"updated before created": {"app_CXHJAY4MJNX2FO22XWJ3XNCKHT", "Orbit", "active", now, now.Add(-time.Hour)},
	}

	for name, arguments := range rejected {
		t.Run(name, func(t *testing.T) {
			if _, err := pool.Exec(ctx, insert, arguments...); err == nil {
				t.Error("insert succeeded, want the database to reject the row")
			}
		})
	}
}

// TestMigrateIsRepeatable proves that re-running migrations is a safe no-op.
func TestMigrateIsRepeatable(t *testing.T) {
	databaseURL := newTestDatabase(t)

	for attempt := range 2 {
		if err := Migrate(context.Background(), databaseURL, discardLogger()); err != nil {
			t.Fatalf("Migrate() attempt %d error = %v", attempt+1, err)
		}
	}
}

/*
TestRollbackRevertsAndReapplies covers the recovery path an operator needs
after a failed deployment: revert the schema, then move forward again.
*/
func TestRollbackRevertsAndReapplies(t *testing.T) {
	databaseURL := newTestDatabase(t)
	ctx := context.Background()

	if err := Migrate(ctx, databaseURL, discardLogger()); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	if err := Rollback(ctx, databaseURL, discardLogger()); err != nil {
		t.Fatalf("Rollback() error = %v", err)
	}

	pool := openTestPool(t, databaseURL)
	if tableExists(t, pool, "applications") {
		t.Error("applications still exists after a rollback")
	}

	if err := Migrate(ctx, databaseURL, discardLogger()); err != nil {
		t.Fatalf("Migrate() after rollback error = %v", err)
	}
	if !tableExists(t, pool, "applications") {
		t.Error("applications is missing after reapplying the migration")
	}
}

func TestStatusReportsMigrations(t *testing.T) {
	databaseURL := newTestDatabase(t)

	if err := Status(context.Background(), databaseURL, discardLogger()); err != nil {
		t.Fatalf("Status() error = %v", err)
	}
}

/*
TestQueryHonorsContextCancellation proves that a caller's deadline stops a slow
query, which is what keeps a stalled database from exhausting the pool.
*/
func TestQueryHonorsContextCancellation(t *testing.T) {
	pool := openTestPool(t, newTestDatabase(t))

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	start := time.Now()
	if _, err := pool.Exec(ctx, "SELECT pg_sleep(5)"); err == nil {
		t.Fatal("Exec() error = nil, want the query to be cancelled")
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("the query took %s, want it cancelled near the deadline", elapsed)
	}
}

func TestPingReportsAHealthyDatabase(t *testing.T) {
	pool := openTestPool(t, newTestDatabase(t))

	if err := pool.Ping(context.Background()); err != nil {
		t.Errorf("Ping() error = %v", err)
	}
}

func columnTypes(t *testing.T, pool *pgxpool.Pool, table string) map[string]string {
	t.Helper()

	rows, err := pool.Query(context.Background(),
		`SELECT column_name, data_type
		 FROM information_schema.columns
		 WHERE table_schema = 'public' AND table_name = $1`, table)
	if err != nil {
		t.Fatalf("read the columns of %s: %v", table, err)
	}
	defer rows.Close()

	columns := make(map[string]string)
	for rows.Next() {
		var name, dataType string
		if err := rows.Scan(&name, &dataType); err != nil {
			t.Fatalf("scan a column of %s: %v", table, err)
		}
		columns[name] = dataType
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate the columns of %s: %v", table, err)
	}
	return columns
}

func tableExists(t *testing.T, pool *pgxpool.Pool, table string) bool {
	t.Helper()

	var exists bool
	err := pool.QueryRow(context.Background(),
		`SELECT EXISTS (
		     SELECT 1 FROM information_schema.tables
		     WHERE table_schema = 'public' AND table_name = $1
		 )`, table).Scan(&exists)
	if err != nil {
		t.Fatalf("check whether %s exists: %v", table, err)
	}
	return exists
}
