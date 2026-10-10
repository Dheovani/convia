package audit_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"log/slog"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"convia/internal/api"
	"convia/internal/applications"
	"convia/internal/audit"
	"convia/internal/config"
	"convia/internal/credentials"
	"convia/internal/database"
	"convia/internal/transaction"
)

/*
testDatabaseURLEnvironment points these tests at a PostgreSQL instance.

The tests are skipped when it is unset, so `go test ./...` runs without
infrastructure, and every test works in its own database so runs never share
state.
*/
const testDatabaseURLEnvironment = "CONVIA_TEST_DATABASE_URL"

type fixture struct {
	trail *audit.Service
	pool  *pgxpool.Pool
	logs  *bytes.Buffer
}

func newFixture(t *testing.T) fixture {
	t.Helper()

	maintenanceURL := strings.TrimSpace(os.Getenv(testDatabaseURLEnvironment))
	if maintenanceURL == "" {
		t.Skipf("set %s to run the audit integration tests", testDatabaseURLEnvironment)
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
	}, logger, nil)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(pool.Close)

	logs.Reset()
	return fixture{trail: audit.NewService(audit.NewStore(pool), logger), pool: pool, logs: logs}
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

func as(actor audit.Actor) context.Context {
	return audit.ContextWithActor(api.WithRequestID(context.Background(), "req_"+strings.ToLower(rand.Text()[:8])), actor)
}

func operatorNamed(id string) audit.Actor {
	return audit.Actor{Kind: audit.KindOperator, ID: id}
}

func everything(t *testing.T, f fixture, query audit.Query) []audit.Entry {
	t.Helper()

	page, err := f.trail.Search(context.Background(), audit.SearchOptions{Query: query, Limit: 100})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	return page.Entries
}

/*
TestTheTrailSaysWhoSuspendedATenant is the defect `M21` was opened on, end to
end: an application suspended by an operator, read back as having been
suspended by that operator.

It used to say `actor=unauthenticated`, in a log line nothing could query.
*/
func TestTheTrailSaysWhoSuspendedATenant(t *testing.T) {
	f := newFixture(t)
	tenants := applications.NewService(applications.NewStore(f.pool), f.trail)

	ctx := as(operatorNamed("oper_7KQZP4XN2VJH6TBWMDR3YAFC5E"))
	application, err := tenants.Create(ctx, "Orbit")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if _, err := tenants.Suspend(ctx, application.ID); err != nil {
		t.Fatalf("Suspend() error = %v", err)
	}

	entries := everything(t, f, audit.Query{ApplicationID: application.ID})
	if len(entries) != 2 {
		t.Fatalf("entries = %+v, want created and suspended", entries)
	}

	suspended := entries[0]
	if suspended.Action != "application.suspended" {
		t.Errorf("newest action = %q, want application.suspended", suspended.Action)
	}
	if suspended.Actor != operatorNamed("oper_7KQZP4XN2VJH6TBWMDR3YAFC5E") {
		t.Errorf("Actor = %+v, want the operator who did it", suspended.Actor)
	}
	if suspended.Subject != (audit.Subject{Kind: "application", ID: application.ID}) {
		t.Errorf("Subject = %+v", suspended.Subject)
	}
	if !strings.HasPrefix(suspended.RequestID, "req_") {
		t.Errorf("RequestID = %q, want the request that caused it", suspended.RequestID)
	}
}

/*
TestAChangeThatRollsBackLeavesNoEntry is the other half of writing them together:
the trail never claims something happened that did not.
*/
func TestAChangeThatRollsBackLeavesNoEntry(t *testing.T) {
	f := newFixture(t)
	refused := errors.New("the change after it failed")

	err := transaction.Run(context.Background(), f.pool, func(ctx context.Context) error {
		if _, err := f.trail.Record(audit.ContextWithActor(ctx, operatorNamed("oper_X")), audit.Written{
			Action:  "room.closed",
			Subject: audit.Subject{Kind: "room", ID: "room_X"},
		}); err != nil {
			return err
		}
		return refused
	})
	if !errors.Is(err, refused) {
		t.Fatalf("Run() error = %v", err)
	}

	if entries := everything(t, f, audit.Query{}); len(entries) != 0 {
		t.Errorf("a rolled-back change left %d entries in the trail", len(entries))
	}
	if strings.Contains(f.logs.String(), "audit event") {
		t.Errorf("a rolled-back change was logged as though it happened: %s", f.logs.String())
	}
}

