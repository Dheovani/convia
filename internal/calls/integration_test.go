package calls

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
	"convia/internal/media"
	"convia/internal/rooms"
)

/*
testDatabaseURLEnvironment points these tests at a PostgreSQL instance.

The tests are skipped when it is unset, so `go test ./...` runs without
infrastructure, and every test works in its own database so runs never share
state.
*/
const testDatabaseURLEnvironment = "CONVIA_TEST_DATABASE_URL"

// fixture is a service under test together with two tenants to isolate.
type fixture struct {
	service      *Service
	pool         *pgxpool.Pool
	rooms        *rooms.Service
	applications *applications.Service
	broker       *events.Broker
	first        string
	second       string
	firstRoom    string
	secondRoom   string
	logs         *bytes.Buffer
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	return newFixtureWith(t, media.Absent{})
}

/*
newFixtureWith builds the same fixture against a chosen media plane.

Most tests do not care and use the absent one, which is what Convia ships with.
The tests that do care are about what happens when the media plane fails, and
they are the reason the boundary exists.
*/
func newFixtureWith(t *testing.T, plane MediaPlane) fixture {
	t.Helper()

	maintenanceURL := strings.TrimSpace(os.Getenv(testDatabaseURLEnvironment))
	if maintenanceURL == "" {
		t.Skipf("set %s to run the call integration tests", testDatabaseURLEnvironment)
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
	roomService := rooms.NewService(rooms.NewStore(pool), applicationService, logger)

	first := newApplication(t, applicationService, "First Tenant")
	second := newApplication(t, applicationService, "Second Tenant")

	broker := events.NewBroker()
	// No durable sink: these tests are about what the domain announces,
	// not about where it is later delivered.
	announcer := events.NewAnnouncer(broker, nil, logger)

	setup := fixture{
		service:      NewService(NewStore(pool), applicationService, roomService, plane, announcer, logger),
		pool:         pool,
		rooms:        roomService,
		applications: applicationService,
		broker:       broker,
		first:        first,
		second:       second,
		logs:         logs,
	}
	setup.firstRoom = newRoom(t, roomService, first, "standup")
	setup.secondRoom = newRoom(t, roomService, second, "standup")

	logs.Reset()
	return setup
}

func newApplication(t *testing.T, service *applications.Service, name string) string {
	t.Helper()

	application, err := service.Create(context.Background(), name)
	if err != nil {
		t.Fatalf("create the %q application: %v", name, err)
	}
	return application.ID
}

func newRoom(t *testing.T, service *rooms.Service, applicationID, alias string) string {
	t.Helper()

	room, err := service.Create(context.Background(), applicationID,
		rooms.Definition{Alias: alias, Name: "Standup"})
	if err != nil {
		t.Fatalf("create a room for %s: %v", applicationID, err)
	}
	return room.ID
}

func execute(t *testing.T, databaseURL, statement string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect to run %q: %v", statement, err)
	}
	defer pool.Close()

	if _, err := pool.Exec(ctx, statement); err != nil {
		t.Fatalf("run %q: %v", statement, err)
	}
}

// start begins a call and fails the test if it could not.
func (setup fixture) start(t *testing.T, applicationID, roomID string) Call {
	t.Helper()

	call, err := setup.service.Start(context.Background(), applicationID, roomID,
		Definition{}, ActorApplication)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	return call
}

/*
TestARoomHoldsOneCallAtATime is the rule the whole domain is built around.

A second conversation in the same place would mean two groups of people
talking past each other, so it is refused. It is refused rather than answered
with the running call, because a caller that received the current one could not
tell whether it had just started something.
*/
func TestARoomHoldsOneCallAtATime(t *testing.T) {
	setup := newFixture(t)
	setup.start(t, setup.first, setup.firstRoom)

	_, err := setup.service.Start(context.Background(), setup.first, setup.firstRoom,
		Definition{}, ActorApplication)
	if !errors.Is(err, ErrCallInProgress) {
		t.Fatalf("Start() error = %v, want %v", err, ErrCallInProgress)
	}
}

