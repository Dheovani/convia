package rooms

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"log/slog"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"convia/internal/applications"
	"convia/internal/config"
	"convia/internal/database"
	"convia/internal/events"
	"convia/internal/users"
)

/*
testDatabaseURLEnvironment points these tests at a PostgreSQL instance.

The tests are skipped when it is unset, so `go test ./...` runs without
infrastructure, and every test works in its own database so runs never share
state.
*/
const testDatabaseURLEnvironment = "CONVIA_TEST_DATABASE_URL"

// fixture is a service under test together with two applications to isolate.
type fixture struct {
	service      *Service
	applications *applications.Service
	users        *users.Service
	pool         *pgxpool.Pool
	databaseURL  string
	first        string
	second       string
	logs         *bytes.Buffer
	published    *recorder
}

func newFixture(t *testing.T) fixture {
	t.Helper()

	maintenanceURL := strings.TrimSpace(os.Getenv(testDatabaseURLEnvironment))
	if maintenanceURL == "" {
		t.Skipf("set %s to run the room integration tests", testDatabaseURLEnvironment)
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
		MaxConnections: 8,
		ConnectTimeout: 10 * time.Second,
		QueryTimeout:   5 * time.Second,
	}, logger)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(pool.Close)

	applicationService := applications.NewService(applications.NewStore(pool), logger)
	userService := users.NewService(users.NewStore(pool), applicationService, logger)
	first := newApplication(t, applicationService, "First Tenant")
	second := newApplication(t, applicationService, "Second Tenant")

	// The service announces into a recorder so that a test can assert what was
	// announced without holding a stream open.
	published := &recorder{}

	logs.Reset()
	return fixture{
		service:      NewService(NewStore(pool), applicationService, userService, published, logger),
		applications: applicationService,
		users:        userService,
		pool:         pool,
		databaseURL:  databaseURL,
		first:        first,
		second:       second,
		logs:         logs,
		published:    published,
	}
}

// recorder keeps every event the domain announced.
type recorder struct {
	mutex  sync.Mutex
	events []events.Event
}

func (record *recorder) Publish(_ context.Context, event events.Event) {
	record.mutex.Lock()
	defer record.mutex.Unlock()
	record.events = append(record.events, event)
}

func (record *recorder) all() []events.Event {
	record.mutex.Lock()
	defer record.mutex.Unlock()
	return append([]events.Event(nil), record.events...)
}

