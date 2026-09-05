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
	"slices"
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

func TestRenameChangesTheNameAndVersion(t *testing.T) {
	service, _ := newTestService(t)
	ctx := context.Background()

	created, err := service.Create(ctx, "Workspace Town")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	renamed, err := service.Rename(ctx, created.ID, "  Workspace Village  ", "")
	if err != nil {
		t.Fatalf("Rename() error = %v", err)
	}

	if renamed.Name != "Workspace Village" {
		t.Errorf("Name = %q, want the trimmed new name", renamed.Name)
	}
	if renamed.ID != created.ID {
		t.Errorf("ID = %q, want it unchanged at %q", renamed.ID, created.ID)
	}
	if !renamed.CreatedAt.Equal(created.CreatedAt) {
		t.Errorf("CreatedAt = %s, want it unchanged", renamed.CreatedAt)
	}
	if !renamed.UpdatedAt.After(created.UpdatedAt) {
		t.Errorf("UpdatedAt = %s, want it after %s", renamed.UpdatedAt, created.UpdatedAt)
	}
	if renamed.Version() == created.Version() {
		t.Error("the version did not change with the rename")
	}
}

/*
TestRenameRefusesAStaleVersion is the point of optimistic concurrency: a client
that read an application, then lost the race, must not silently overwrite the
change it never saw.
*/
func TestRenameRefusesAStaleVersion(t *testing.T) {
	service, _ := newTestService(t)
	ctx := context.Background()

	created, err := service.Create(ctx, "Workspace Town")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	stale := created.Version()

	if _, err := service.Rename(ctx, created.ID, "Renamed By Someone Else", stale); err != nil {
		t.Fatalf("Rename() with a current version error = %v", err)
	}

	_, err = service.Rename(ctx, created.ID, "Renamed Too Late", stale)
	if !errors.Is(err, ErrPreconditionFailed) {
		t.Fatalf("Rename() error = %v, want ErrPreconditionFailed", err)
	}

	current, err := service.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if current.Name != "Renamed By Someone Else" {
		t.Errorf("Name = %q, want the refused rename to have changed nothing", current.Name)
	}
}

func TestRenameRejectsInvalidNames(t *testing.T) {
	service, _ := newTestService(t)
	ctx := context.Background()

	created, err := service.Create(ctx, "Workspace Town")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	if _, err := service.Rename(ctx, created.ID, "  ", ""); err == nil {
		t.Error("Rename() error = nil, want a validation error")
	}
}

/*
TestSuspendAndActivateAreRepeatable walks the lifecycle and proves that
repeating a transition is safe, which is what lets a client retry after a
timeout without checking first.
*/
func TestSuspendAndActivateAreRepeatable(t *testing.T) {
	service, _ := newTestService(t)
	ctx := context.Background()

	created, err := service.Create(ctx, "Workspace Town")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	suspended, err := service.Suspend(ctx, created.ID)
	if err != nil {
		t.Fatalf("Suspend() error = %v", err)
	}
	if suspended.Status != StatusSuspended {
		t.Errorf("Status = %q, want %q", suspended.Status, StatusSuspended)
	}

	again, err := service.Suspend(ctx, created.ID)
	if err != nil {
		t.Fatalf("Suspend() repeated error = %v", err)
	}
	if again.Version() != suspended.Version() {
		t.Error("repeating a suspension changed the application")
	}

	activated, err := service.Activate(ctx, created.ID)
	if err != nil {
		t.Fatalf("Activate() error = %v", err)
	}
	if activated.Status != StatusActive {
		t.Errorf("Status = %q, want %q", activated.Status, StatusActive)
	}
}