/*
TestOnlyOneOfManySimultaneousStartsWins is the race the index exists for.

Two requests arriving together must not both start a call, and no
application-level check could prevent it: it would read, decide, and lose the
race in between. The partial unique index makes PostgreSQL settle it.
*/
func TestOnlyOneOfManySimultaneousStartsWins(t *testing.T) {
	setup := newFixture(t)

	const racers = 8
	var (
		start    sync.WaitGroup
		finished sync.WaitGroup
		mutex    sync.Mutex
	)
	started, refused := 0, 0

	start.Add(1)
	for range racers {
		finished.Add(1)
		go func() {
			defer finished.Done()
			start.Wait()

			_, err := setup.service.Start(context.Background(), setup.first, setup.firstRoom,
				Definition{}, ActorApplication)

			mutex.Lock()
			defer mutex.Unlock()
			switch {
			case err == nil:
				started++
			case errors.Is(err, ErrCallInProgress):
				refused++
			default:
				t.Errorf("Start() error = %v", err)
			}
		}()
	}

	start.Done()
	finished.Wait()

	if started != 1 {
		t.Errorf("%d of %d simultaneous starts succeeded, want exactly one", started, racers)
	}
	if started+refused != racers {
		t.Errorf("%d starts were accounted for, want %d", started+refused, racers)
	}

	page, err := setup.service.List(context.Background(), setup.first, ListOptions{RoomID: setup.firstRoom})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(page.Calls) != 1 {
		t.Errorf("the room holds %d calls, want the losers to have created none", len(page.Calls))
	}
}

/*
TestEndingFreesTheRoomForTheNextCall proves the constraint bounds the present.

Only an active call occupies a room. A room that has hosted a hundred
conversations can host another, which is what makes it durable rather than
single-use.
*/
func TestEndingFreesTheRoomForTheNextCall(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	first := setup.start(t, setup.first, setup.firstRoom)

	if _, err := setup.service.End(ctx, setup.first, first.ID, ActorApplication, ""); err != nil {
		t.Fatalf("End() error = %v", err)
	}

	second := setup.start(t, setup.first, setup.firstRoom)
	if second.ID == first.ID {
		t.Fatal("starting again returned the call that had already ended")
	}

	page, err := setup.service.List(ctx, setup.first, ListOptions{RoomID: setup.firstRoom})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(page.Calls) != 2 {
		t.Errorf("the room's history holds %d calls, want both", len(page.Calls))
	}
}

/*
TestEndingRecordsWhoAndWhy is what makes the history worth keeping.

A record that said only "over" would leave an operator reconstructing events
from timestamps. Who ended it comes from the verified authority; why comes from
the caller and is optional.
*/
func TestEndingRecordsWhoAndWhy(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	call := setup.start(t, setup.first, setup.firstRoom)
	if call.EndedAt != nil || call.EndedBy != nil {
		t.Fatal("a call that just started already carried an ending")
	}

	ended, err := setup.service.End(ctx, setup.first, call.ID, ActorOperator, "  stopped during an incident  ")
	if err != nil {
		t.Fatalf("End() error = %v", err)
	}

	switch {
	case ended.Status != StatusEnded:
		t.Errorf("status = %q, want %q", ended.Status, StatusEnded)
	case ended.EndedAt == nil:
		t.Error("the call ended without recording when")
	case ended.EndedBy == nil || *ended.EndedBy != ActorOperator:
		t.Errorf("ended_by = %v, want %q", ended.EndedBy, ActorOperator)
	case ended.EndReason != "stopped during an incident":
		t.Errorf("reason = %q, want it trimmed and stored", ended.EndReason)
	}

	if ended.EndedAt != nil && ended.EndedAt.Before(ended.CreatedAt) {
		t.Error("the call ended before it began")
	}
}

/*
TestEndingIsRepeatable keeps a retry from being punished.

The second request finds nothing to change and returns the call as it stands.
It must not overwrite the first answer: who ended a conversation is the one who
actually ended it, not whoever asked again afterwards.
*/
func TestEndingIsRepeatable(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	call := setup.start(t, setup.first, setup.firstRoom)

	first, err := setup.service.End(ctx, setup.first, call.ID, ActorApplication, "the meeting finished")
	if err != nil {
		t.Fatalf("End() error = %v", err)
	}

	second, err := setup.service.End(ctx, setup.first, call.ID, ActorOperator, "a different story")
	if err != nil {
		t.Fatalf("End() on the repeat error = %v", err)
	}

	switch {
	case second.Status != StatusEnded:
		t.Errorf("status = %q, want the call to stay %q", second.Status, StatusEnded)
	case second.EndReason != first.EndReason:
		t.Errorf("reason = %q, want the original %q", second.EndReason, first.EndReason)
	case second.EndedBy == nil || *second.EndedBy != ActorApplication:
		t.Errorf("ended_by = %v, want the actor that actually ended it", second.EndedBy)
	case !second.EndedAt.Equal(*first.EndedAt):
		t.Error("the repeat moved the moment the call ended")
	}
}