func newApplication(t *testing.T, service *applications.Service, name string) string {
	t.Helper()

	application, err := service.Create(context.Background(), name)
	if err != nil {
		t.Fatalf("create the %q application: %v", name, err)
	}
	return application.ID
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

// created makes one room of the first application for a test.
func created(t *testing.T, setup fixture, definition Definition) Room {
	t.Helper()

	if definition.Name == "" {
		definition.Name = "Weekly Standup"
	}

	room, err := setup.service.Create(context.Background(), setup.first, definition)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	return room
}

/*
TestCreatedRoomRoundTrips proves the domain survives PostgreSQL: everything a
caller supplied comes back, and everything Convia owns is assigned.
*/
func TestCreatedRoomRoundTrips(t *testing.T) {
	setup := newFixture(t)
	limit := 25

	room := created(t, setup, Definition{
		Alias:           "weekly-standup",
		Name:            "Weekly Standup",
		Metadata:        map[string]string{"team": "platform"},
		MaxParticipants: &limit,
	})

	stored, err := setup.service.Get(context.Background(), setup.first, room.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}

	if stored.Alias != "weekly-standup" {
		t.Errorf("alias = %q, want %q", stored.Alias, "weekly-standup")
	}
	if stored.Name != "Weekly Standup" {
		t.Errorf("name = %q, want %q", stored.Name, "Weekly Standup")
	}
	if stored.Metadata["team"] != "platform" {
		t.Errorf("metadata = %v, want the team entry", stored.Metadata)
	}
	if stored.MaxParticipants == nil || *stored.MaxParticipants != limit {
		t.Errorf("max_participants = %v, want %d", stored.MaxParticipants, limit)
	}
	if stored.Status != StatusOpen {
		t.Errorf("status = %q, want %q", stored.Status, StatusOpen)
	}
	if !ValidID(stored.ID) {
		t.Errorf("id = %q, which is not a room identifier", stored.ID)
	}
	if stored.ApplicationID != setup.first {
		t.Errorf("application = %q, want %q", stored.ApplicationID, setup.first)
	}
}

/*
TestAnAnonymousRoomNeedsNoAlias proves the ephemeral case.

More importantly it proves several of them can coexist: an alias stored as an
empty string rather than NULL would collide in the unique index, and one
application would be able to have exactly one anonymous room.
*/
func TestAnAnonymousRoomNeedsNoAlias(t *testing.T) {
	setup := newFixture(t)

	first := created(t, setup, Definition{Name: "Ad hoc"})
	second := created(t, setup, Definition{Name: "Another ad hoc"})

	if first.Alias != "" || second.Alias != "" {
		t.Fatalf("aliases = %q and %q, want both empty", first.Alias, second.Alias)
	}
	if first.ID == second.ID {
		t.Fatal("the two anonymous rooms are the same room")
	}
}

/*
TestAnAliasIsTakenOnceAndStaysTaken proves creating and looking up stay
different questions, and that deletion does not free a name.

A cached alias must never come to point at a different room.
*/
func TestAnAliasIsTakenOnceAndStaysTaken(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	room := created(t, setup, Definition{Alias: "standup", Name: "Standup"})

	_, err := setup.service.Create(ctx, setup.first, Definition{Alias: "standup", Name: "Impostor"})
	if !errors.Is(err, ErrAliasTaken) {
		t.Fatalf("Create() with a taken alias error = %v, want %v", err, ErrAliasTaken)
	}

	if err := setup.service.Delete(ctx, setup.first, room.ID); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}

	_, err = setup.service.Create(ctx, setup.first, Definition{Alias: "standup", Name: "Successor"})
	if !errors.Is(err, ErrAliasTaken) {
		t.Fatalf("Create() after deletion error = %v, want the alias to stay reserved (%v)", err, ErrAliasTaken)
	}
}

/*
TestRoomsAreIsolatedPerApplication is the tenancy guarantee.

No operation may reach another application's room, and the two may use the same
alias without any relationship between the resulting rooms.
*/
func TestRoomsAreIsolatedPerApplication(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	mine := created(t, setup, Definition{Alias: "shared-name", Name: "Mine"})

	// The same alias in another application is a different room entirely.
	theirs, err := setup.service.Create(ctx, setup.second, Definition{Alias: "shared-name", Name: "Theirs"})
	if err != nil {
		t.Fatalf("Create() in the second application error = %v", err)
	}
	if theirs.ID == mine.ID {
		t.Fatal("the two applications share one room")
	}

	name := "Renamed"
	crossings := map[string]func() error{
		"get": func() error {
			_, err := setup.service.Get(ctx, setup.second, mine.ID)
			return err
		},
		"update": func() error {
			_, err := setup.service.Update(ctx, setup.second, mine.ID, Change{Name: &name}, "")
			return err
		},
		"close": func() error {
			_, err := setup.service.Close(ctx, setup.second, mine.ID)
			return err
		},
		"reopen": func() error {
			_, err := setup.service.Reopen(ctx, setup.second, mine.ID)
			return err
		},
		"delete": func() error {
			return setup.service.Delete(ctx, setup.second, mine.ID)
		},
	}

	for name, cross := range crossings {
		t.Run(name, func(t *testing.T) {
			if err := cross(); !errors.Is(err, ErrNotFound) {
				t.Errorf("%s across tenants error = %v, want %v", name, err, ErrNotFound)
			}
		})
	}

	// The room is untouched by every attempt above.
	stored, err := setup.service.Get(ctx, setup.first, mine.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if stored.Name != "Mine" || stored.Status != StatusOpen {
		t.Errorf("room = %q/%q, want it unchanged", stored.Name, stored.Status)
	}
}

// A listing must never contain another application's rooms.
func TestListIsScopedToOneApplication(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	mine := created(t, setup, Definition{Name: "Mine"})
	if _, err := setup.service.Create(ctx, setup.second, Definition{Name: "Theirs"}); err != nil {
		t.Fatalf("Create() in the second application error = %v", err)
	}

	page, err := setup.service.List(ctx, setup.first, ListOptions{})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(page.Rooms) != 1 || page.Rooms[0].ID != mine.ID {
		t.Fatalf("listing returned %d rooms, want only the first application's", len(page.Rooms))
	}
}

/*
TestGetByAliasFindsTheRoomItsApplicationNamed proves the lookup that makes a
durable room usable without a Convia identifier, and that it is tenant-scoped.
*/
func TestGetByAliasFindsTheRoomItsApplicationNamed(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	room := created(t, setup, Definition{Alias: "support-queue", Name: "Support"})

	found, err := setup.service.GetByAlias(ctx, setup.first, "support-queue")
	if err != nil {
		t.Fatalf("GetByAlias() error = %v", err)
	}
	if found.ID != room.ID {
		t.Errorf("found %q, want %q", found.ID, room.ID)
	}

	if _, err := setup.service.GetByAlias(ctx, setup.second, "support-queue"); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetByAlias() across tenants error = %v, want %v", err, ErrNotFound)
	}
}

