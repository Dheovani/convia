package sessions

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

	"convia/internal/accounts"
	"convia/internal/applications"
	"convia/internal/config"
	"convia/internal/database"
	"convia/internal/users"
)

const testDatabaseURLEnvironment = "CONVIA_TEST_DATABASE_URL"

// fixture is the session service over a real database, with one account ready
// to sign in.
type fixture struct {
	service      *Service
	store        *Store
	accounts     *accounts.Service
	applications *applications.Service
	users        *users.Service
	pool         *pgxpool.Pool
	application  string
	account      accounts.Account
	password     accounts.Password
	logs         *bytes.Buffer
}

func newFixture(t *testing.T) fixture {
	t.Helper()

	maintenanceURL := strings.TrimSpace(os.Getenv(testDatabaseURLEnvironment))
	if maintenanceURL == "" {
		t.Skipf("set %s to run the session integration tests", testDatabaseURLEnvironment)
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
	accountService := accounts.NewService(accounts.NewStore(pool), userService, application.ID, logger)

	account, password, err := accountService.Create(context.Background(), accounts.Registration{
		Email: "ana@example.com", DisplayName: "Ana Ribeiro"})
	if err != nil {
		t.Fatalf("create the account: %v", err)
	}

	store := NewStore(pool)
	logs.Reset()

	return fixture{
		service:      NewService(store, accountService, applicationService, userService, application.ID, logger),
		store:        store,
		accounts:     accountService,
		applications: applicationService,
		users:        userService,
		pool:         pool,
		application:  application.ID,
		account:      account,
		password:     password,
		logs:         logs,
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

// signIn opens a session for the fixture's account.
func (setup fixture) signIn(t *testing.T) (Session, string) {
	t.Helper()

	session, token, err := setup.service.Begin(context.Background(), setup.account.Email, setup.password)
	if err != nil {
		t.Fatalf("Begin() error = %v", err)
	}
	return session, token
}

// TestSigningInAndBackOutAgain is the round trip, against a real database.
func TestSigningInAndBackOutAgain(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	session, token := setup.signIn(t)

	principal, err := setup.service.Authenticate(ctx, token)
	if err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	if principal.AccountID != setup.account.ID || principal.UserID != setup.account.UserID {
		t.Errorf("the principal is %+v", principal)
	}
	if principal.ApplicationID != setup.application {
		t.Errorf("the principal names application %q, want %q", principal.ApplicationID, setup.application)
	}
	if principal.SessionID != session.ID {
		t.Errorf("the principal names session %q, want %q", principal.SessionID, session.ID)
	}

	if err := setup.service.End(ctx, session.ID); err != nil {
		t.Fatalf("End() error = %v", err)
	}
	if _, err := setup.service.Authenticate(ctx, token); !errors.Is(err, ErrUnauthenticated) {
		t.Errorf("a revoked session still authenticates: %v", err)
	}
}

/*
TestEveryWayASessionStopsWorkingLooksTheSame walks each of them against a real
row.

A caller learning *which* would learn something about somebody else's account,
so revoked, expired, idle, and never-existed are one answer.
*/
func TestEveryWayASessionStopsWorkingLooksTheSame(t *testing.T) {
	/*
		Each case takes a fixture of its own and breaks the session its own
		way, so that one failure mode cannot be mistaken for another through
		shared state.
	*/
	cases := map[string]func(t *testing.T, setup fixture, session Session) string{
		"a token of the wrong shape": func(*testing.T, fixture, Session) string {
			return "cvk_4XZQP7KN2VJH6TBWMDR3YAFC5E_YH3TKPQ2MWZC7NVJ6BXRD4FGA5"
		},
		"a secret that does not match": func(_ *testing.T, _ fixture, session Session) string {
			return Token(session.ID, NewSecret())
		},
		"a session nobody opened": func(*testing.T, fixture, Session) string {
			return Token(NewID(), NewSecret())
		},
		"revoked": func(t *testing.T, setup fixture, session Session) string {
			if err := setup.store.Revoke(context.Background(), session.ID, time.Now().UTC()); err != nil {
				t.Fatalf("Revoke() error = %v", err)
			}
			return ""
		},
		"past its absolute deadline": func(t *testing.T, setup fixture, session Session) string {
			/*
				Moved wholesale into the past rather than only its deadline: a
				constraint keeps absolute_expires_at after created_at, and it
				is right to — a session that expired before it existed is not a
				state Convia should be able to store.
			*/
			age(t, setup, session.ID, `created_at = now() - interval '200 days',
				last_seen_at = now() - interval '200 days',
				absolute_expires_at = now() - interval '110 days'`)
			return ""
		},
		"idle for too long": func(t *testing.T, setup fixture, session Session) string {
			age(t, setup, session.ID, `created_at = now() - interval '60 days',
				last_seen_at = now() - interval '60 days'`)
			return ""
		},
	}

	for name, breakIt := range cases {
		t.Run(name, func(t *testing.T) {
			setup := newFixture(t)
			session, token := setup.signIn(t)

			if replacement := breakIt(t, setup, session); replacement != "" {
				token = replacement
			}

			if _, err := setup.service.Authenticate(context.Background(), token); !errors.Is(err, ErrUnauthenticated) {
				t.Errorf("Authenticate() error = %v, want %v", err, ErrUnauthenticated)
			}
		})
	}
}

/*
TestSuspensionReachesAnOpenSession is what makes suspension mean anything.

An account suspended, a person suspended, an application suspended: each has to
stop an existing session on its next request, not merely stop new ones.
*/
func TestSuspensionReachesAnOpenSession(t *testing.T) {
	t.Run("the account", func(t *testing.T) {
		setup := newFixture(t)
		ctx := context.Background()
		_, token := setup.signIn(t)

		if _, err := setup.accounts.Suspend(ctx, setup.account.ID); err != nil {
			t.Fatalf("Suspend() error = %v", err)
		}
		if _, err := setup.service.Authenticate(ctx, token); !errors.Is(err, ErrUnauthenticated) {
			t.Errorf("a suspended account kept its session: %v", err)
		}
	})

	t.Run("the person", func(t *testing.T) {
		setup := newFixture(t)
		ctx := context.Background()
		_, token := setup.signIn(t)

		if _, err := setup.users.Suspend(ctx, setup.application, setup.account.UserID); err != nil {
			t.Fatalf("Suspend() error = %v", err)
		}
		if _, err := setup.service.Authenticate(ctx, token); !errors.Is(err, ErrUnauthenticated) {
			t.Errorf("a suspended user kept their session: %v", err)
		}
	})

	t.Run("the first-party application", func(t *testing.T) {
		setup := newFixture(t)
		ctx := context.Background()
		_, token := setup.signIn(t)

		if _, err := setup.applications.Suspend(ctx, setup.application); err != nil {
			t.Fatalf("Suspend() error = %v", err)
		}
		if _, err := setup.service.Authenticate(ctx, token); !errors.Is(err, ErrUnauthenticated) {
			t.Errorf("a suspended tenant kept its sessions: %v", err)
		}
	})
}

/*
TestChangingAPasswordEndsEverythingElseAndRotatesThis is the whole reason the
three operations happen together.
*/
func TestChangingAPasswordEndsEverythingElseAndRotatesThis(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	_, elsewhere := setup.signIn(t)
	_, here := setup.signIn(t)

	principal, err := setup.service.Authenticate(ctx, here)
	if err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}

	rotated, replacement, err := setup.service.ChangePassword(ctx, principal,
		setup.password, "a replacement password")
	if err != nil {
		t.Fatalf("ChangePassword() error = %v", err)
	}

	if rotated.ID == principal.SessionID || replacement == here {
		t.Error("the session doing the change was not rotated, so a leaked token survives the fix")
	}
	if _, err := setup.service.Authenticate(ctx, here); !errors.Is(err, ErrUnauthenticated) {
		t.Error("the old token for this browser still works")
	}
	if _, err := setup.service.Authenticate(ctx, elsewhere); !errors.Is(err, ErrUnauthenticated) {
		t.Error("another device survived the password change")
	}
	if _, err := setup.service.Authenticate(ctx, replacement); err != nil {
		t.Errorf("the rotated session does not work: %v", err)
	}
}

// TestSigningOutEverywhereSparesNobody, including the browser asking.
func TestSigningOutEverywhereSparesNobody(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	_, first := setup.signIn(t)
	_, second := setup.signIn(t)

	ended, err := setup.service.EndAll(ctx, setup.account.ID)
	if err != nil {
		t.Fatalf("EndAll() error = %v", err)
	}
	if ended != 2 {
		t.Errorf("ended %d sessions, want 2", ended)
	}

	for name, token := range map[string]string{"the first": first, "the one asking": second} {
		if _, err := setup.service.Authenticate(ctx, token); !errors.Is(err, ErrUnauthenticated) {
			t.Errorf("%s session survived: %v", name, err)
		}
	}
}

/*
TestOneChannelHoldsOnlySoManySessions keeps somebody who learned a password
from making revocation the owner's problem.
*/
func TestOneChannelHoldsOnlySoManySessions(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	tokens := make([]string, 0, MaxPerAccount+2)
	for range MaxPerAccount + 2 {
		_, token := setup.signIn(t)
		tokens = append(tokens, token)
	}

	live := 0
	for _, token := range tokens {
		if _, err := setup.service.Authenticate(ctx, token); err == nil {
			live++
		}
	}

	if live > MaxPerAccount {
		t.Errorf("%d sessions are live, want at most %d", live, MaxPerAccount)
	}
	if _, err := setup.service.Authenticate(ctx, tokens[len(tokens)-1]); err != nil {
		t.Errorf("the most recent sign-in was evicted: %v", err)
	}
	if _, err := setup.service.Authenticate(ctx, tokens[0]); err == nil {
		t.Error("the least recently used session was not the one evicted")
	}
}

/*
TestTheLastUseTimestampIsThrottledInTheStatement checks that concurrent
requests from one page collapse into at most one write.

The staleness test is in SQL rather than in Go precisely so that a browser
firing six requests at once does not have six of them racing to write the same
value.
*/
func TestTheLastUseTimestampIsThrottledInTheStatement(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	session, _ := setup.signIn(t)
	before, _, err := setup.store.Credentials(ctx, session.ID)
	if err != nil {
		t.Fatalf("Credentials() error = %v", err)
	}

	// A moment later is not worth a write.
	if err := setup.store.Touch(ctx, session.ID, before.LastSeenAt.Add(time.Minute)); err != nil {
		t.Fatalf("Touch() error = %v", err)
	}
	after, _, _ := setup.store.Credentials(ctx, session.ID)
	if !after.LastSeenAt.Equal(before.LastSeenAt) {
		t.Errorf("a minute later was written: %v -> %v", before.LastSeenAt, after.LastSeenAt)
	}

	// An hour later is.
	moment := before.LastSeenAt.Add(RefreshInterval + time.Minute)
	if err := setup.store.Touch(ctx, session.ID, moment); err != nil {
		t.Fatalf("Touch() error = %v", err)
	}
	after, _, _ = setup.store.Credentials(ctx, session.ID)
	if after.LastSeenAt.Equal(before.LastSeenAt) {
		t.Error("an hour later was not written, so the idle window would never advance")
	}
}

/*
TestPruningRemovesWhatStoppedMattering is the half of expiry that deriving
state from timestamps does not give.

"Expiry needs no scheduled job" is true for correctness and false for storage:
one row per sign-in per device accumulates forever otherwise.
*/
func TestPruningRemovesWhatStoppedMattering(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	session, _ := setup.signIn(t)
	live, _ := setup.signIn(t)

	age(t, setup, session.ID, `created_at = now() - interval '300 days',
		last_seen_at = now() - interval '300 days',
		absolute_expires_at = now() - interval '210 days'`)

	removed, err := setup.service.Prune(ctx, 30*24*time.Hour)
	if err != nil {
		t.Fatalf("Prune() error = %v", err)
	}
	if removed != 1 {
		t.Errorf("pruned %d rows, want 1", removed)
	}

	if _, _, err := setup.store.Credentials(ctx, session.ID); !errors.Is(err, ErrNotFound) {
		t.Error("the long-dead session is still stored")
	}
	if _, _, err := setup.store.Credentials(ctx, live.ID); err != nil {
		t.Errorf("pruning removed a live session: %v", err)
	}
}

/*
age edits a session row directly, which is the only way to reach a deadline a
test cannot wait ninety days for.

Every assignment moves the whole row consistently. The table refuses a session
whose absolute deadline precedes its creation, and refuses one last seen before
it existed — so aging one means moving all three together, which is also what
would actually have happened.
*/
func age(t *testing.T, setup fixture, sessionID, assignment string) {
	t.Helper()

	if _, err := setup.pool.Exec(context.Background(),
		"UPDATE sessions SET "+assignment+" WHERE id = $1", sessionID); err != nil {
		t.Fatalf("age the session: %v", err)
	}
}