/*
TestAClosedRoomRefusesANewCall implements the decision M08 recorded.

Closing a room stops new conversations. That is all it does, which is why the
call already in progress is left alone: ending one because an administrator
tidied a listing would be the wrong default.
*/
func TestAClosedRoomRefusesANewCall(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	running := setup.start(t, setup.first, setup.firstRoom)

	if _, err := setup.rooms.Close(ctx, setup.first, setup.firstRoom); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	survived, err := setup.service.Get(ctx, setup.first, running.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if survived.Status != StatusActive {
		t.Errorf("the conversation in progress was %q after the room closed, want %q",
			survived.Status, StatusActive)
	}

	if _, err := setup.service.End(ctx, setup.first, running.ID, ActorApplication, ""); err != nil {
		t.Fatalf("End() error = %v, want a call in a closed room to still be endable", err)
	}

	_, err = setup.service.Start(ctx, setup.first, setup.firstRoom, Definition{}, ActorApplication)
	if !errors.Is(err, ErrRoomClosed) {
		t.Fatalf("Start() error = %v, want %v", err, ErrRoomClosed)
	}

	if _, err := setup.rooms.Reopen(ctx, setup.first, setup.firstRoom); err != nil {
		t.Fatalf("Reopen() error = %v", err)
	}
	setup.start(t, setup.first, setup.firstRoom)
}

/*
TestADeletedRoomIsMissingRatherThanClosed keeps the two answers apart.

Closed is a state the application chose and can undo; deleted is gone from the
API. Reporting a deleted room as closed would invite a caller to reopen
something that is not there.
*/
func TestADeletedRoomIsMissingRatherThanClosed(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	if err := setup.rooms.Delete(ctx, setup.first, setup.firstRoom); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}

	_, err := setup.service.Start(ctx, setup.first, setup.firstRoom, Definition{}, ActorApplication)
	if !errors.Is(err, ErrRoomNotFound) {
		t.Fatalf("Start() error = %v, want %v", err, ErrRoomNotFound)
	}
}

/*
TestAnUnknownRoomIsRefusedBeforeAnythingIsWritten proves the room is resolved
first, so a start never leaves a call attached to a room that does not exist.
*/
func TestAnUnknownRoomIsRefusedBeforeAnythingIsWritten(t *testing.T) {
	setup := newFixture(t)

	_, err := setup.service.Start(context.Background(), setup.first,
		"room_AAAAAAAAAAAAAAAAAAAAAAAAAA", Definition{}, ActorApplication)
	if !errors.Is(err, ErrRoomNotFound) {
		t.Fatalf("Start() error = %v, want %v", err, ErrRoomNotFound)
	}
}