/*
TestUpdateChangesOnlyWhatWasSent proves an update that mentions one attribute
cannot silently reset another.
*/
func TestUpdateChangesOnlyWhatWasSent(t *testing.T) {
	setup := newFixture(t)
	limit := 10

	room := created(t, setup, Definition{
		Alias:           "standup",
		Name:            "Standup",
		Metadata:        map[string]string{"team": "platform"},
		MaxParticipants: &limit,
	})

	name := "Standup (EMEA)"
	updated, err := setup.service.Update(context.Background(), setup.first, room.ID,
		Change{Name: &name}, "")
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	if updated.Name != name {
		t.Errorf("name = %q, want %q", updated.Name, name)
	}
	if updated.Alias != "standup" {
		t.Errorf("alias = %q, want it untouched", updated.Alias)
	}
	if updated.Metadata["team"] != "platform" {
		t.Errorf("metadata = %v, want it untouched", updated.Metadata)
	}
	if updated.MaxParticipants == nil || *updated.MaxParticipants != limit {
		t.Errorf("max_participants = %v, want it untouched", updated.MaxParticipants)
	}
}

// Sending a value empty clears it, which is how a room becomes anonymous or loses its cap.
func TestUpdateClearsWhatWasSentEmpty(t *testing.T) {
	setup := newFixture(t)
	limit := 10

	room := created(t, setup, Definition{Alias: "standup", Name: "Standup", MaxParticipants: &limit})

	empty := ""
	var noLimit *int
	updated, err := setup.service.Update(context.Background(), setup.first, room.ID,
		Change{Alias: &empty, MaxParticipants: &noLimit}, "")
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	if updated.Alias != "" {
		t.Errorf("alias = %q, want it cleared", updated.Alias)
	}
	if updated.MaxParticipants != nil {
		t.Errorf("max_participants = %v, want it cleared", *updated.MaxParticipants)
	}

	// Clearing the alias frees it for another room, because nothing holds it now.
	if _, err := setup.service.Create(context.Background(), setup.first,
		Definition{Alias: "standup", Name: "Successor"}); err != nil {
		t.Errorf("Create() after the alias was cleared error = %v, want it to be free", err)
	}
}

// An update that renames onto a taken alias is a conflict, not a silent no-op.
func TestUpdateRefusesATakenAlias(t *testing.T) {
	setup := newFixture(t)

	created(t, setup, Definition{Alias: "taken", Name: "First"})
	second := created(t, setup, Definition{Alias: "free", Name: "Second"})

	alias := "taken"
	_, err := setup.service.Update(context.Background(), setup.first, second.ID,
		Change{Alias: &alias}, "")
	if !errors.Is(err, ErrAliasTaken) {
		t.Fatalf("Update() error = %v, want %v", err, ErrAliasTaken)
	}
}

