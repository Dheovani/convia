package messages

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
	"convia/internal/calls"
	"convia/internal/config"
	"convia/internal/database"
	"convia/internal/events"
	"convia/internal/invitations"
	"convia/internal/media"
	"convia/internal/participants"
	"convia/internal/rooms"
	"convia/internal/transaction"
	"convia/internal/users"
)

/*
testDatabaseURLEnvironment points these tests at a PostgreSQL instance.

They are skipped when it is unset, so `go test ./...` stays runnable without
infrastructure. Each test gets a database of its own, so nothing shares state.
*/
const testDatabaseURLEnvironment = "CONVIA_TEST_DATABASE_URL"

// fixture is the message service over a real database, with the domains it
// depends on wired the way the composition root wires them.
type fixture struct {
	service     *Service
	store       *Store
	rooms       *rooms.Service
	users       *users.Service
	calls       *calls.Service
	invitations *invitations.Service
	pool        *pgxpool.Pool
	published   *recorder
	first       string
	second      string
	logs        *bytes.Buffer
}

func newFixture(t *testing.T) fixture {
	t.Helper()

	maintenanceURL := strings.TrimSpace(os.Getenv(testDatabaseURLEnvironment))
	if maintenanceURL == "" {
		t.Skipf("set %s to run the message integration tests", testDatabaseURLEnvironment)
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
		MaxConnections: 24,
		ConnectTimeout: 10 * time.Second,
		QueryTimeout:   5 * time.Second,
	}, logger)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(pool.Close)

	applicationService := applications.NewService(applications.NewStore(pool), logger)
	userService := users.NewService(users.NewStore(pool), applicationService, logger)
	announcer := events.NewAnnouncer(events.NewBroker(), nil, logger)
	roomService := rooms.NewService(rooms.NewStore(pool), applicationService, userService, announcer, logger)
	callService := calls.NewService(calls.NewStore(pool), applicationService, roomService,
		media.Absent{}, announcer, logger)
	participantService := participants.NewService(participants.NewStore(pool),
		applicationService, callService, roomService, userService, announcer, logger)
	invitationService := invitations.NewService(invitations.NewStore(pool), applicationService,
		callService, userService, participantService, announcer, logger)

	store := NewStore(pool)
	// The message service announces into a recorder so that a test can assert
	// what left the domain, not merely that the domain did not fail.
	published := &recorder{}

	setup := fixture{
		service: NewService(store, applicationService, roomService, userService,
			invitationService, published, logger),
		store:       store,
		rooms:       roomService,
		users:       userService,
		calls:       callService,
		invitations: invitationService,
		pool:        pool,
		published:   published,
		first:       newApplication(t, applicationService, "First Tenant"),
		second:      newApplication(t, applicationService, "Second Tenant"),
		logs:        logs,
	}

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

