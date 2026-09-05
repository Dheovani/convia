package users

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
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"convia/internal/applications"
	"convia/internal/config"
	"convia/internal/database"
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
	service *Service
	pool    *pgxpool.Pool
	first   string
	second  string
	logs    *bytes.Buffer
}

func newFixture(t *testing.T) fixture {
	t.Helper()

	maintenanceURL := strings.TrimSpace(os.Getenv(testDatabaseURLEnvironment))
	if maintenanceURL == "" {
		t.Skipf("set %s to run the user integration tests", testDatabaseURLEnvironment)
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
	first := newApplication(t, applicationService, "First Tenant")
	second := newApplication(t, applicationService, "Second Tenant")

	logs.Reset()
	return fixture{
		service: NewService(NewStore(pool), applicationService, logger),
		pool:    pool,
		first:   first,
		second:  second,
		logs:    logs,
	}
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

func TestResolveCreatesThenReturnsTheSameUser(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	created, isNew, err := setup.service.Resolve(ctx, setup.first, Identity{
		ExternalSubject: "  customer-42  ",
		DisplayName:     "  Ada Lovelace  ",
		Metadata:        map[string]string{"plan": "pro"},
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if !isNew {
		t.Error("the first resolve reported an existing user")
	}
	if created.ExternalSubject != "customer-42" || created.DisplayName != "Ada Lovelace" {
		t.Errorf("stored %+v, want the trimmed values", created)
	}
	if created.Status != StatusActive {
		t.Errorf("Status = %q, want %q", created.Status, StatusActive)
	}

	again, isNew, err := setup.service.Resolve(ctx, setup.first, Identity{ExternalSubject: "customer-42"})
	if err != nil {
		t.Fatalf("Resolve() repeated error = %v", err)
	}
	if isNew {
		t.Error("the second resolve reported a new user")
	}
	if again.ID != created.ID {
		t.Errorf("ID = %q, want the original %q", again.ID, created.ID)
	}

	// A resolve must not overwrite what an operator corrected elsewhere.
	if again.DisplayName != "Ada Lovelace" || again.Metadata["plan"] != "pro" {
		t.Errorf("resolve changed the stored user: %+v", again)
	}
}

/*
TestResolveIsSafeUnderConcurrency proves the unique index and the single
resolving statement cooperate: many simultaneous callers see one user.
*/
func TestResolveIsSafeUnderConcurrency(t *testing.T) {
	setup := newFixture(t)

	const callers = 12
	identifiers := make([]string, callers)
	creations := make([]bool, callers)
	failures := make([]error, callers)

	var start sync.WaitGroup
	var finished sync.WaitGroup
	start.Add(1)

	for index := range callers {
		finished.Add(1)
		go func() {
			defer finished.Done()
			start.Wait()

			user, isNew, err := setup.service.Resolve(context.Background(), setup.first,
				Identity{ExternalSubject: "contended-subject"})
			identifiers[index] = user.ID
			creations[index] = isNew
			failures[index] = err
		}()
	}

	start.Done()
	finished.Wait()

	created := 0
	for index := range callers {
		if failures[index] != nil {
			t.Fatalf("caller %d error = %v", index, failures[index])
		}
		if identifiers[index] != identifiers[0] {
			t.Errorf("caller %d resolved %q, want the same user as %q", index, identifiers[index], identifiers[0])
		}
		if creations[index] {
			created++
		}
	}

	if created != 1 {
		t.Errorf("%d callers reported creating the user, want exactly 1", created)
	}
}

/*
TestResolveReadsAUserCommittedWhileItWaited covers the race that the shuffled
concurrency test above only finds by luck.

A resolve that conflicts with an uncommitted user waits for that transaction to
finish, but PostgreSQL evaluates its lookup against the snapshot taken before
that wait began, so the winner's row is invisible to it and the statement
returns nothing. Resolving has to notice that and read again, rather than
report a user that plainly exists as missing.
*/
func TestResolveReadsAUserCommittedWhileItWaited(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	const subject = "contended-subject"
	winner := NewID()

	holder, err := setup.pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin the holding transaction: %v", err)
	}
	defer func() { _ = holder.Rollback(ctx) }()

	moment := time.Now().UTC().Truncate(time.Microsecond)
	if _, err := holder.Exec(ctx, `INSERT INTO users (`+columns+`) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		winner, setup.first, subject, nil, map[string]string{}, StatusActive, moment, moment); err != nil {
		t.Fatalf("insert the uncommitted user: %v", err)
	}

	type outcome struct {
		user  User
		isNew bool
		err   error
	}

	/*
		The resolve is cancellable so that a failure to reach the blocked state
		releases its pooled connection: closing the pool waits for it, and the
		fixture cannot drop its database until then.
	*/
	resolving, abandon := context.WithCancel(ctx)
	defer abandon()

	resolved := make(chan outcome, 1)
	go func() {
		user, isNew, err := setup.service.Resolve(resolving, setup.first, Identity{ExternalSubject: subject})
		resolved <- outcome{user: user, isNew: isNew, err: err}
	}()

	waitForBlockedStatement(t, setup.pool)

	if err := holder.Commit(ctx); err != nil {
		t.Fatalf("commit the holding transaction: %v", err)
	}

	result := <-resolved
	if result.err != nil {
		t.Fatalf("Resolve() error = %v, want the user committed during the wait", result.err)
	}
	if result.isNew {
		t.Error("the losing resolve reported creating the user")
	}
	if result.user.ID != winner {
		t.Errorf("ID = %q, want the committed %q", result.user.ID, winner)
	}
}

/*
waitForBlockedStatement waits until a statement in the test database is waiting
on a lock.

That is the point where the resolve under test is stalled on the uncommitted
row and its snapshot is already older than the commit that follows. Failing
here rather than carrying on keeps the test from passing without ever having
created the race it exists to cover.
*/
func waitForBlockedStatement(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()

	const statement = `SELECT count(*) FROM pg_stat_activity
	                   WHERE datname = current_database() AND wait_event_type = 'Lock'`

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var blocked int
		if err := pool.QueryRow(context.Background(), statement).Scan(&blocked); err != nil {
			t.Fatalf("read pg_stat_activity: %v", err)
		}
		if blocked > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}

	t.Fatal("no statement ever waited on a lock, so the race was never created")
}

/*
TestUsersAreIsolatedPerApplication is the guarantee M06 exists for: the same
external subject in two applications is two unrelated users, and neither
application can reach the other's.
*/
func TestUsersAreIsolatedPerApplication(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	inFirst, _, err := setup.service.Resolve(ctx, setup.first, Identity{ExternalSubject: "shared-subject"})
	if err != nil {
		t.Fatalf("Resolve() in the first application error = %v", err)
	}
	inSecond, isNew, err := setup.service.Resolve(ctx, setup.second, Identity{ExternalSubject: "shared-subject"})
	if err != nil {
		t.Fatalf("Resolve() in the second application error = %v", err)
	}

	if !isNew {
		t.Error("the second application resolved to an existing user")
	}
	if inFirst.ID == inSecond.ID {
		t.Fatalf("both applications resolved to %q, want unrelated users", inFirst.ID)
	}

	// Reading another application's user is reported as missing, not refused,
	// so the answer reveals nothing about whether it exists.
	if _, err := setup.service.Get(ctx, setup.second, inFirst.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get() across applications error = %v, want ErrNotFound", err)
	}
	if _, err := setup.service.Get(ctx, setup.first, inSecond.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get() across applications error = %v, want ErrNotFound", err)
	}
}

// A listing never leaks another application's users.
func TestListIsScopedToOneApplication(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	for _, subject := range []string{"a", "b", "c"} {
		if _, _, err := setup.service.Resolve(ctx, setup.first, Identity{ExternalSubject: subject}); err != nil {
			t.Fatalf("Resolve() error = %v", err)
		}
	}
	if _, _, err := setup.service.Resolve(ctx, setup.second, Identity{ExternalSubject: "outsider"}); err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}

	page, err := setup.service.List(ctx, setup.first, ListOptions{})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(page.Users) != 3 {
		t.Fatalf("List() returned %d users, want 3", len(page.Users))
	}
	for _, user := range page.Users {
		if user.ApplicationID != setup.first {
			t.Errorf("user %q belongs to %q, want %q", user.ID, user.ApplicationID, setup.first)
		}
	}
}

func TestListPagesNewestFirst(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	var created []string
	for _, subject := range []string{"one", "two", "three", "four", "five"} {
		user, _, err := setup.service.Resolve(ctx, setup.first, Identity{ExternalSubject: subject})
		if err != nil {
			t.Fatalf("Resolve() error = %v", err)
		}
		created = append(created, user.ID)
	}

	var seen []string
	options := ListOptions{Limit: 2}
	for pages := 0; ; pages++ {
		if pages > 5 {
			t.Fatal("paging did not terminate")
		}

		page, err := setup.service.List(ctx, setup.first, options)
		if err != nil {
			t.Fatalf("List() error = %v", err)
		}
		for _, user := range page.Users {
			seen = append(seen, user.ID)
		}
		if page.NextCursor == "" {
			break
		}
		options.Cursor = page.NextCursor
	}

	if len(seen) != len(created) {
		t.Fatalf("paging returned %d users, want %d", len(seen), len(created))
	}
	for index, id := range seen {
		if expected := created[len(created)-1-index]; id != expected {
			t.Errorf("position %d = %q, want %q (newest first)", index, id, expected)
		}
	}
}

// Work is refused for an application Convia does not serve.
func TestOperationsRequireAKnownApplication(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	unknown := "app_AAAAAAAAAAAAAAAAAAAAAAAAAA"

	if _, _, err := setup.service.Resolve(ctx, unknown, Identity{ExternalSubject: "customer-42"}); !errors.Is(err, ErrApplicationNotFound) {
		t.Errorf("Resolve() error = %v, want ErrApplicationNotFound", err)
	}
	if _, err := setup.service.Get(ctx, unknown, NewID()); !errors.Is(err, ErrApplicationNotFound) {
		t.Errorf("Get() error = %v, want ErrApplicationNotFound", err)
	}
	if _, err := setup.service.List(ctx, unknown, ListOptions{}); !errors.Is(err, ErrApplicationNotFound) {
		t.Errorf("List() error = %v, want ErrApplicationNotFound", err)
	}
}

func TestResolveRejectsInvalidIdentities(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	rejected := map[string]Identity{
		"no subject":       {ExternalSubject: "  "},
		"long subject":     {ExternalSubject: strings.Repeat("a", 256)},
		"long name":        {ExternalSubject: "customer-42", DisplayName: strings.Repeat("a", 121)},
		"invalid metadata": {ExternalSubject: "customer-42", Metadata: map[string]string{"Plan": "pro"}},
	}

	for name, identity := range rejected {
		t.Run(name, func(t *testing.T) {
			var validation ValidationError
			if _, _, err := setup.service.Resolve(ctx, setup.first, identity); !errors.As(err, &validation) {
				t.Errorf("Resolve() error = %v, want a validation error", err)
			}
		})
	}
}

/*
TestAuditRecordsCreationWithoutTheSubject proves identity changes are audited
and that the audit record does not carry application-owned personal data.
*/
func TestAuditRecordsCreationWithoutTheSubject(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	created, _, err := setup.service.Resolve(ctx, setup.first,
		Identity{ExternalSubject: "ada@example.test", DisplayName: "Ada Lovelace"})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if _, _, err := setup.service.Resolve(ctx, setup.first, Identity{ExternalSubject: "ada@example.test"}); err != nil {
		t.Fatalf("Resolve() repeated error = %v", err)
	}

	creations := 0
	for _, line := range strings.Split(strings.TrimSpace(setup.logs.String()), "\n") {
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}
		if entry["event"] == "user.created" && entry["user_id"] == created.ID {
			creations++
		}
	}

	if creations != 1 {
		t.Errorf("audit recorded %d creations, want exactly 1", creations)
	}
	if strings.Contains(setup.logs.String(), "ada@example.test") {
		t.Error("the audit log contains the external subject, which is application-owned personal data")
	}
	if strings.Contains(setup.logs.String(), "Ada Lovelace") {
		t.Error("the audit log contains the display name, which is application-owned personal data")
	}
}