// TestUpdateHonorsTheEntityTag proves optimistic concurrency reaches storage.
func TestUpdateHonorsTheEntityTag(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()
	room := created(t, setup, Definition{Name: "Standup"})

	first := "First"
	updated, err := setup.service.Update(ctx, setup.first, room.ID, Change{Name: &first}, room.Version())
	if err != nil {
		t.Fatalf("Update() with the current version error = %v", err)
	}

	// The version the caller started with is now stale.
	second := "Second"
	_, err = setup.service.Update(ctx, setup.first, room.ID, Change{Name: &second}, room.Version())
	if !errors.Is(err, ErrPreconditionFailed) {
		t.Fatalf("Update() with a stale version error = %v, want %v", err, ErrPreconditionFailed)
	}

	stored, err := setup.service.Get(ctx, setup.first, room.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if stored.Name != updated.Name {
		t.Errorf("name = %q, want the refused update not to have applied", stored.Name)
	}
}

/*
TestCloseAndReopenAreReversibleAndRepeatable proves closing loses nothing and
that a client retrying after a timeout is never punished for it.
*/
func TestCloseAndReopenAreReversibleAndRepeatable(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()
	room := created(t, setup, Definition{Alias: "standup", Name: "Standup"})

	for attempt := range 2 {
		closed, err := setup.service.Close(ctx, setup.first, room.ID)
		if err != nil {
			t.Fatalf("Close() attempt %d error = %v", attempt+1, err)
		}
		if closed.Status != StatusClosed {
			t.Errorf("status = %q, want %q", closed.Status, StatusClosed)
		}
		if closed.Alias != "standup" || closed.Name != "Standup" {
			t.Error("closing lost an attribute, but it is meant to be lossless")
		}
	}

	for attempt := range 2 {
		reopened, err := setup.service.Reopen(ctx, setup.first, room.ID)
		if err != nil {
			t.Fatalf("Reopen() attempt %d error = %v", attempt+1, err)
		}
		if reopened.Status != StatusOpen {
			t.Errorf("status = %q, want %q", reopened.Status, StatusOpen)
		}
	}
}

/*
TestDeletionRemovesTheRoomFromTheAPI proves deletion is repeatable and that a
deleted room refuses further change rather than being revived.
*/
func TestDeletionRemovesTheRoomFromTheAPI(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()
	room := created(t, setup, Definition{Name: "Standup"})

	for attempt := range 2 {
		if err := setup.service.Delete(ctx, setup.first, room.ID); err != nil {
			t.Fatalf("Delete() attempt %d error = %v", attempt+1, err)
		}
	}

	stored, err := setup.service.Get(ctx, setup.first, room.ID)
	if err != nil {
		t.Fatalf("Get() after deletion error = %v", err)
	}
	if stored.Status != StatusDeleted {
		t.Errorf("status = %q, want %q", stored.Status, StatusDeleted)
	}

	name := "Revived"
	if _, err := setup.service.Update(ctx, setup.first, room.ID, Change{Name: &name}, ""); !errors.Is(err, ErrDeleted) {
		t.Errorf("Update() on a deleted room error = %v, want %v", err, ErrDeleted)
	}
	if _, err := setup.service.Close(ctx, setup.first, room.ID); !errors.Is(err, ErrDeleted) {
		t.Errorf("Close() on a deleted room error = %v, want %v", err, ErrDeleted)
	}
}

/*
TestListPagesNewestFirstAndFilters proves the pagination bounds in
docs/api-conventions.md and the one filter the API offers.
*/
func TestListPagesNewestFirstAndFilters(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	var ids []string
	for _, name := range []string{"first", "second", "third"} {
		ids = append(ids, created(t, setup, Definition{Name: name}).ID)
	}
	if _, err := setup.service.Close(ctx, setup.first, ids[0]); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	page, err := setup.service.List(ctx, setup.first, ListOptions{Limit: 2})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(page.Rooms) != 2 {
		t.Fatalf("page size = %d, want 2", len(page.Rooms))
	}
	if page.NextCursor == "" {
		t.Fatal("no continuation cursor on a page that is not the last")
	}
	if page.Rooms[0].ID != ids[2] {
		t.Errorf("first result = %q, want the newest (%q)", page.Rooms[0].ID, ids[2])
	}

	next, err := setup.service.List(ctx, setup.first, ListOptions{Limit: 2, Cursor: page.NextCursor})
	if err != nil {
		t.Fatalf("List(cursor) error = %v", err)
	}
	if len(next.Rooms) != 1 || next.Rooms[0].ID != ids[0] {
		t.Fatalf("second page = %d rooms, want the oldest one", len(next.Rooms))
	}

	closed, err := setup.service.List(ctx, setup.first, ListOptions{Status: string(StatusClosed)})
	if err != nil {
		t.Fatalf("List(status=closed) error = %v", err)
	}
	if len(closed.Rooms) != 1 || closed.Rooms[0].ID != ids[0] {
		t.Errorf("closed listing returned %d rooms, want only the closed one", len(closed.Rooms))
	}
}

// A deleted room is excluded from a routine listing but reachable by asking.
func TestListExcludesDeletedRoomsUnlessAsked(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	kept := created(t, setup, Definition{Name: "Kept"})
	removed := created(t, setup, Definition{Name: "Removed"})
	if err := setup.service.Delete(ctx, setup.first, removed.ID); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}

	page, err := setup.service.List(ctx, setup.first, ListOptions{})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(page.Rooms) != 1 || page.Rooms[0].ID != kept.ID {
		t.Fatalf("listing returned %d rooms, want only the one still in use", len(page.Rooms))
	}

	deleted, err := setup.service.List(ctx, setup.first, ListOptions{Status: string(StatusDeleted)})
	if err != nil {
		t.Fatalf("List(status=deleted) error = %v", err)
	}
	if len(deleted.Rooms) != 1 || deleted.Rooms[0].ID != removed.ID {
		t.Errorf("deleted listing returned %d rooms, want the removed one", len(deleted.Rooms))
	}
}

