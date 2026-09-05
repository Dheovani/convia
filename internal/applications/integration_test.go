package applications

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
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
testDatabaseURLEnvironment points these tests at a PostgreSQL instance.

As in internal/database, the tests are skipped when it is unset so that
`go test ./...` runs without infrastructure, and every test works in its own
database so runs never share state.
*/
const testDatabaseURLEnvironment = "CONVIA_TEST_DATABASE_URL"

func newTestService(t *testing.T) (*Service, *bytes.Buffer) {
	t.Helper()

	maintenanceURL := strings.TrimSpace(os.Getenv(testDatabaseURLEnvironment))
	if maintenanceURL == "" {
		t.Skipf("set %s to run the application integration tests", testDatabaseURLEnvironment)
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
	databaseURL := parsed.String()

	logs := &bytes.Buffer{}
	logger := slog.New(slog.NewJSONHandler(logs, nil))

	if err := database.Migrate(context.Background(), databaseURL, logger); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	pool, err := database.Open(context.Background(), config.Database{
		URL:            databaseURL,
		MaxConnections: 4,
		ConnectTimeout: 10 * time.Second,
		QueryTimeout:   5 * time.Second,
	}, logger)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(pool.Close)

	logs.Reset()
	return NewService(NewStore(pool), logger), logs
}

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

func TestServiceCreateStoresAnApplication(t *testing.T) {
	service, _ := newTestService(t)
	ctx := context.Background()

	created, err := service.Create(ctx, "  Workspace Town  ")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	if !ValidID(created.ID) {
		t.Errorf("ID = %q, want a valid application identifier", created.ID)
	}
	if created.Name != "Workspace Town" {
		t.Errorf("Name = %q, want the trimmed name", created.Name)
	}
	if created.Status != StatusActive {
		t.Errorf("Status = %q, want %q", created.Status, StatusActive)
	}
	if created.CreatedAt.IsZero() || !created.CreatedAt.Equal(created.UpdatedAt) {
		t.Errorf("timestamps = %s and %s, want equal non-zero values", created.CreatedAt, created.UpdatedAt)
	}

	stored, err := service.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if stored != created {
		t.Errorf("Get() = %+v, want %+v", stored, created)
	}
}

// Identifiers are assigned by Convia, so two applications never collide.
func TestCreateAssignsDistinctIdentifiers(t *testing.T) {
	service, _ := newTestService(t)
	ctx := context.Background()

	first, err := service.Create(ctx, "Orbit")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	second, err := service.Create(ctx, "Orbit")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	if first.ID == second.ID {
		t.Errorf("both applications received %q", first.ID)
	}
}

func TestCreateRejectsInvalidNames(t *testing.T) {
	service, _ := newTestService(t)

	if _, err := service.Create(context.Background(), "   "); err == nil {
		t.Fatal("Create() error = nil, want a validation error")
	}
}

func TestServiceGetReportsMissingApplications(t *testing.T) {
	service, _ := newTestService(t)

	for name, id := range map[string]string{
		"unknown":   NewID(),
		"malformed": "not-an-identifier",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := service.Get(context.Background(), id); !errors.Is(err, ErrNotFound) {
				t.Errorf("Get() error = %v, want ErrNotFound", err)
			}
		})
	}
}

/*
TestListPagesNewestFirst walks a full result set through the cursor, which is
the behavior every future list endpoint depends on.
*/
func TestListPagesNewestFirst(t *testing.T) {
	service, _ := newTestService(t)
	ctx := context.Background()

	var created []string
	for _, name := range []string{"First", "Second", "Third", "Fourth", "Fifth"} {
		application, err := service.Create(ctx, name)
		if err != nil {
			t.Fatalf("Create(%q) error = %v", name, err)
		}
		created = append(created, application.ID)
	}

	var seen []string
	options := ListOptions{Limit: 2}
	for pages := 0; ; pages++ {
		if pages > 5 {
			t.Fatal("paging did not terminate")
		}

		page, err := service.List(ctx, options)
		if err != nil {
			t.Fatalf("List() error = %v", err)
		}
		for _, application := range page.Applications {
			seen = append(seen, application.ID)
		}
		if page.NextCursor == "" {
			break
		}
		if len(page.Applications) != 2 {
			t.Errorf("a page before the last returned %d items, want 2", len(page.Applications))
		}
		options.Cursor = page.NextCursor
	}

	if len(seen) != len(created) {
		t.Fatalf("paging returned %d applications, want %d", len(seen), len(created))
	}
	for index, id := range seen {
		expected := created[len(created)-1-index]
		if id != expected {
			t.Errorf("position %d = %q, want %q (newest first)", index, id, expected)
		}
	}
}

func TestListAppliesTheDefaultAndMaximumPageSize(t *testing.T) {
	service, _ := newTestService(t)
	ctx := context.Background()

	page, err := service.List(ctx, ListOptions{})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if page.NextCursor != "" || len(page.Applications) != 0 {
		t.Errorf("List() = %+v, want an empty page", page)
	}

	if _, err := service.List(ctx, ListOptions{Limit: maxPageSize + 1}); err == nil {
		t.Error("List() error = nil, want an oversized limit to be rejected")
	}
	if _, err := service.List(ctx, ListOptions{Limit: -1}); err == nil {
		t.Error("List() error = nil, want a negative limit to be rejected")
	}
	if _, err := service.List(ctx, ListOptions{Cursor: "nonsense"}); err == nil {
		t.Error("List() error = nil, want an invalid cursor to be rejected")
	}
}

// Creating a tenant is security relevant, so it must leave an audit record.
func TestCreateRecordsAnAuditEvent(t *testing.T) {
	service, logs := newTestService(t)

	created, err := service.Create(context.Background(), "Workspace Town")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	var found bool
	for _, line := range strings.Split(strings.TrimSpace(logs.String()), "\n") {
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}
		if entry["event"] == "application.created" && entry["application_id"] == created.ID {
			found = true
		}
	}

	if !found {
		t.Errorf("logs = %q, want an application.created audit event", logs.String())
	}
}

/*
TestListExcludesDeletedApplications proves the deletion semantics the service
will rely on once the lifecycle endpoints exist: a deleted tenant disappears
from the API while its row is retained.
*/
func TestListExcludesDeletedApplications(t *testing.T) {
	service, _ := newTestService(t)
	ctx := context.Background()

	remaining, err := service.Create(ctx, "Active")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	removed, err := service.Create(ctx, "Removed")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	markDeleted(t, service.store.pool, removed.ID)

	page, err := service.List(ctx, ListOptions{})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(page.Applications) != 1 || page.Applications[0].ID != remaining.ID {
		t.Errorf("List() returned %+v, want only %q", page.Applications, remaining.ID)
	}

	if _, err := service.Get(ctx, removed.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get() error = %v, want ErrNotFound", err)
	}
}

func markDeleted(t *testing.T, pool *pgxpool.Pool, id string) {
	t.Helper()

	_, err := pool.Exec(context.Background(),
		`UPDATE applications SET status = $1, updated_at = now() WHERE id = $2`, StatusDeleted, id)
	if err != nil {
		t.Fatalf("mark %s deleted: %v", id, err)
	}
}