/*
TestAKeyIsRecordedWithWhatItCanDo is why entries have details at all: an action
name says a key was issued, and only this says what the key can do.
*/
func TestAKeyIsRecordedWithWhatItCanDo(t *testing.T) {
	f := newFixture(t)
	tenants := applications.NewService(applications.NewStore(f.pool), f.trail)
	keys := credentials.NewService(credentials.NewStore(f.pool), tenants, f.trail)

	ctx := as(operatorNamed("oper_X"))
	application, err := tenants.Create(ctx, "Workspace Town")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	credential, secret, err := keys.Issue(ctx, application.ID, credentials.Request{
		Name:   "backend",
		Scopes: []credentials.Scope{credentials.ScopeUsersRead, credentials.ScopeUsersWrite},
	})
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}

	entries := everything(t, f, audit.Query{SubjectKind: "credential", SubjectID: credential.ID})
	if len(entries) != 1 {
		t.Fatalf("entries = %+v, want one", entries)
	}
	if got := entries[0].Details["scopes"]; got != "users:read users:write" {
		t.Errorf("scopes = %q, want what the key was minted with", got)
	}

	var stored string
	if err := f.pool.QueryRow(context.Background(),
		`SELECT details::text || coalesce(reason, '') FROM audit_entries WHERE subject_id = $1`,
		credential.ID).Scan(&stored); err != nil {
		t.Fatalf("read the row: %v", err)
	}
	if strings.Contains(stored, string(secret)) {
		t.Error("the trail holds the secret it exists to protect")
	}
}

/*
TestAnEntryOutlivesWhatItDescribes is why the table has no foreign keys: the
record that a tenant was deleted is worth most once the tenant is gone.
*/
func TestAnEntryOutlivesWhatItDescribes(t *testing.T) {
	f := newFixture(t)
	tenants := applications.NewService(applications.NewStore(f.pool), f.trail)

	ctx := as(operatorNamed("oper_X"))
	application, err := tenants.Create(ctx, "Short-lived")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if err := tenants.Delete(ctx, application.ID); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if _, err := f.pool.Exec(context.Background(), `DELETE FROM applications WHERE id = $1`, application.ID); err != nil {
		t.Fatalf("erase the row: %v", err)
	}

	entries := everything(t, f, audit.Query{ApplicationID: application.ID})
	if len(entries) != 2 || entries[0].Action != "application.deleted" {
		t.Errorf("entries = %+v, want created and deleted to survive the row", entries)
	}
}

/*
TestEachFilterNarrowsTheTrail writes a small history and asks it the questions
an incident asks.
*/
func TestEachFilterNarrowsTheTrail(t *testing.T) {
	f := newFixture(t)

	record := func(actor audit.Actor, action, applicationID string, subject audit.Subject) audit.Entry {
		t.Helper()
		entry, err := f.trail.Record(as(actor), audit.Written{
			Action: action, Subject: subject, ApplicationID: applicationID,
		})
		if err != nil {
			t.Fatalf("Record(%s) error = %v", action, err)
		}
		return entry
	}

	room := audit.Subject{Kind: "room", ID: "room_A"}
	first := record(operatorNamed("oper_A"), "room.closed", "app_A", room)
	record(audit.Actor{Kind: audit.KindApplication, ID: "cred_B"}, "room.reopened", "app_A", room)
	record(operatorNamed("oper_A"), "application.suspended", "app_B", audit.Subject{Kind: "application", ID: "app_B"})
	record(audit.System(), "call.ended", "app_B", audit.Subject{Kind: "call", ID: "call_C"})

	cases := map[string]struct {
		query audit.Query
		want  int
	}{
		"nothing":         {audit.Query{}, 4},
		"tenant":          {audit.Query{ApplicationID: "app_A"}, 2},
		"operator":        {audit.Query{ActorKind: audit.KindOperator, ActorID: "oper_A"}, 2},
		"system":          {audit.Query{ActorKind: audit.KindSystem}, 1},
		"room":            {audit.Query{SubjectKind: "room", SubjectID: "room_A"}, 2},
		"action":          {audit.Query{Action: "room.closed"}, 1},
		"all together":    {audit.Query{ApplicationID: "app_A", ActorID: "oper_A", Action: "room.closed"}, 1},
		"after the last":  {audit.Query{Since: time.Now().Add(time.Minute)}, 0},
		"up to the first": {audit.Query{Until: first.RecordedAt}, 1},
	}

	for name, test := range cases {
		if got := len(everything(t, f, test.query)); got != test.want {
			t.Errorf("%s: %d entries, want %d", name, got, test.want)
		}
	}
}