// Operations must refuse an application Convia is no longer serving.
func TestOperationsRequireAServedApplication(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()
	room := created(t, setup, Definition{Name: "Standup"})

	if _, err := setup.applications.Suspend(ctx, setup.first); err != nil {
		t.Fatalf("Suspend() error = %v", err)
	}

	if _, err := setup.service.Get(ctx, setup.first, room.ID); !errors.Is(err, ErrApplicationNotFound) {
		t.Errorf("Get() for a suspended tenant error = %v, want %v", err, ErrApplicationNotFound)
	}
	if _, err := setup.service.Create(ctx, setup.first, Definition{Name: "New"}); !errors.Is(err, ErrApplicationNotFound) {
		t.Errorf("Create() for a suspended tenant error = %v, want %v", err, ErrApplicationNotFound)
	}
}

/*
TestAuditRecordsLifecycleWithoutApplicationLabels proves the audit trail
identifies what changed without carrying names people chose.

An alias and a name are application-chosen labels that may say something about
the people using them, so neither belongs in an operator's log.
*/
func TestAuditRecordsLifecycleWithoutApplicationLabels(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	room := created(t, setup, Definition{Alias: "private-therapy-group", Name: "Tuesday Group"})
	if _, err := setup.service.Close(ctx, setup.first, room.ID); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := setup.service.Delete(ctx, setup.first, room.ID); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}

	logged := setup.logs.String()
	for _, event := range []string{"room.created", "room.closed", "room.deleted"} {
		if !strings.Contains(logged, event) {
			t.Errorf("the audit log is missing the %q event", event)
		}
	}
	if !strings.Contains(logged, room.ID) {
		t.Error("the audit log does not identify the room at all")
	}
	if strings.Contains(logged, "private-therapy-group") {
		t.Error("the audit log carries the alias, which an application chose")
	}
	if strings.Contains(logged, "Tuesday Group") {
		t.Error("the audit log carries the name, which an application chose")
	}
}
