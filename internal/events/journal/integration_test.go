package journal

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"convia/internal/config"
	"convia/internal/database"
	"convia/internal/events"
	"convia/internal/transaction"
)

const testDatabaseURLEnvironment = "CONVIA_TEST_DATABASE_URL"

func newPool(t *testing.T) *pgxpool.Pool {
	t.Helper()

	maintenanceURL := strings.TrimSpace(os.Getenv(testDatabaseURLEnvironment))
	if maintenanceURL == "" {
		t.Skipf("set %s to run the journal integration tests", testDatabaseURLEnvironment)
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

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := context.Background()
	if err := database.Migrate(ctx, parsed.String(), logger); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	pool, err := database.Open(ctx, config.Database{URL: parsed.String(), MaxConnections: 8,
		ConnectTimeout: 10 * time.Second, QueryTimeout: 5 * time.Second}, logger)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
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

// heard keeps what a follower delivered.
type heard struct {
	mutex  sync.Mutex
	events []events.Event
}

func (sink *heard) Receive(event events.Event) {
	sink.mutex.Lock()
	defer sink.mutex.Unlock()
	sink.events = append(sink.events, event)
}

func (sink *heard) ids() []string {
	sink.mutex.Lock()
	defer sink.mutex.Unlock()
	var ids []string
	for _, event := range sink.events {
		ids = append(ids, event.ID)
	}
	return ids
}

func (sink *heard) waitFor(t *testing.T, count int) []string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if ids := sink.ids(); len(ids) >= count {
			return ids
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("delivered %v, want %d events", sink.ids(), count)
	return nil
}

func event(applicationID string) events.Event {
	return events.New(events.CallStarted, applicationID, "call_1", "", events.Data{"room_id": "room_1"})
}

func record(t *testing.T, ctx context.Context, journal *Journal, recorded events.Event) events.Event {
	t.Helper()
	written, err := journal.Record(ctx, recorded)
	if err != nil {
		t.Fatalf("Record() error = %v", err)
	}
	return written
}

// TestAnEventIsRecordedOnlyWithItsChange: outside a transaction nothing is written, and a rollback keeps nothing.
func TestAnEventIsRecordedOnlyWithItsChange(t *testing.T) {
	pool := newPool(t)
	journal := New(pool)
	ctx := context.Background()

	if _, err := journal.Record(ctx, event("app_1")); !errors.Is(err, transaction.ErrNone) {
		t.Errorf("Record() outside a transaction error = %v, want %v", err, transaction.ErrNone)
	}

	undone := errors.New("the change failed")
	err := transaction.Run(ctx, pool, func(ctx context.Context) error {
		written := record(t, ctx, journal, event("app_1"))
		if _, err := events.ParseCursor(written.Cursor); err != nil {
			t.Errorf("Record() gave cursor %q: %v", written.Cursor, err)
		}
		return undone
	})
	if !errors.Is(err, undone) {
		t.Fatalf("Run() error = %v", err)
	}

	read, err := journal.next(ctx, events.Cursor{}, batch)
	if err != nil || len(read) != 0 {
		t.Errorf("next() = %v, %v, want nothing kept from a rolled-back change", read, err)
	}
}

/*
TestAnEventIsNeverDeliveredBehindTheFollower is the reason for ordering by
transaction. The first transaction writes before the second but records its
event after it; the second commits first. The follower must not pass the second
event while the first could still commit behind it.
*/
func TestAnEventIsNeverDeliveredBehindTheFollower(t *testing.T) {
	pool := newPool(t)
	journal := New(pool)
	ctx := context.Background()

	sink := &heard{}
	follower, err := NewFollower(ctx, journal, sink, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("NewFollower() error = %v", err)
	}
	running, stop := context.WithCancel(ctx)
	defer stop()
	go follower.Run(running)

	first, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin() error = %v", err)
	}
	defer func() { _ = first.Rollback(ctx) }()
	// Writing anything gives the first transaction its identifier now.
	if _, err := first.Exec(ctx, `SELECT pg_current_xact_id()`); err != nil {
		t.Fatalf("take a transaction identifier: %v", err)
	}

	second := event("app_1")
	if err := transaction.Run(ctx, pool, func(ctx context.Context) error {
		second = record(t, ctx, journal, second)
		return nil
	}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	follower.Wake()
	time.Sleep(3 * interval)
	if delivered := sink.ids(); len(delivered) != 0 {
		t.Fatalf("delivered %v while an older transaction was still open", delivered)
	}

	firstEvent := event("app_1")
	body, err := json.Marshal(firstEvent)
	if err != nil {
		t.Fatalf("render the first event: %v", err)
	}
	if _, err := first.Exec(ctx, `INSERT INTO event_journal (id, application_id, type, body, recorded_at)
	                              VALUES ($1, $2, $3, $4, now())`,
		firstEvent.ID, firstEvent.ApplicationID, string(firstEvent.Type), string(body)); err != nil {
		t.Fatalf("record in the first transaction: %v", err)
	}
	if err := first.Commit(ctx); err != nil {
		t.Fatalf("Commit() error = %v", err)
	}
	follower.Wake()

	if delivered := sink.waitFor(t, 2); !slices.Equal(delivered, []string{firstEvent.ID, second.ID}) {
		t.Errorf("delivered %v, want the first transaction's event and then %s", delivered, second.ID)
	}
	if position := follower.Position(); position.String() != second.Cursor {
		t.Errorf("Position() = %s, want %s", position, second.Cursor)
	}
}

// TestAReplayIsOneApplicationsAndStopsAtTheFloor covers what a resuming stream reads.
func TestAReplayIsOneApplicationsAndStopsAtTheFloor(t *testing.T) {
	pool := newPool(t)
	journal := New(pool)
	ctx := context.Background()

	var mine, theirs []events.Event
	for range 3 {
		if err := transaction.Run(ctx, pool, func(ctx context.Context) error {
			mine = append(mine, record(t, ctx, journal, event("app_1")))
			theirs = append(theirs, record(t, ctx, journal, event("app_2")))
			return nil
		}); err != nil {
			t.Fatalf("Run() error = %v", err)
		}
	}

	/*
		A transaction still running anywhere on the server, in any database,
		keeps the newest events unsettled for a moment, so the head is asked for
		until they are.
	*/
	var (
		follower *Follower
		head     events.Cursor
	)
	for deadline := time.Now().Add(5 * time.Second); ; {
		var err error
		follower, err = NewFollower(ctx, journal, &heard{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
		if err != nil {
			t.Fatalf("NewFollower() error = %v", err)
		}
		head = follower.Position()
		if head.String() == theirs[2].Cursor {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("a new follower starts at %s, want the head %s", head, theirs[2].Cursor)
		}
		time.Sleep(20 * time.Millisecond)
	}

	after, _ := events.ParseCursor(mine[0].Cursor)
	var replayed []string
	if err := follower.Replay(ctx, "app_1", after, head, func(event events.Event) error {
		replayed = append(replayed, event.ID)
		return nil
	}); err != nil {
		t.Fatalf("Replay() error = %v", err)
	}
	if want := []string{mine[1].ID, mine[2].ID}; !slices.Equal(replayed, want) {
		t.Errorf("Replay() = %v, want %v", replayed, want)
	}

	if _, err := pool.Exec(ctx, `UPDATE event_journal SET recorded_at = $1 WHERE id = ANY($2)`,
		time.Now().Add(-2*Retention), []string{mine[0].ID, theirs[0].ID, mine[1].ID}); err != nil {
		t.Fatalf("age some events: %v", err)
	}
	removed, err := journal.Prune(ctx, time.Now().Add(-Retention))
	if err != nil || removed != 3 {
		t.Fatalf("Prune() = %d, %v, want 3", removed, err)
	}

	err = follower.Replay(ctx, "app_1", after, head, func(events.Event) error { return nil })
	if !errors.Is(err, events.ErrCursorTooOld) {
		t.Errorf("Replay() from before the floor error = %v, want %v", err, events.ErrCursorTooOld)
	}

	fromFloor, _ := events.ParseCursor(mine[1].Cursor)
	replayed = nil
	if err := follower.Replay(ctx, "app_1", fromFloor, head, func(event events.Event) error {
		replayed = append(replayed, event.ID)
		return nil
	}); err != nil {
		t.Fatalf("Replay() from the floor error = %v", err)
	}
	if want := []string{mine[2].ID}; !slices.Equal(replayed, want) {
		t.Errorf("Replay() from the floor = %v, want %v", replayed, want)
	}
}
