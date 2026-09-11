package accounts

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

	"convia/internal/applications"
	"convia/internal/config"
	"convia/internal/database"
	"convia/internal/users"
)

/*
testDatabaseURLEnvironment points these tests at a PostgreSQL instance.

They are skipped when it is unset, so `go test ./...` stays runnable without
infrastructure. Each test gets a database of its own, so nothing shares state.
*/
const testDatabaseURLEnvironment = "CONVIA_TEST_DATABASE_URL"

// fixture is the account service over a real database.
type fixture struct {
	service     *Service
	store       *Store
	pool        *pgxpool.Pool
	application string
	logs        *bytes.Buffer
}

func newFixture(t *testing.T) fixture {
	t.Helper()

	maintenanceURL := strings.TrimSpace(os.Getenv(testDatabaseURLEnvironment))
	if maintenanceURL == "" {
		t.Skipf("set %s to run the account integration tests", testDatabaseURLEnvironment)
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
	application, err := applicationService.Create(context.Background(), "Convia")
	if err != nil {
		t.Fatalf("create the first-party application: %v", err)
	}

	userService := users.NewService(users.NewStore(pool), applicationService, logger)
	store := NewStore(pool)

	logs.Reset()
	return fixture{
		service:     NewService(store, userService, application.ID, logger),
		store:       store,
		pool:        pool,
		application: application.ID,
		logs:        logs,
	}
}

func execute(t *testing.T, databaseURL, statement string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	connection, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() {
		if err := connection.Close(ctx); err != nil {
			t.Errorf("close: %v", err)
		}
	}()

	if _, err := connection.Exec(ctx, statement); err != nil {
		t.Fatalf("execute %q: %v", statement, err)
	}
}

/*
TestCreatingAnAccountMakesTheUserItPointsAt is the link that keeps an account
from being a second notion of who somebody is.

Everything else in Convia addresses a person as a users row. If creating an
account did not resolve one, every person-facing route would need a second path
for people who signed in rather than being asserted.
*/
func TestCreatingAnAccountMakesTheUserItPointsAt(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	account, password, err := setup.service.Create(ctx, Registration{
		Email: "Ana@Example.com", DisplayName: "Ana Ribeiro"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	if account.Email != "ana@example.com" {
		t.Errorf("the stored address is %q, want it lowercased", account.Email)
	}
	if account.UserID == "" {
		t.Fatal("the account points at no user")
	}
	if password == "" {
		t.Fatal("no password was generated")
	}

	/*
		The external subject is the account identifier rather than the email.
		A deleted subject stays reserved forever, so using the address would
		make it unusable by the person who owned it.
	*/
	person, err := users.NewService(users.NewStore(setup.pool),
		applications.NewService(applications.NewStore(setup.pool), slog.New(slog.NewJSONHandler(setup.logs, nil))),
		slog.New(slog.NewJSONHandler(setup.logs, nil))).Get(ctx, setup.application, account.UserID)
	if err != nil {
		t.Fatalf("read the account's user: %v", err)
	}
	if person.ExternalSubject != account.ID {
		t.Errorf("the user's external subject is %q, want the account identifier %q",
			person.ExternalSubject, account.ID)
	}
}

/*
TestAnAddressIdentifiesOneAccount is the unique index doing its job, reported
as a domain outcome rather than a driver error.
*/
func TestAnAddressIdentifiesOneAccount(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	if _, _, err := setup.service.Create(ctx, Registration{
		Email: "ana@example.com", DisplayName: "Ana"}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	_, _, err := setup.service.Create(ctx, Registration{
		Email: "ANA@example.com", DisplayName: "Somebody Else"})
	if !errors.Is(err, ErrEmailTaken) {
		t.Errorf("Create() with a taken address error = %v, want %v", err, ErrEmailTaken)
	}
}

/*
TestTheDigestNeverLeavesTheStoreExceptToBeCompared is why reading credentials
is a statement of its own.

Every other read projects a column list that does not include the digest, so a
digest cannot reach a response by somebody forgetting to exclude a field.
*/
func TestTheDigestNeverLeavesTheStoreExceptToBeCompared(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	account, password, err := setup.service.Create(ctx, Registration{
		Email: "ana@example.com", DisplayName: "Ana"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	read, err := setup.store.Get(ctx, account.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if read.ID != account.ID || read.Email != account.Email {
		t.Errorf("Get() returned %+v", read)
	}

	_, digest, err := setup.store.Credentials(ctx, account.Email)
	if err != nil {
		t.Fatalf("Credentials() error = %v", err)
	}

	matches, err := Verify(digest, password)
	if err != nil || !matches {
		t.Errorf("the stored digest does not verify the generated password: %v, %v", matches, err)
	}
}

/*
TestEveryRefusalIsTheSameRefusal walks each way signing in can fail and checks
they are indistinguishable.

The ordering matters as much as the answer: the password is verified before the
lifecycle, so a suspended account cannot be discovered without one.
*/
func TestEveryRefusalIsTheSameRefusal(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	account, password, err := setup.service.Create(ctx, Registration{
		Email: "ana@example.com", DisplayName: "Ana"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	if _, err := setup.service.Authenticate(ctx, "nobody@example.com", password); !errors.Is(err, ErrUnauthenticated) {
		t.Errorf("an unknown address error = %v, want %v", err, ErrUnauthenticated)
	}
	if _, err := setup.service.Authenticate(ctx, "not an address", password); !errors.Is(err, ErrUnauthenticated) {
		t.Errorf("a malformed address error = %v, want %v", err, ErrUnauthenticated)
	}
	if _, err := setup.service.Authenticate(ctx, account.Email, "the wrong one"); !errors.Is(err, ErrUnauthenticated) {
		t.Errorf("a wrong password error = %v, want %v", err, ErrUnauthenticated)
	}

	if _, err := setup.service.Authenticate(ctx, account.Email, password); err != nil {
		t.Fatalf("the right password does not work: %v", err)
	}

	if _, err := setup.service.Suspend(ctx, account.ID); err != nil {
		t.Fatalf("Suspend() error = %v", err)
	}
	if _, err := setup.service.Authenticate(ctx, account.Email, password); !errors.Is(err, ErrUnauthenticated) {
		t.Errorf("a suspended account error = %v, want %v", err, ErrUnauthenticated)
	}

	if _, err := setup.service.Activate(ctx, account.ID); err != nil {
		t.Fatalf("Activate() error = %v", err)
	}
	if _, err := setup.service.Authenticate(ctx, account.Email, password); err != nil {
		t.Errorf("activating did not restore signing in: %v", err)
	}
}

/*
TestChangingAPasswordNeedsTheCurrentOne keeps a stolen session from being
enough to lock somebody out of their own account.
*/
func TestChangingAPasswordNeedsTheCurrentOne(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	account, password, err := setup.service.Create(ctx, Registration{
		Email: "ana@example.com", DisplayName: "Ana"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	if err := setup.service.ChangePassword(ctx, account.ID, "not the current one",
		"a replacement password"); !errors.Is(err, ErrUnauthenticated) {
		t.Errorf("ChangePassword() without the current one error = %v, want %v", err, ErrUnauthenticated)
	}

	if err := setup.service.ChangePassword(ctx, account.ID, password, "short"); err == nil {
		t.Error("ChangePassword() accepted a password below the floor")
	}

	if err := setup.service.ChangePassword(ctx, account.ID, password, "a replacement password"); err != nil {
		t.Fatalf("ChangePassword() error = %v", err)
	}

	if _, err := setup.service.Authenticate(ctx, account.Email, password); !errors.Is(err, ErrUnauthenticated) {
		t.Error("the old password still signs in")
	}
	if _, err := setup.service.Authenticate(ctx, account.Email, "a replacement password"); err != nil {
		t.Errorf("the new password does not sign in: %v", err)
	}
}

/*
TestNothingSecretReachesTheAuditLog is asserted against the real log a running
instance writes, rather than against intent.
*/
func TestNothingSecretReachesTheAuditLog(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	account, password, err := setup.service.Create(ctx, Registration{
		Email: "ana@example.com", DisplayName: "Ana"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if _, err := setup.service.Authenticate(ctx, account.Email, password); err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	if _, err := setup.service.Authenticate(ctx, account.Email, "wrong"); err == nil {
		t.Fatal("a wrong password was accepted")
	}

	written := setup.logs.String()
	for _, secret := range []string{string(password), "ana@example.com", "argon2id"} {
		if strings.Contains(written, secret) {
			t.Errorf("the log carries %q:\n%s", secret, written)
		}
	}

	if !strings.Contains(written, account.ID) {
		t.Errorf("the log does not name the account, so an operator cannot trace it:\n%s", written)
	}
	if !strings.Contains(written, "account.refused") {
		t.Errorf("a refused sign-in was not recorded:\n%s", written)
	}
}