/*
TestOneApplicationCannotReachAnothersCall is the isolation guarantee.

Crossing answers "not found" rather than "forbidden", because forbidden would
confirm the call exists. The same is true of the room-scoped history: a room
that is not the caller's is missing, not empty.
*/
func TestOneApplicationCannotReachAnothersCall(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	call := setup.start(t, setup.first, setup.firstRoom)

	if _, err := setup.service.Get(ctx, setup.second, call.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get() across tenants error = %v, want %v", err, ErrNotFound)
	}
	if _, err := setup.service.End(ctx, setup.second, call.ID, ActorApplication, ""); !errors.Is(err, ErrNotFound) {
		t.Errorf("End() across tenants error = %v, want %v", err, ErrNotFound)
	}
	if _, err := setup.service.List(ctx, setup.second, ListOptions{RoomID: setup.firstRoom}); !errors.Is(err, ErrRoomNotFound) {
		t.Errorf("List() across tenants error = %v, want %v", err, ErrRoomNotFound)
	}

	page, err := setup.service.List(ctx, setup.second, ListOptions{})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(page.Calls) != 0 {
		t.Errorf("the second tenant sees %d calls, want none of the first tenant's", len(page.Calls))
	}

	// The call the other tenant could not touch is untouched.
	intact, err := setup.service.Get(ctx, setup.first, call.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if intact.Status != StatusActive {
		t.Errorf("status = %q, want the call to be unaffected by the other tenant", intact.Status)
	}
}

/*
TestTwoTenantsCanHoldCallsInTheirOwnRoomsAtOnce proves the one-call rule is
scoped to a room rather than to Convia.
*/
func TestTwoTenantsCanHoldCallsInTheirOwnRoomsAtOnce(t *testing.T) {
	setup := newFixture(t)

	first := setup.start(t, setup.first, setup.firstRoom)
	second := setup.start(t, setup.second, setup.secondRoom)

	if first.ID == second.ID {
		t.Fatal("two tenants received the same call")
	}
	if first.ApplicationID == second.ApplicationID {
		t.Error("a call was attributed to the wrong tenant")
	}
}

/*
TestTheHistoryPagesNewestFirst proves the listing is a history rather than a
snapshot: ended calls are returned, and the newest comes first.
*/
func TestTheHistoryPagesNewestFirst(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	var ordered []string
	for range 5 {
		call := setup.start(t, setup.first, setup.firstRoom)
		ordered = append(ordered, call.ID)

		if _, err := setup.service.End(ctx, setup.first, call.ID, ActorApplication, ""); err != nil {
			t.Fatalf("End() error = %v", err)
		}
	}

	first, err := setup.service.List(ctx, setup.first, ListOptions{Limit: 2})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(first.Calls) != 2 || first.NextCursor == "" {
		t.Fatalf("first page held %d calls with cursor %q, want 2 and a continuation",
			len(first.Calls), first.NextCursor)
	}
	if first.Calls[0].ID != ordered[4] {
		t.Errorf("first result = %s, want the newest call %s", first.Calls[0].ID, ordered[4])
	}

	var seen []string
	cursor := ""
	for range 5 {
		page, err := setup.service.List(ctx, setup.first, ListOptions{Limit: 2, Cursor: cursor})
		if err != nil {
			t.Fatalf("List() error = %v", err)
		}
		for _, call := range page.Calls {
			seen = append(seen, call.ID)
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}

	if len(seen) != len(ordered) {
		t.Fatalf("paging saw %d calls, want %d", len(seen), len(ordered))
	}
	for index, id := range seen {
		want := ordered[len(ordered)-1-index]
		if id != want {
			t.Errorf("position %d = %s, want %s", index, id, want)
		}
	}
}

/*
TestTheStatusFilterAnswersWhatIsHappeningNow is how a caller asks whether a
room is busy, without a separate endpoint for it.
*/
func TestTheStatusFilterAnswersWhatIsHappeningNow(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	finished := setup.start(t, setup.first, setup.firstRoom)
	if _, err := setup.service.End(ctx, setup.first, finished.ID, ActorApplication, ""); err != nil {
		t.Fatalf("End() error = %v", err)
	}
	running := setup.start(t, setup.first, setup.firstRoom)

	active, err := setup.service.List(ctx, setup.first,
		ListOptions{RoomID: setup.firstRoom, Status: string(StatusActive)})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(active.Calls) != 1 || active.Calls[0].ID != running.ID {
		t.Fatalf("the active filter returned %d calls, want only the running one", len(active.Calls))
	}

	ended, err := setup.service.List(ctx, setup.first,
		ListOptions{RoomID: setup.firstRoom, Status: string(StatusEnded)})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(ended.Calls) != 1 || ended.Calls[0].ID != finished.ID {
		t.Fatalf("the ended filter returned %d calls, want only the finished one", len(ended.Calls))
	}

	everything, err := setup.service.List(ctx, setup.first, ListOptions{RoomID: setup.firstRoom})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(everything.Calls) != 2 {
		t.Errorf("an unfiltered history returned %d calls, want both", len(everything.Calls))
	}

	if _, err := setup.service.List(ctx, setup.first, ListOptions{Status: "ringing"}); err == nil {
		t.Error("a status Convia does not recognize was accepted as a filter")
	}
}

/*
TestASuspendedTenantIsNotServed proves the tenant check reaches every operation.

An application Convia has stopped serving must not keep starting conversations
or reading its history, and it is told the tenant is missing rather than that
it is suspended.
*/
func TestASuspendedTenantIsNotServed(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	call := setup.start(t, setup.first, setup.firstRoom)

	if _, err := setup.applications.Suspend(ctx, setup.first); err != nil {
		t.Fatalf("Suspend() error = %v", err)
	}

	if _, err := setup.service.Start(ctx, setup.first, setup.firstRoom, Definition{}, ActorApplication); !errors.Is(err, ErrApplicationNotFound) {
		t.Errorf("Start() error = %v, want %v", err, ErrApplicationNotFound)
	}
	if _, err := setup.service.Get(ctx, setup.first, call.ID); !errors.Is(err, ErrApplicationNotFound) {
		t.Errorf("Get() error = %v, want %v", err, ErrApplicationNotFound)
	}
	if _, err := setup.service.List(ctx, setup.first, ListOptions{}); !errors.Is(err, ErrApplicationNotFound) {
		t.Errorf("List() error = %v, want %v", err, ErrApplicationNotFound)
	}
	if _, err := setup.service.End(ctx, setup.first, call.ID, ActorApplication, ""); !errors.Is(err, ErrApplicationNotFound) {
		t.Errorf("End() error = %v, want %v", err, ErrApplicationNotFound)
	}
}

/*
TestTheAuditRecordsTheTransitionWithoutTheLabels keeps application text out of
the audit trail.

The metadata and the end reason are composed by the application and may say
something about the people in the call. What an operator needs is which call
changed, in which room, for which tenant, and on whose authority.
*/
func TestTheAuditRecordsTheTransitionWithoutTheLabels(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	const (
		secretReason   = "patient-consultation-ended"
		secretMetadata = "oncology-followup"
	)

	call, err := setup.service.Start(ctx, setup.first, setup.firstRoom,
		Definition{Metadata: map[string]string{"agenda": secretMetadata}}, ActorApplication)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if _, err := setup.service.End(ctx, setup.first, call.ID, ActorOperator, secretReason); err != nil {
		t.Fatalf("End() error = %v", err)
	}

	recorded := setup.logs.String()

	for _, event := range []string{"call.started", "call.ended"} {
		if !strings.Contains(recorded, event) {
			t.Errorf("the audit trail does not record %q", event)
		}
	}
	for _, expected := range []string{call.ID, setup.first, setup.firstRoom, string(ActorOperator)} {
		if !strings.Contains(recorded, expected) {
			t.Errorf("the audit trail does not record %q", expected)
		}
	}
	for _, leaked := range []string{secretReason, secretMetadata} {
		if strings.Contains(recorded, leaked) {
			t.Errorf("the audit trail recorded application-composed text: %q", leaked)
		}
	}
}

/*
TestMetadataIsStoredWithoutInterpretation proves the application's annotations
survive a round trip, and that they are validated the way rooms and users
validate theirs.
*/
func TestMetadataIsStoredWithoutInterpretation(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	call, err := setup.service.Start(ctx, setup.first, setup.firstRoom,
		Definition{Metadata: map[string]string{"agenda": "sprint_review", "locale": "pt-BR"}},
		ActorApplication)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	stored, err := setup.service.Get(ctx, setup.first, call.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if stored.Metadata["agenda"] != "sprint_review" || stored.Metadata["locale"] != "pt-BR" {
		t.Errorf("metadata = %v, want it stored unchanged", stored.Metadata)
	}

	if _, err := setup.service.End(ctx, setup.first, call.ID, ActorApplication, ""); err != nil {
		t.Fatalf("End() error = %v", err)
	}

	_, err = setup.service.Start(ctx, setup.first, setup.firstRoom,
		Definition{Metadata: map[string]string{"Agenda": "rejected"}}, ActorApplication)

	var validation ValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("Start() error = %v, want a ValidationError for an uppercase key", err)
	}
}

/*
TestAnInvalidIdentifierIsMissingRatherThanAnError proves a malformed identifier
is answered the same way an unknown one is, so probing tells a caller nothing.
*/
func TestAnInvalidIdentifierIsMissingRatherThanAnError(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	for _, id := range []string{"", "room_7KQZP4XN2VJH6TBWMDR3YAFC5E", "call_lowercase", "not-an-id"} {
		if _, err := setup.service.Get(ctx, setup.first, id); !errors.Is(err, ErrNotFound) {
			t.Errorf("Get(%q) error = %v, want %v", id, err, ErrNotFound)
		}
	}
}
