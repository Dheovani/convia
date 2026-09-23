package departure

import (
	"context"
	"crypto/rand"
	"errors"
	"io"
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
	"convia/internal/events"
	"convia/internal/events/serving"
	"convia/internal/messages"
	"convia/internal/peers"
	"convia/internal/rooms"
	"convia/internal/sessions"
	"convia/internal/users"
	"convia/internal/webhooks"
)

const testDatabaseURLEnvironment = "CONVIA_TEST_DATABASE_URL"

const password = accounts.Password("correct horse battery staple")

type fixture struct {
	service  *Service
	sessions *sessions.Service
	accounts *accounts.Service
	rooms    *rooms.Service
	messages *messages.Service
	pool     *pgxpool.Pool
}

func newFixture(t *testing.T) fixture {
	t.Helper()

	maintenanceURL := strings.TrimSpace(os.Getenv(testDatabaseURLEnvironment))
	if maintenanceURL == "" {
		t.Skipf("set %s to run the departure integration tests", testDatabaseURLEnvironment)
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

	applicationService := applications.NewService(applications.NewStore(pool), logger)
	if err := applicationService.EnsureFirstParty(ctx); err != nil {
		t.Fatalf("EnsureFirstParty() error = %v", err)
	}
	announcer := serving.NewAnnouncer(events.NewBroker(), nil, logger)
	userService := users.NewService(users.NewStore(pool), applicationService, logger)
	roomService := rooms.NewService(rooms.NewStore(pool), applicationService, userService, announcer, logger)
	// Nobody here arrived by invitation, which is all the message service asks invitations about.
	messageService := messages.NewService(messages.NewStore(pool), applicationService, roomService, userService,
		nil, announcer, logger)
	accountService := accounts.NewService(accounts.NewStore(pool), userService, applications.FirstPartyID, logger)
	sessionService := sessions.NewService(sessions.NewStore(pool), accountService, applicationService,
		userService, applications.FirstPartyID, logger)
	peerService := peers.NewService(peers.NewStore(pool), roomService, userService, applicationService,
		accountService, peers.NewClient(webhooks.NewDestinations(true)), applications.FirstPartyID, logger)

	return fixture{
		service:  NewService(accountService, peerService, roomService, messageService, userService, logger),
		sessions: sessionService,
		accounts: accountService,
		rooms:    roomService,
		messages: messageService,
		pool:     pool,
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
	defer func() { _ = connection.Close(ctx) }()

	if _, err := connection.Exec(ctx, statement); err != nil {
		t.Fatalf("execute %q: %v", statement, err)
	}
}

// signedIn registers somebody and returns what their browser would hold.
func (setup fixture) signedIn(t *testing.T, username string) (sessions.Principal, accounts.Identity, string) {
	t.Helper()
	ctx := context.Background()

	_, token, err := setup.sessions.Register(ctx, username, password)
	if err != nil {
		t.Fatalf("register %s: %v", username, err)
	}
	principal, err := setup.sessions.Authenticate(ctx, token)
	if err != nil {
		t.Fatalf("authenticate %s: %v", username, err)
	}
	identity, err := setup.sessions.Identity(ctx, token)
	if err != nil {
		t.Fatalf("open %s's key: %v", username, err)
	}
	return principal, identity, token
}

/*
TestDeletingAnAccountTakesEverythingWithIt is M18-031 end to end: a wrong
password changes nothing, and the right one leaves the rooms, redacts what was
said, retires the person, frees the username and ends the session.
*/
func TestDeletingAnAccountTakesEverythingWithIt(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	ana, identity, token := setup.signedIn(t, "ana")
	bruno, _, _ := setup.signedIn(t, "bruno")

	room, err := setup.rooms.CreateFor(ctx, ana.ApplicationID, ana.UserID, rooms.Definition{Name: "Standup"})
	if err != nil {
		t.Fatalf("open a room: %v", err)
	}
	if _, _, err := setup.rooms.AddMember(ctx, ana.ApplicationID, room.ID, bruno.UserID); err != nil {
		t.Fatalf("add bruno: %v", err)
	}
	said, err := messages.AsPerson(setup.messages, setup.rooms, ana).Post(ctx, room.ID, "Something to redact.")
	if err != nil {
		t.Fatalf("Post() error = %v", err)
	}

	if err := setup.service.Delete(ctx, ana, identity, "a guess at the password"); !errors.Is(err, accounts.ErrWrongPassword) {
		t.Fatalf("Delete() with a wrong password error = %v, want %v", err, accounts.ErrWrongPassword)
	}
	if err := setup.service.Delete(ctx, ana, identity, ""); !errors.Is(err, ErrPasswordMissing) {
		t.Fatalf("Delete() without a password error = %v, want %v", err, ErrPasswordMissing)
	}
	if _, err := setup.sessions.Authenticate(ctx, token); err != nil {
		t.Fatalf("a refused deletion ended the session: %v", err)
	}
	if in, _ := setup.rooms.IsMember(ctx, ana.ApplicationID, room.ID, ana.UserID); !in {
		t.Fatal("a refused deletion took ana out of the room")
	}

	if err := setup.service.Delete(ctx, ana, identity, password); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}

	if _, err := setup.sessions.Authenticate(ctx, token); !errors.Is(err, sessions.ErrUnauthenticated) {
		t.Errorf("the session after deletion error = %v, want %v", err, sessions.ErrUnauthenticated)
	}
	if _, err := setup.accounts.Get(ctx, ana.AccountID); !errors.Is(err, accounts.ErrNotFound) {
		t.Errorf("the account after deletion error = %v, want %v", err, accounts.ErrNotFound)
	}
	if _, _, err := setup.sessions.Register(ctx, "ana", "a different long password"); err != nil {
		t.Errorf("registering the freed username error = %v", err)
	}

	stored, err := setup.rooms.Get(ctx, ana.ApplicationID, room.ID)
	if err != nil || stored.OwnerUserID != bruno.UserID {
		t.Errorf("the room = owned by %q, %v, want bruno %q", stored.OwnerUserID, err, bruno.UserID)
	}

	var body, author *string
	if err := setup.pool.QueryRow(ctx, `SELECT body, author_user_id FROM messages WHERE id = $1`,
		said.ID).Scan(&body, &author); err != nil {
		t.Fatalf("read the message: %v", err)
	}
	if body != nil || author != nil {
		t.Errorf("the message kept body %v and author %v, want both gone", body, author)
	}

	var status string
	var name *string
	if err := setup.pool.QueryRow(ctx, `SELECT status, display_name FROM users WHERE id = $1`,
		ana.UserID).Scan(&status, &name); err != nil {
		t.Fatalf("read the user: %v", err)
	}
	if status != string(users.StatusDeleted) || name != nil {
		t.Errorf("the user = %s named %v, want deleted with no name", status, name)
	}
}
