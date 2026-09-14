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
	"sync"
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

const samplePassword Password = "correct horse battery staple"

// fixture is the account service over a real database.
type fixture struct {
	service     *Service
	store       *Store
	users       *users.Service
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
	if err := applicationService.EnsureFirstParty(context.Background()); err != nil {
		t.Fatalf("create the first-party application: %v", err)
	}

	userService := users.NewService(users.NewStore(pool), applicationService, logger)
	store := NewStore(pool)

	logs.Reset()
	return fixture{
		service:     NewService(store, userService, applications.FirstPartyID, logger),
		store:       store,
		users:       userService,
		pool:        pool,
		application: applications.FirstPartyID,
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
TestRegisteringMakesTheUserItPointsAt is the link that keeps an account from
being a second notion of who somebody is.

Everything else in Convia addresses a person as a users row. If registering did
not resolve one, every person-facing route would need a second path for people
who signed in rather than being asserted.
*/
func TestRegisteringMakesTheUserItPointsAt(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	account, _, err := setup.service.Register(ctx, "Ana", samplePassword)
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	if account.Username != "ana" {
		t.Errorf("the stored username is %q, want it lowercased", account.Username)
	}
	if account.ID != IDFor(account.PublicKey) {
		t.Errorf("the identifier %q is not the fingerprint of the stored key", account.ID)
	}

	/*
		The external subject is the account identifier rather than the username.
		A deleted subject stays reserved forever, and a username is something a
		person chose and may want back.
	*/
	person, err := setup.users.Get(ctx, setup.application, account.UserID)
	if err != nil {
		t.Fatalf("read the account's user: %v", err)
	}
	if person.ExternalSubject != account.ID {
		t.Errorf("the user's external subject is %q, want the account identifier %q",
			person.ExternalSubject, account.ID)
	}
	if person.DisplayName != "ana" {
		t.Errorf("the user's display name is %v, want the username, which is what a roster shows", person.DisplayName)
	}

	read, err := setup.store.Get(ctx, account.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if read.ID != account.ID || read.Username != "ana" || !read.PublicKey.Equal(account.PublicKey) {
		t.Errorf("Get() returned %+v", read)
	}
}

/*
TestAUsernameNamesOneAccount is the check and the unique index both doing their
job, and reported as a domain outcome rather than a driver error.

The concurrent half is the race the early check cannot see: two people choosing
one name at the same moment both find it free, and the index decides.
*/
func TestAUsernameNamesOneAccount(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	if _, _, err := setup.service.Register(ctx, "ana", samplePassword); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if _, _, err := setup.service.Register(ctx, "ANA", "a different password"); !errors.Is(err, ErrUsernameTaken) {
		t.Errorf("Register() with a taken name error = %v, want %v", err, ErrUsernameTaken)
	}

	const racers = 4
	var (
		wait      sync.WaitGroup
		mutex     sync.Mutex
		succeeded int
	)
	for range racers {
		wait.Go(func() {
			_, _, err := setup.service.Register(ctx, "bruno", samplePassword)
			mutex.Lock()
			defer mutex.Unlock()
			switch {
			case err == nil:
				succeeded++
			case !errors.Is(err, ErrUsernameTaken):
				t.Errorf("a racing Register() error = %v, want nil or %v", err, ErrUsernameTaken)
			}
		})
	}
	wait.Wait()

	if succeeded != 1 {
		t.Errorf("%d people registered one username at once, want exactly 1", succeeded)
	}
}

func TestRegisteringChecksWhatItIsGiven(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	var validation ValidationError
	if _, _, err := setup.service.Register(ctx, "a b", samplePassword); !errors.As(err, &validation) ||
		validation.Field != "username" {
		t.Errorf("an unusable username error = %v, want a validation error about the username", err)
	}
	if _, _, err := setup.service.Register(ctx, "ana", "short"); !errors.As(err, &validation) ||
		validation.Field != "password" {
		t.Errorf("a short password error = %v, want a validation error about the password", err)
	}
}

/*
TestTheKeyOpensOnlyWithThePassword is the password-manager property, against
what is actually stored: the database holds a sealed key that the registering
password opens, and nothing else does.
*/
func TestTheKeyOpensOnlyWithThePassword(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	account, _, err := setup.service.Register(ctx, "ana", samplePassword)
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	_, digest, sealed, err := setup.store.Credentials(ctx, "ana")
	if err != nil {
		t.Fatalf("Credentials() error = %v", err)
	}

	if matches, err := Verify(digest, samplePassword); err != nil || !matches {
		t.Errorf("the stored digest does not verify the password: %v, %v", matches, err)
	}
	if _, err := Open(sealed, account.PublicKey, samplePassword); err != nil {
		t.Errorf("the stored key does not open with the password: %v", err)
	}
	if _, err := Open(sealed, account.PublicKey, "not the password"); err == nil {
		t.Error("the stored key opened with a wrong password")
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

	account, _, err := setup.service.Register(ctx, "ana", samplePassword)
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	if _, _, err := setup.service.Authenticate(ctx, "nobody", samplePassword); !errors.Is(err, ErrUnauthenticated) {
		t.Errorf("an unknown username error = %v, want %v", err, ErrUnauthenticated)
	}
	if _, _, err := setup.service.Authenticate(ctx, "not a username", samplePassword); !errors.Is(err, ErrUnauthenticated) {
		t.Errorf("a malformed username error = %v, want %v", err, ErrUnauthenticated)
	}
	if _, _, err := setup.service.Authenticate(ctx, "ana", "the wrong one"); !errors.Is(err, ErrUnauthenticated) {
		t.Errorf("a wrong password error = %v, want %v", err, ErrUnauthenticated)
	}

	if _, _, err := setup.service.Authenticate(ctx, "Ana", samplePassword); err != nil {
		t.Fatalf("the right password does not work: %v", err)
	}

	if _, err := setup.service.Suspend(ctx, account.ID); err != nil {
		t.Fatalf("Suspend() error = %v", err)
	}
	if _, _, err := setup.service.Authenticate(ctx, "ana", samplePassword); !errors.Is(err, ErrUnauthenticated) {
		t.Errorf("a suspended account error = %v, want %v", err, ErrUnauthenticated)
	}

	if _, err := setup.service.Activate(ctx, account.ID); err != nil {
		t.Fatalf("Activate() error = %v", err)
	}
	if _, _, err := setup.service.Authenticate(ctx, "ana", samplePassword); err != nil {
		t.Errorf("activating did not restore signing in: %v", err)
	}
}

/*
TestChangingAPasswordNeedsTheCurrentOneAndKeepsTheKey keeps a stolen session
from being enough to lock somebody out, and keeps the identity through the
change: the identifier is the key's fingerprint, so a new key would be a new
person.
*/
func TestChangingAPasswordNeedsTheCurrentOneAndKeepsTheKey(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	account, _, err := setup.service.Register(ctx, "ana", samplePassword)
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	if _, err := setup.service.ChangePassword(ctx, account.ID, "not the current one",
		"a replacement password"); !errors.Is(err, ErrUnauthenticated) {
		t.Errorf("ChangePassword() without the current one error = %v, want %v", err, ErrUnauthenticated)
	}
	if _, err := setup.service.ChangePassword(ctx, account.ID, samplePassword, "short"); err == nil {
		t.Error("ChangePassword() accepted a password below the floor")
	}

	if _, err := setup.service.ChangePassword(ctx, account.ID, samplePassword, "a replacement password"); err != nil {
		t.Fatalf("ChangePassword() error = %v", err)
	}

	if _, _, err := setup.service.Authenticate(ctx, "ana", samplePassword); !errors.Is(err, ErrUnauthenticated) {
		t.Error("the old password still signs in")
	}
	if _, _, err := setup.service.Authenticate(ctx, "ana", "a replacement password"); err != nil {
		t.Errorf("the new password does not sign in: %v", err)
	}

	_, _, sealed, err := setup.store.Credentials(ctx, "ana")
	if err != nil {
		t.Fatalf("Credentials() error = %v", err)
	}
	opened, err := Open(sealed, account.PublicKey, "a replacement password")
	if err != nil {
		t.Fatalf("the key does not open with the new password: %v", err)
	}
	if opened.ID() != account.ID {
		t.Error("changing the password changed the key, and with it who the account is")
	}
	if _, err := Open(sealed, account.PublicKey, samplePassword); err == nil {
		t.Error("the old password still opens the key")
	}
}

/*
TestNothingSecretReachesTheAuditLog is asserted against the real log a running
instance writes, rather than against intent.
*/
func TestNothingSecretReachesTheAuditLog(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	account, _, err := setup.service.Register(ctx, "anaribeiro", samplePassword)
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if _, _, err := setup.service.Authenticate(ctx, "anaribeiro", samplePassword); err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	if _, _, err := setup.service.Authenticate(ctx, "anaribeiro", "wrong"); err == nil {
		t.Fatal("a wrong password was accepted")
	}

	written := setup.logs.String()
	for _, secret := range []string{string(samplePassword), "anaribeiro", "argon2id"} {
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