func execute(t *testing.T, databaseURL, statement string) {
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

// newRoom makes a room to talk in.
func (setup fixture) newRoom(t *testing.T, applicationID string) rooms.Room {
	t.Helper()

	room, err := setup.rooms.Create(context.Background(), applicationID, rooms.Definition{Name: "Standup"})
	if err != nil {
		t.Fatalf("create a room: %v", err)
	}
	return room
}

// newAuthor resolves one of an application's people into a user.
func (setup fixture) newAuthor(t *testing.T, applicationID, subject string) Author {
	t.Helper()

	person, _, err := setup.users.Resolve(context.Background(), applicationID,
		users.Identity{ExternalSubject: subject})
	if err != nil {
		t.Fatalf("resolve %q: %v", subject, err)
	}
	return Author{UserID: person.ID}
}

/*
TestAPositionIsAllocatedByTheDatabaseAndStartsAtOne pins the ordering
guarantee's two visible properties.

The sequence is what a client orders by and what read state will later be
expressed in, so where it starts and that it advances by one are part of the
contract rather than an implementation detail.
*/
func TestAPositionIsAllocatedByTheDatabaseAndStartsAtOne(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	room := setup.newRoom(t, setup.first)
	author := setup.newAuthor(t, setup.first, "ana")

	for expected := FirstSequence; expected <= 3; expected++ {
		message, err := setup.service.Post(ctx, setup.first, room.ID, author, "hello")
		if err != nil {
			t.Fatalf("Post() error = %v", err)
		}
		if message.Sequence != expected {
			t.Errorf("Post() sequence = %d, want %d", message.Sequence, expected)
		}
	}

	/*
		A second room counts from the beginning again. The sequence orders one
		room's history and is not a global clock, which is what lets it be a
		small number a client can hold on to.
	*/
	other := setup.newRoom(t, setup.first)
	message, err := setup.service.Post(ctx, setup.first, other.ID, author, "hello")
	if err != nil {
		t.Fatalf("Post() error = %v", err)
	}
	if message.Sequence != FirstSequence {
		t.Errorf("a new room's first message has sequence %d, want %d", message.Sequence, FirstSequence)
	}
}

/*
TestSimultaneousMessagesEachTakeOneAndOnlyOnePosition is the test the ordering
guarantee rests on.

Two instances appending to one room read the same highest sequence and reach for
the same next one. This asserts what the unique index is for: exactly one of
them gets it, the other takes the position after, and no two messages in a room
ever share a place in its history.
*/
func TestSimultaneousMessagesEachTakeOneAndOnlyOnePosition(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	room := setup.newRoom(t, setup.first)
	author := setup.newAuthor(t, setup.first, "ana")

	const writers = 16

	var (
		start     sync.WaitGroup
		finished  sync.WaitGroup
		mutex     sync.Mutex
		sequences []int64
		failures  []error
	)

	start.Add(1)
	for writer := 0; writer < writers; writer++ {
		finished.Add(1)
		go func() {
			defer finished.Done()
			start.Wait()

			message, err := setup.service.Post(ctx, setup.first, room.ID, author, "at the same moment")

			mutex.Lock()
			defer mutex.Unlock()
			if err != nil {
				failures = append(failures, err)
				return
			}
			sequences = append(sequences, message.Sequence)
		}()
	}

	start.Done()
	finished.Wait()

	if len(failures) > 0 {
		t.Fatalf("%d of %d simultaneous messages failed, the first being %v",
			len(failures), writers, failures[0])
	}

	seen := make(map[int64]bool, len(sequences))
	for _, sequence := range sequences {
		if seen[sequence] {
			t.Errorf("sequence %d was taken twice", sequence)
		}
		seen[sequence] = true
	}

	for expected := FirstSequence; expected < FirstSequence+writers; expected++ {
		if !seen[expected] {
			t.Errorf("sequence %d was taken by nobody, so the history has a hole", expected)
		}
	}
}

/*
TestHistoryReadsBothWaysFromWhereItLeftOff covers the two readings that are
genuinely different: opening a room, and catching up after being away.
*/
func TestHistoryReadsBothWaysFromWhereItLeftOff(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	room := setup.newRoom(t, setup.first)
	author := setup.newAuthor(t, setup.first, "ana")

	const total = 7
	for index := 0; index < total; index++ {
		if _, err := setup.service.Post(ctx, setup.first, room.ID, author, "a message"); err != nil {
			t.Fatalf("Post() error = %v", err)
		}
	}

	// Opening a room: the newest first, with more behind it.
	page, err := setup.service.History(ctx, setup.first, room.ID,
		HistoryOptions{Direction: Older, Limit: 3})
	if err != nil {
		t.Fatalf("History() error = %v", err)
	}
	if got := sequencesOf(page.Messages); !equal(got, []int64{7, 6, 5}) {
		t.Errorf("the first page reads %v, want [7 6 5]", got)
	}
	if page.NextCursor != "5" {
		t.Errorf("NextCursor = %q, want the last sequence on the page", page.NextCursor)
	}

	older := int64(5)
	page, err = setup.service.History(ctx, setup.first, room.ID,
		HistoryOptions{Direction: Older, After: &older, Limit: 3})
	if err != nil {
		t.Fatalf("History() error = %v", err)
	}
	if got := sequencesOf(page.Messages); !equal(got, []int64{4, 3, 2}) {
		t.Errorf("the second page reads %v, want [4 3 2]", got)
	}

	// Catching up: forwards from where a reader stopped, oldest first.
	stopped := int64(4)
	page, err = setup.service.History(ctx, setup.first, room.ID,
		HistoryOptions{Direction: Newer, After: &stopped, Limit: 10})
	if err != nil {
		t.Fatalf("History() error = %v", err)
	}
	if got := sequencesOf(page.Messages); !equal(got, []int64{5, 6, 7}) {
		t.Errorf("catching up reads %v, want [5 6 7]", got)
	}
	if page.NextCursor != "" {
		t.Errorf("NextCursor = %q, want none at the end of the history", page.NextCursor)
	}
}

/*
TestADeletedMessageKeepsItsPlaceAndLosesItsBody is what separates a tombstone
from a removal.

If the row went away, every later message would keep its sequence but the
history would have a hole a client could not tell from a message it had not
received yet. The row stays so that the hole is visible and explained.
*/
func TestADeletedMessageKeepsItsPlaceAndLosesItsBody(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	room := setup.newRoom(t, setup.first)
	author := setup.newAuthor(t, setup.first, "ana")

	var written []Message
	for _, body := range []string{"first", "second", "third"} {
		message, err := setup.service.Post(ctx, setup.first, room.ID, author, body)
		if err != nil {
			t.Fatalf("Post() error = %v", err)
		}
		written = append(written, message)
	}

	deleted, err := setup.service.Delete(ctx, setup.first, written[1].ID, author)
	if err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if !deleted.Deleted() {
		t.Error("the message does not report as deleted")
	}
	if deleted.Body != "" {
		t.Errorf("the tombstone still carries %q", deleted.Body)
	}
	if deleted.Sequence != written[1].Sequence {
		t.Errorf("deleting moved the message from %d to %d", written[1].Sequence, deleted.Sequence)
	}

	page, err := setup.service.History(ctx, setup.first, room.ID, HistoryOptions{Limit: 10})
	if err != nil {
		t.Fatalf("History() error = %v", err)
	}
	if got := sequencesOf(page.Messages); !equal(got, []int64{3, 2, 1}) {
		t.Errorf("the history reads %v, want the tombstone still in place", got)
	}

	// Deleting again is the state the caller asked for, not a failure.
	if _, err := setup.service.Delete(ctx, setup.first, written[1].ID, author); err != nil {
		t.Errorf("deleting an already deleted message error = %v, want none", err)
	}
}

/*
TestOnlyTheAuthorChangesWhatTheySaid keeps an application from rewriting one of
its people's words while claiming to be them.
*/
func TestOnlyTheAuthorChangesWhatTheySaid(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	room := setup.newRoom(t, setup.first)
	ana := setup.newAuthor(t, setup.first, "ana")
	bruno := setup.newAuthor(t, setup.first, "bruno")

	message, err := setup.service.Post(ctx, setup.first, room.ID, ana, "what I said")
	if err != nil {
		t.Fatalf("Post() error = %v", err)
	}

	if _, err := setup.service.Edit(ctx, setup.first, message.ID, bruno, "what she said"); !errors.Is(err, ErrNotAuthor) {
		t.Errorf("Edit() by somebody else error = %v, want %v", err, ErrNotAuthor)
	}
	if _, err := setup.service.Delete(ctx, setup.first, message.ID, bruno); !errors.Is(err, ErrNotAuthor) {
		t.Errorf("Delete() by somebody else error = %v, want %v", err, ErrNotAuthor)
	}

	edited, err := setup.service.Edit(ctx, setup.first, message.ID, ana, "what I meant")
	if err != nil {
		t.Fatalf("Edit() error = %v", err)
	}
	if edited.Body != "what I meant" {
		t.Errorf("Edit() body = %q", edited.Body)
	}
	if !edited.Edited() {
		t.Error("an edited message does not report as edited")
	}
	if edited.Sequence != message.Sequence {
		t.Error("editing moved the message in the history")
	}

	// An edit must not be able to put something back where the author removed it.
	if _, err := setup.service.Delete(ctx, setup.first, message.ID, ana); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if _, err := setup.service.Edit(ctx, setup.first, message.ID, ana, "back again"); !errors.Is(err, ErrDeleted) {
		t.Errorf("editing a deleted message error = %v, want %v", err, ErrDeleted)
	}
}

/*
TestAClosedRoomKeepsItsHistoryAndTakesNothingNew is what makes closing a room
mean something without making it lossy.
*/
func TestAClosedRoomKeepsItsHistoryAndTakesNothingNew(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	room := setup.newRoom(t, setup.first)
	author := setup.newAuthor(t, setup.first, "ana")

	if _, err := setup.service.Post(ctx, setup.first, room.ID, author, "before"); err != nil {
		t.Fatalf("Post() error = %v", err)
	}

	if _, err := setup.rooms.Close(ctx, setup.first, room.ID); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	if _, err := setup.service.Post(ctx, setup.first, room.ID, author, "after"); !errors.Is(err, ErrRoomClosed) {
		t.Errorf("Post() into a closed room error = %v, want %v", err, ErrRoomClosed)
	}

	page, err := setup.service.History(ctx, setup.first, room.ID, HistoryOptions{Limit: 10})
	if err != nil {
		t.Fatalf("History() on a closed room error = %v, want it readable", err)
	}
	if len(page.Messages) != 1 {
		t.Errorf("a closed room's history holds %d messages, want 1", len(page.Messages))
	}
}

/*
TestOneTenantCannotWriteIntoAnothersRoom is the boundary every store method in
Convia is scoped by, asserted rather than trusted.
*/
func TestOneTenantCannotWriteIntoAnothersRoom(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	room := setup.newRoom(t, setup.first)
	mine := setup.newAuthor(t, setup.first, "ana")
	theirs := setup.newAuthor(t, setup.second, "ana")

	message, err := setup.service.Post(ctx, setup.first, room.ID, mine, "ours")
	if err != nil {
		t.Fatalf("Post() error = %v", err)
	}

	if _, err := setup.service.Post(ctx, setup.second, room.ID, theirs, "theirs"); !errors.Is(err, ErrRoomNotFound) {
		t.Errorf("posting into another tenant's room error = %v, want %v", err, ErrRoomNotFound)
	}
	if _, err := setup.service.Get(ctx, setup.second, message.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("reading another tenant's message error = %v, want %v", err, ErrNotFound)
	}
	if _, err := setup.service.History(ctx, setup.second, room.ID, HistoryOptions{}); !errors.Is(err, ErrRoomNotFound) {
		t.Errorf("reading another tenant's history error = %v, want %v", err, ErrRoomNotFound)
	}
}

/*
TestAnAuthorMustBeSomebodyTheApplicationStillHas keeps a message from being
attributed to an identifier nobody can resolve.
*/
func TestAnAuthorMustBeSomebodyTheApplicationStillHas(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	room := setup.newRoom(t, setup.first)
	author := setup.newAuthor(t, setup.first, "ana")

	if _, err := setup.service.Post(ctx, setup.first, room.ID, Author{}, "nobody"); err == nil {
		t.Error("a message with no author was accepted")
	}
	if _, err := setup.service.Post(ctx, setup.first, room.ID,
		Author{UserID: "usr_AAAAAAAAAAAAAAAAAAAAAAAAAA"}, "a stranger"); err == nil {
		t.Error("a message from an unknown user was accepted")
	}

	if _, err := setup.users.Suspend(ctx, setup.first, author.UserID); err != nil {
		t.Fatalf("Suspend() error = %v", err)
	}
	if _, err := setup.service.Post(ctx, setup.first, room.ID, author, "while suspended"); err == nil {
		t.Error("a suspended user wrote a message")
	}
}

/*
TestNothingSomebodySaidReachesTheLog is asserted against the real log a running
instance writes, rather than against intent.

An audit trail records that somebody wrote in a room. Repeating what they wrote
would put every private conversation into a stream that is shipped, retained,
and read by people who are not in the room.
*/
func TestNothingSomebodySaidReachesTheLog(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	room := setup.newRoom(t, setup.first)
	author := setup.newAuthor(t, setup.first, "ana")

	const secret = "the merger closes on tuesday"
	message, err := setup.service.Post(ctx, setup.first, room.ID, author, secret)
	if err != nil {
		t.Fatalf("Post() error = %v", err)
	}
	if _, err := setup.service.Edit(ctx, setup.first, message.ID, author, secret+", revised"); err != nil {
		t.Fatalf("Edit() error = %v", err)
	}

	written := setup.logs.String()
	if strings.Contains(written, secret) {
		t.Errorf("the log carries what was said:\n%s", written)
	}
	if !strings.Contains(written, message.ID) {
		t.Errorf("the log does not name the message, so an operator cannot trace it:\n%s", written)
	}
	if !strings.Contains(written, "message.posted") || !strings.Contains(written, "message.edited") {
		t.Errorf("the log does not record what happened:\n%s", written)
	}
}

func sequencesOf(messages []Message) []int64 {
	sequences := make([]int64, 0, len(messages))
	for _, message := range messages {
		sequences = append(sequences, message.Sequence)
	}
	return sequences
}

func equal(got, want []int64) bool {
	if len(got) != len(want) {
		return false
	}
	for index := range got {
		if got[index] != want[index] {
			return false
		}
	}
	return true
}

/*
recorder keeps every event the domain announced.

It is a mutex rather than a slice alone because one test appends from sixteen
goroutines, and a data race in a test fixture is still a data race.
*/
type recorder struct {
	mutex  sync.Mutex
	events []events.Event
}

func (record *recorder) Publish(ctx context.Context, event events.Event) error {
	// Only what committed was announced.
	transaction.AfterCommit(ctx, func(context.Context) {
		record.mutex.Lock()
		defer record.mutex.Unlock()
		record.events = append(record.events, event)
	})
	return nil
}

func (record *recorder) all() []events.Event {
	record.mutex.Lock()
	defer record.mutex.Unlock()
	return append([]events.Event(nil), record.events...)
}