/*
TestPagesNeitherRepeatNorSkip walks the trail one entry at a time, across
entries stamped in the same microsecond.

The ties are made on purpose, through the store, because they are what the
identifier in the cursor is for and nothing else would produce them reliably:
two writes landing in one microsecond is what happens on a busy installation
and almost never in a test.
*/
func TestPagesNeitherRepeatNorSkip(t *testing.T) {
	f := newFixture(t)
	store := audit.NewStore(f.pool)
	tied := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)

	written := map[string]bool{}
	for index := 0; index < 5; index++ {
		entry, err := audit.Record("room.closed", operatorNamed("oper_X"),
			audit.Subject{Kind: "room", ID: "room_X"}, "", "", nil, "req", tied)
		if err != nil {
			t.Fatalf("Record() error = %v", err)
		}
		if err := store.Append(context.Background(), entry); err != nil {
			t.Fatalf("Append() error = %v", err)
		}
		written[entry.ID] = true
	}

	seen := map[string]bool{}
	cursor := ""
	for pages := 0; pages < 10; pages++ {
		page, err := f.trail.Search(context.Background(), audit.SearchOptions{Limit: 1, Cursor: cursor})
		if err != nil {
			t.Fatalf("Search() error = %v", err)
		}
		for _, entry := range page.Entries {
			if seen[entry.ID] {
				t.Fatalf("%s was returned twice", entry.ID)
			}
			seen[entry.ID] = true
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}

	if len(seen) != len(written) {
		t.Errorf("walked %d entries, wrote %d", len(seen), len(written))
	}
}

/*
TestTheSchemaRefusesWhatTheDomainRefuses holds the constraints to the rules they
restate, so a write that bypassed the domain still could not record an action
by nobody, or a reason that says nothing.
*/
func TestTheSchemaRefusesWhatTheDomainRefuses(t *testing.T) {
	f := newFixture(t)

	insert := `INSERT INTO audit_entries (id, action, actor_kind, actor_id, application_id,
		subject_kind, subject_id, reason, details, request_id, recorded_at)
		VALUES ($1, 'room.closed', $2, $3, NULL, 'room', 'room_X', $4, $5::jsonb, 'req', now())`

	cases := map[string][]any{
		"an operator with no identifier": {audit.NewID(), "operator", nil, nil, "{}"},
		"the system naming a credential": {audit.NewID(), "system", "oper_X", nil, "{}"},
		"an unknown kind":                {audit.NewID(), "root", "oper_X", nil, "{}"},
		"a reason that says nothing":     {audit.NewID(), "operator", "oper_X", "", "{}"},
		"details that are not an object": {audit.NewID(), "operator", "oper_X", nil, "[]"},
		"details that are a payload":     {audit.NewID(), "operator", "oper_X", nil, `{"body":"` + strings.Repeat("x", 4096) + `"}`},
		"an identifier of another kind":  {"room_7KQZP4XN2VJH6TBWMDR3YAFC5E", "operator", "oper_X", nil, "{}"},
	}

	for name, arguments := range cases {
		if _, err := f.pool.Exec(context.Background(), insert, arguments...); err == nil {
			t.Errorf("%s was stored", name)
		}
	}

	if _, err := f.pool.Exec(context.Background(), insert, audit.NewID(), "system", nil, nil, "{}"); err != nil {
		t.Errorf("a valid row was refused: %v", err)
	}
}
