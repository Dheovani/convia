package peers

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync"
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
	"convia/internal/rooms"
	"convia/internal/sessions"
	"convia/internal/users"
)

const testDatabaseURLEnvironment = "CONVIA_TEST_DATABASE_URL"

// fakeRelay stands in for another installation, answering by method and target.
type fakeRelay struct {
	mutex   sync.Mutex
	answers map[string]Response
	err     error
}

func (fake *fakeRelay) Do(_ context.Context, _ accounts.Identity, method, _, target string, _ []byte) (Response, error) {
	fake.mutex.Lock()
	defer fake.mutex.Unlock()
	if fake.err != nil {
		return Response{}, fake.err
	}
	return fake.answers[method+" "+target], nil
}

type fixture struct {
	service  *Service
	store    *Store
	users    *users.Service
	rooms    *rooms.Service
	accounts *accounts.Service
	pool     *pgxpool.Pool
	relay    *fakeRelay
	logs     *bytes.Buffer

	ana     accounts.Account
	room    rooms.Room
	inviter sessions.Principal
}

func newFixture(t *testing.T) fixture {
	t.Helper()

	maintenanceURL := strings.TrimSpace(os.Getenv(testDatabaseURLEnvironment))
	if maintenanceURL == "" {
		t.Skipf("set %s to run the peer integration tests", testDatabaseURLEnvironment)
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

	logs := &bytes.Buffer{}
	logger := slog.New(slog.NewJSONHandler(logs, nil))
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
	userService := users.NewService(users.NewStore(pool), applicationService, logger)
	roomService := rooms.NewService(rooms.NewStore(pool), applicationService, userService,
		serving.NewAnnouncer(events.NewBroker(), nil, logger), logger)
	accountService := accounts.NewService(accounts.NewStore(pool), userService, applications.FirstPartyID, logger)

	ana, _, err := accountService.Register(ctx, "ana", "correct horse battery staple")
	if err != nil {
		t.Fatalf("register ana: %v", err)
	}
	room, err := roomService.CreateFor(ctx, applications.FirstPartyID, ana.UserID, rooms.Definition{Name: "Standup"})
	if err != nil {
		t.Fatalf("open a room: %v", err)
	}

	store := NewStore(pool)
	relay := &fakeRelay{answers: map[string]Response{}}
	logs.Reset()

	return fixture{
		service: NewService(store, roomService, userService, applicationService, accountService, relay,
			applications.FirstPartyID, logger),
		store:    store,
		users:    userService,
		rooms:    roomService,
		accounts: accountService,
		pool:     pool,
		relay:    relay,
		logs:     logs,
		ana:      ana,
		room:     room,
		inviter: sessions.Principal{AccountID: ana.ID, UserID: ana.UserID,
			ApplicationID: applications.FirstPartyID},
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

// visitor is somebody from another installation: a key, and a username there.
func visitor(t *testing.T) (accounts.Identity, Signer) {
	t.Helper()
	identity, err := accounts.NewIdentity()
	if err != nil {
		t.Fatalf("NewIdentity() error = %v", err)
	}
	return identity, Signer{AccountID: identity.ID(), PublicKey: identity.Public}
}

/*
TestAnInvitationAdmitsOnlyThePersonItNames is the whole promise of a link that
is not a secret: whoever else holds it, only the key it names gets in, and only
with the name the inviter typed.
*/
func TestAnInvitationAdmitsOnlyThePersonItNames(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	bia, biaSigner := visitor(t)
	_, stranger := visitor(t)

	invitation, err := setup.service.Invite(ctx, setup.inviter, setup.room.ID, accounts.Handle("bia", bia.ID()))
	if err != nil {
		t.Fatalf("Invite() error = %v", err)
	}

	if _, err := setup.service.Preview(ctx, stranger, invitation.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("a stranger previewing error = %v, want %v", err, ErrNotFound)
	}
	if _, err := setup.service.Accept(ctx, stranger, "bia", invitation.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("a stranger accepting error = %v, want %v", err, ErrNotFound)
	}
	if _, err := setup.service.Accept(ctx, biaSigner, "beatriz", invitation.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("the right key with another username error = %v, want %v", err, ErrNotFound)
	}

	preview, err := setup.service.Preview(ctx, biaSigner, invitation.ID)
	if err != nil {
		t.Fatalf("Preview() error = %v", err)
	}
	if preview.RoomName != "Standup" || preview.Inviter != setup.ana.Handle() {
		t.Errorf("the preview is %+v", preview)
	}

	accepted, err := setup.service.Accept(ctx, biaSigner, "BIA", invitation.ID)
	if err != nil {
		t.Fatalf("Accept() error = %v", err)
	}
	if accepted.Local || accepted.RoomID != setup.room.ID {
		t.Errorf("Accept() = %+v", accepted)
	}

	member, err := setup.rooms.IsMember(ctx, applications.FirstPartyID, setup.room.ID, accepted.UserID)
	if err != nil || !member {
		t.Errorf("the visitor is not a member after accepting: %v, %v", member, err)
	}

	principal, err := setup.service.Visit(ctx, biaSigner)
	if err != nil || principal.UserID != accepted.UserID {
		t.Errorf("Visit() = %+v, %v, want the user accepting made", principal, err)
	}

	if _, err := setup.service.Accept(ctx, biaSigner, "bia", invitation.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("accepting a second time error = %v, want %v", err, ErrNotFound)
	}

	// Suspending the visitor at the home stops them on their next request, as
	// suspending somebody who signs in here does.
	if _, err := setup.users.Suspend(ctx, applications.FirstPartyID, accepted.UserID); err != nil {
		t.Fatalf("Suspend() error = %v", err)
	}
	if _, err := setup.service.Visit(ctx, biaSigner); !errors.Is(err, ErrUnauthenticated) {
		t.Errorf("a suspended visitor error = %v, want %v", err, ErrUnauthenticated)
	}
}

// TestOneInvitationAdmitsOnce, even when two acceptances arrive together.
func TestOneInvitationAdmitsOnce(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	bia, biaSigner := visitor(t)
	invitation, err := setup.service.Invite(ctx, setup.inviter, setup.room.ID, accounts.Handle("bia", bia.ID()))
	if err != nil {
		t.Fatalf("Invite() error = %v", err)
	}

	var (
		wait      sync.WaitGroup
		mutex     sync.Mutex
		succeeded int
	)
	for range 4 {
		wait.Go(func() {
			_, err := setup.service.Accept(ctx, biaSigner, "bia", invitation.ID)
			mutex.Lock()
			defer mutex.Unlock()
			if err == nil {
				succeeded++
			} else if !errors.Is(err, ErrNotFound) {
				t.Errorf("a racing Accept() error = %v", err)
			}
		})
	}
	wait.Wait()

	if succeeded != 1 {
		t.Errorf("%d acceptances of one invitation succeeded, want 1", succeeded)
	}
}

/*
TestAClaimSucceedsOnce holds the store to what the concurrent test can only hope
to catch: whatever the service checked a moment earlier, the claim itself refuses
an invitation that was already accepted, withdrawn, or has expired, and one
addressed to anybody else.
*/
func TestAClaimSucceedsOnce(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	bia, _ := visitor(t)
	handle := accounts.Handle("bia", bia.ID())
	at := time.Now().UTC()

	claimed := func(invitation Invitation, accountID, username string, when time.Time) bool {
		t.Helper()
		ok, err := setup.store.ClaimInvitation(ctx, invitation.ID, accountID, username, setup.ana.UserID, when)
		if err != nil {
			t.Fatalf("ClaimInvitation() error = %v", err)
		}
		return ok
	}

	once, err := setup.service.Invite(ctx, setup.inviter, setup.room.ID, handle)
	if err != nil {
		t.Fatalf("Invite() error = %v", err)
	}
	if claimed(once, bia.ID(), "bia", at) != true {
		t.Fatal("the first claim did not succeed")
	}
	if claimed(once, bia.ID(), "bia", at) {
		t.Error("an invitation was claimed a second time")
	}

	other, _ := visitor(t)
	addressed, _ := setup.service.Invite(ctx, setup.inviter, setup.room.ID, handle)
	if claimed(addressed, other.ID(), "bia", at) {
		t.Error("an invitation was claimed by a key it was not addressed to")
	}
	if claimed(addressed, bia.ID(), "beatriz", at) {
		t.Error("an invitation was claimed under a username it did not name")
	}

	withdrawn, _ := setup.service.Invite(ctx, setup.inviter, setup.room.ID, handle)
	if err := setup.service.Revoke(ctx, setup.inviter, withdrawn.ID); err != nil {
		t.Fatalf("Revoke() error = %v", err)
	}
	if claimed(withdrawn, bia.ID(), "bia", at) {
		t.Error("a withdrawn invitation was claimed")
	}

	expired, _ := setup.service.Invite(ctx, setup.inviter, setup.room.ID, handle)
	if claimed(expired, bia.ID(), "bia", at.Add(InvitationLifetime+time.Minute)) {
		t.Error("an expired invitation was claimed")
	}
}

func TestWhoCanInviteWhom(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	bia, _ := visitor(t)
	handle := accounts.Handle("bia", bia.ID())

	var validation ValidationError
	mistyped := handle[:len(handle)-1] + map[bool]string{true: "A", false: "B"}[!strings.HasSuffix(handle, "A")]
	if _, err := setup.service.Invite(ctx, setup.inviter, setup.room.ID, mistyped); !errors.As(err, &validation) {
		t.Errorf("a mistyped handle error = %v, want a validation error", err)
	}

	bruno, _, err := setup.accounts.Register(ctx, "bruno", "another good password")
	if err != nil {
		t.Fatalf("register bruno: %v", err)
	}
	outsider := sessions.Principal{AccountID: bruno.ID, UserID: bruno.UserID, ApplicationID: applications.FirstPartyID}
	if _, err := setup.service.Invite(ctx, outsider, setup.room.ID, handle); !errors.Is(err, ErrRoomNotFound) {
		t.Errorf("inviting into a room one is not in error = %v, want %v", err, ErrRoomNotFound)
	}

	if _, err := setup.service.Invite(ctx, setup.inviter, setup.room.ID, setup.ana.Handle()); !errors.Is(err, ErrAlreadyMember) {
		t.Errorf("inviting somebody already in the room error = %v, want %v", err, ErrAlreadyMember)
	}
}

func TestAnExpiredOrWithdrawnInvitationCannotBeUsed(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	bia, biaSigner := visitor(t)
	handle := accounts.Handle("bia", bia.ID())

	withdrawn, err := setup.service.Invite(ctx, setup.inviter, setup.room.ID, handle)
	if err != nil {
		t.Fatalf("Invite() error = %v", err)
	}
	if err := setup.service.Revoke(ctx, setup.inviter, withdrawn.ID); err != nil {
		t.Fatalf("Revoke() error = %v", err)
	}
	if err := setup.service.Revoke(ctx, setup.inviter, withdrawn.ID); err != nil {
		t.Errorf("revoking twice error = %v, want it to succeed again", err)
	}
	if _, err := setup.service.Accept(ctx, biaSigner, "bia", withdrawn.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("accepting a withdrawn invitation error = %v, want %v", err, ErrNotFound)
	}

	expiring, err := setup.service.Invite(ctx, setup.inviter, setup.room.ID, handle)
	if err != nil {
		t.Fatalf("Invite() error = %v", err)
	}
	later := time.Now().UTC().Add(InvitationLifetime + time.Minute)
	setup.service.now = func() time.Time { return later }
	if _, err := setup.service.Accept(ctx, biaSigner, "bia", expiring.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("accepting after a day error = %v, want %v", err, ErrNotFound)
	}
}

// TestSomebodyWhoSignsInHereNeedsNoPointer: the same key is the same person, and
// a room at their own installation is simply theirs.
func TestSomebodyWhoSignsInHereNeedsNoPointer(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	bruno, identity, err := setup.accounts.Register(ctx, "bruno", "another good password")
	if err != nil {
		t.Fatalf("register bruno: %v", err)
	}
	invitation, err := setup.service.Invite(ctx, setup.inviter, setup.room.ID, bruno.Handle())
	if err != nil {
		t.Fatalf("Invite() error = %v", err)
	}

	accepted, err := setup.service.Accept(ctx, Signer{AccountID: identity.ID(), PublicKey: identity.Public}, "bruno", invitation.ID)
	if err != nil {
		t.Fatalf("Accept() error = %v", err)
	}
	if !accepted.Local || accepted.UserID != bruno.UserID {
		t.Errorf("Accept() = %+v, want bruno's own user, marked local", accepted)
	}
}

var nothingMayLeave = errors.New("nothing may leave this installation")

func TestAnInvitationOnThisInstallationNeverLeavesIt(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	setup.relay.err = nothingMayLeave
	const home = "http://localhost:8080"

	bruno, identity, err := setup.accounts.Register(ctx, "bruno", "another good password")
	if err != nil {
		t.Fatalf("register bruno: %v", err)
	}
	invitation, err := setup.service.Invite(ctx, setup.inviter, setup.room.ID, bruno.Handle())
	if err != nil {
		t.Fatalf("Invite() error = %v", err)
	}

	link := Link{Home: home, InvitationID: invitation.ID}.String()
	brunoHere := sessions.Principal{AccountID: bruno.ID, UserID: bruno.UserID,
		ApplicationID: applications.FirstPartyID}

	_, preview, err := setup.service.Look(ctx, home, brunoHere, identity, link)
	if err != nil {
		t.Fatalf("Look() error = %v", err)
	}
	if preview.RoomName != setup.room.Name {
		t.Errorf("Look() room = %q, want %q", preview.RoomName, setup.room.Name)
	}

	joined, err := setup.service.Join(ctx, home, bruno, identity, link)
	if err != nil {
		t.Fatalf("Join() error = %v", err)
	}
	if joined.Remote != nil || joined.RoomID != setup.room.ID {
		t.Errorf("Join() = %+v, want the room itself and no pointer", joined)
	}

	elsewhere := Link{Home: "https://elsewhere.example", InvitationID: invitation.ID}.String()
	if _, _, err := setup.service.Look(ctx, home, brunoHere, identity, elsewhere); !errors.Is(err, nothingMayLeave) {
		t.Errorf("Look() at another installation's link error = %v, want it to have travelled", err)
	}
}

/*
TestASignedRequestIsAcceptedOnce is replay protection against the real table:
the same signed request, presented twice, is refused the second time.
*/
func TestASignedRequestIsAcceptedOnce(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	identity, _ := visitor(t)
	request := httptest.NewRequest(http.MethodGet, "https://convia.example/v1/peer/invitations/"+sampleInvitationID, nil)
	Sign(request, nil, identity, time.Now())

	if _, err := setup.service.Verify(ctx, request, nil); err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if _, err := setup.service.Verify(ctx, request, nil); !errors.Is(err, ErrUnauthenticated) {
		t.Errorf("verifying the same request again error = %v, want %v", err, ErrUnauthenticated)
	}

	if _, err := setup.service.Visit(ctx, Signer{AccountID: identity.ID(), PublicKey: identity.Public}); !errors.Is(err, ErrUnauthenticated) {
		t.Errorf("a signer who is nobody here visiting error = %v, want %v", err, ErrUnauthenticated)
	}
}

/*
TestJoiningRemembersTheRoomOnceAndLeavingForgetsIt drives the other side, with a
stand-in for the home: one pointer however often the room is joined, a pointer
kept while the home cannot be reached to leave, and dropped on the person's word
without the home.
*/
func TestJoiningRemembersTheRoomOnceAndLeavingForgetsIt(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	identity, _ := visitor(t)
	link := Link{Home: "https://elsewhere.example", InvitationID: sampleInvitationID}.String()
	setup.relay.answers["POST /v1/peer/invitations/"+sampleInvitationID+"/accept"] = Response{
		Status: http.StatusOK,
		Body:   []byte(`{"room_id":"room_7KQZP4XN2VJH6TBWMDR3YAFC5E","room_name":"Their room","user_id":"usr_7KQZP4XN2VJH6TBWMDR3YAFC5E","local":false}`),
	}

	for range 2 {
		joined, err := setup.service.Join(ctx, "https://here.example", setup.ana, identity, link)
		if err != nil {
			t.Fatalf("Join() error = %v", err)
		}
		if joined.Remote == nil || joined.Remote.Home != "https://elsewhere.example" {
			t.Fatalf("Join() = %+v, want a pointer to the room elsewhere", joined)
		}
	}

	remembered, err := setup.service.RemoteRooms(ctx, setup.ana.ID)
	if err != nil || len(remembered) != 1 {
		t.Fatalf("RemoteRooms() = %+v, %v, want exactly one", remembered, err)
	}

	setup.relay.err = ErrUnreachable
	if err := setup.service.Leave(ctx, identity, remembered[0]); !errors.Is(err, ErrUnreachable) {
		t.Errorf("leaving an unreachable home error = %v, want %v", err, ErrUnreachable)
	}
	if kept, _ := setup.service.RemoteRooms(ctx, setup.ana.ID); len(kept) != 1 {
		t.Error("the pointer was forgotten although the home never heard the person leave")
	}

	// Still unreachable: forgetting must not need the home.
	if err := setup.service.Forget(ctx, remembered[0]); err != nil {
		t.Fatalf("Forget() error = %v", err)
	}
	if forgotten, _ := setup.service.RemoteRooms(ctx, setup.ana.ID); len(forgotten) != 0 {
		t.Errorf("the pointer is still here after forgetting it: %+v", forgotten)
	}

	setup.relay.err = nil
	if _, err := setup.service.Join(ctx, "https://here.example", setup.ana, identity, link); err != nil {
		t.Fatalf("joining again error = %v", err)
	}
	rejoined, _ := setup.service.RemoteRooms(ctx, setup.ana.ID)
	if len(rejoined) != 1 {
		t.Fatalf("RemoteRooms() after joining again = %+v, want exactly one", rejoined)
	}

	setup.relay.answers["POST /v1/peer/rooms/room_7KQZP4XN2VJH6TBWMDR3YAFC5E/leave"] = Response{Status: http.StatusNoContent}
	if err := setup.service.Leave(ctx, identity, rejoined[0]); err != nil {
		t.Fatalf("Leave() error = %v", err)
	}
	if left, _ := setup.service.RemoteRooms(ctx, setup.ana.ID); len(left) != 0 {
		t.Errorf("the pointer is still here after leaving: %+v", left)
	}
}

// TestAHomeThatAnswersNonsenseIsNotBelieved: an acceptance naming a malformed
// room is refused rather than stored.
func TestAHomeThatAnswersNonsenseIsNotBelieved(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	identity, _ := visitor(t)
	setup.relay.answers["POST /v1/peer/invitations/"+sampleInvitationID+"/accept"] = Response{
		Status: http.StatusOK,
		Body:   []byte(`{"room_id":"../../v1/users","room_name":"x","user_id":"usr_7KQZP4XN2VJH6TBWMDR3YAFC5E"}`),
	}

	if _, err := setup.service.Join(ctx, "https://here.example", setup.ana, identity,
		Link{Home: "https://elsewhere.example", InvitationID: sampleInvitationID}.String()); !errors.Is(err, ErrUnreachable) {
		t.Errorf("Join() with a malformed room error = %v, want %v", err, ErrUnreachable)
	}
	if stored, _ := setup.service.RemoteRooms(ctx, setup.ana.ID); len(stored) != 0 {
		t.Errorf("a malformed answer was stored: %+v", stored)
	}
}

/*
TestPendingListsOnlyWhatStillWorks is M18-028: the invitations a person made into
a room that nobody accepted, withdrew or outlived, newest first, and nobody
else's.
*/
func TestPendingListsOnlyWhatStillWorks(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	bia, biaSigner := visitor(t)
	cai, _ := visitor(t)
	dan, _ := visitor(t)

	accepted, err := setup.service.Invite(ctx, setup.inviter, setup.room.ID, accounts.Handle("bia", bia.ID()))
	if err != nil {
		t.Fatalf("Invite() error = %v", err)
	}
	if _, err := setup.service.Accept(ctx, biaSigner, "bia", accepted.ID); err != nil {
		t.Fatalf("Accept() error = %v", err)
	}
	withdrawn, err := setup.service.Invite(ctx, setup.inviter, setup.room.ID, accounts.Handle("cai", cai.ID()))
	if err != nil {
		t.Fatalf("Invite() error = %v", err)
	}
	if err := setup.service.Revoke(ctx, setup.inviter, withdrawn.ID); err != nil {
		t.Fatalf("Revoke() error = %v", err)
	}
	older, err := setup.service.Invite(ctx, setup.inviter, setup.room.ID, accounts.Handle("cai", cai.ID()))
	if err != nil {
		t.Fatalf("Invite() error = %v", err)
	}
	newer, err := setup.service.Invite(ctx, setup.inviter, setup.room.ID, accounts.Handle("dan", dan.ID()))
	if err != nil {
		t.Fatalf("Invite() error = %v", err)
	}

	pending, err := setup.service.Pending(ctx, setup.inviter, setup.room.ID)
	if err != nil {
		t.Fatalf("Pending() error = %v", err)
	}
	var listed []string
	for _, invitation := range pending {
		listed = append(listed, invitation.ID)
	}
	if want := []string{newer.ID, older.ID}; !slices.Equal(listed, want) {
		t.Errorf("Pending() = %v, want %v", listed, want)
	}

	bruno, _, err := setup.accounts.Register(ctx, "bruno", "another good password")
	if err != nil {
		t.Fatalf("register bruno: %v", err)
	}
	outsider := sessions.Principal{AccountID: bruno.ID, UserID: bruno.UserID, ApplicationID: applications.FirstPartyID}
	if _, err := setup.service.Pending(ctx, outsider, setup.room.ID); !errors.Is(err, ErrRoomNotFound) {
		t.Errorf("listing a room one is not in error = %v, want %v", err, ErrRoomNotFound)
	}
	if _, _, err := setup.rooms.AddMember(ctx, applications.FirstPartyID, setup.room.ID, bruno.UserID); err != nil {
		t.Fatalf("AddMember() error = %v", err)
	}
	if others, err := setup.service.Pending(ctx, outsider, setup.room.ID); err != nil || len(others) != 0 {
		t.Errorf("another member's list = %v, %v, want nothing of ana's", others, err)
	}

	later := time.Now().UTC().Add(InvitationLifetime + time.Minute)
	setup.service.now = func() time.Time { return later }
	if expired, err := setup.service.Pending(ctx, setup.inviter, setup.room.ID); err != nil || len(expired) != 0 {
		t.Errorf("a day later Pending() = %v, %v, want nothing", expired, err)
	}
}