/*
TestDeleteRemovesFromTheAPIAndIsRepeatable covers the deletion semantics: the
application leaves the API surface, its row is retained, and a repeated delete
succeeds.
*/
func TestDeleteRemovesFromTheAPIAndIsRepeatable(t *testing.T) {
	service, logs := newTestService(t)
	ctx := context.Background()

	created, err := service.Create(ctx, "Workspace Town")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	if err := service.Delete(ctx, created.ID); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if err := service.Delete(ctx, created.ID); err != nil {
		t.Fatalf("Delete() repeated error = %v", err)
	}

	if _, err := service.Get(ctx, created.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get() error = %v, want ErrNotFound", err)
	}

	// A repeated deletion must not appear in the audit trail as a second deletion.
	deletions := 0
	for _, event := range auditEvents(t, logs, created.ID) {
		if event == "application.deleted" {
			deletions++
		}
	}
	if deletions != 1 {
		t.Errorf("audit recorded %d deletions, want exactly 1", deletions)
	}

	var status string
	err = service.store.pool.QueryRow(ctx, `SELECT status FROM applications WHERE id = $1`, created.ID).Scan(&status)
	if err != nil {
		t.Fatalf("read the retained row: %v", err)
	}
	if status != string(StatusDeleted) {
		t.Errorf("stored status = %q, want %q", status, StatusDeleted)
	}
}

func TestDeleteReportsUnknownApplications(t *testing.T) {
	service, _ := newTestService(t)

	if err := service.Delete(context.Background(), NewID()); !errors.Is(err, ErrNotFound) {
		t.Errorf("Delete() error = %v, want ErrNotFound", err)
	}
	if err := service.Delete(context.Background(), "not-an-identifier"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Delete() error = %v, want ErrNotFound", err)
	}
}

// A deleted application has left the API, so it can no longer be changed.
func TestDeletedApplicationsCannotBeChanged(t *testing.T) {
	service, _ := newTestService(t)
	ctx := context.Background()

	created, err := service.Create(ctx, "Workspace Town")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if err := service.Delete(ctx, created.ID); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}

	if _, err := service.Rename(ctx, created.ID, "Revived", ""); !errors.Is(err, ErrNotFound) {
		t.Errorf("Rename() error = %v, want ErrNotFound", err)
	}
	if _, err := service.Activate(ctx, created.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("Activate() error = %v, want ErrNotFound", err)
	}
	if _, err := service.Suspend(ctx, created.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("Suspend() error = %v, want ErrNotFound", err)
	}
}

// Every lifecycle change is security relevant, so each leaves an audit record.
func TestLifecycleChangesRecordAuditEvents(t *testing.T) {
	service, logs := newTestService(t)
	ctx := context.Background()

	created, err := service.Create(ctx, "Workspace Town")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if _, err := service.Rename(ctx, created.ID, "Workspace Village", ""); err != nil {
		t.Fatalf("Rename() error = %v", err)
	}
	if _, err := service.Suspend(ctx, created.ID); err != nil {
		t.Fatalf("Suspend() error = %v", err)
	}
	if _, err := service.Activate(ctx, created.ID); err != nil {
		t.Fatalf("Activate() error = %v", err)
	}
	if err := service.Delete(ctx, created.ID); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}

	recorded := auditEvents(t, logs, created.ID)
	for _, event := range []string{
		"application.created",
		"application.renamed",
		"application.suspended",
		"application.activated",
		"application.deleted",
	} {
		if !slices.Contains(recorded, event) {
			t.Errorf("audit events = %v, want them to include %q", recorded, event)
		}
	}
}

// auditEvents collects the audit event names logged for one application.
func auditEvents(t *testing.T, logs *bytes.Buffer, id string) []string {
	t.Helper()

	var events []string
	for _, line := range strings.Split(strings.TrimSpace(logs.String()), "\n") {
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}
		if entry["application_id"] != id {
			continue
		}
		if event, isString := entry["event"].(string); isString {
			events = append(events, event)
		}
	}
	return events
}

func markDeleted(t *testing.T, pool *pgxpool.Pool, id string) {
	t.Helper()

	_, err := pool.Exec(context.Background(),
		`UPDATE applications SET status = $1, updated_at = now() WHERE id = $2`, StatusDeleted, id)
	if err != nil {
		t.Fatalf("mark %s deleted: %v", id, err)
	}
}
