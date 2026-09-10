package invitations

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

	"convia/internal/applications"
	"convia/internal/calls"
	"convia/internal/config"
	"convia/internal/database"
	"convia/internal/media"
	"convia/internal/participants"
	"convia/internal/rooms"
	"convia/internal/secret"
	"convia/internal/users"
)

/*
testDatabaseURLEnvironment points these tests at a PostgreSQL instance.

They are skipped when it is unset, so `go test ./...` stays runnable without
infrastructure, and CI always sets it so the coverage is never optional where
it matters.
*/
const testDatabaseURLEnvironment = "CONVIA_TEST_DATABASE_URL"

/*
issuingPlane is a media plane that hands out a recognizable credential.

Redeeming an invitation has to produce something to connect with, so these
tests need a media plane. What it returns matters only in that a token appears
and can be looked for in the log.
*/
type issuingPlane struct{}

func (issuingPlane) OpenSession(_ context.Context, request media.SessionRequest) (media.Session, error) {
	return media.Session{Reference: "room-for-" + request.CallID}, nil
}

func (issuingPlane) CloseSession(context.Context, media.Session) error { return nil }

func (issuingPlane) IssueCredential(_ context.Context, admission media.Admission) (media.Credential, error) {
	return media.Credential{
		URL:       "wss://media.test",
		Token:     media.Token("connection-credential-for-" + admission.ParticipantID),
		ExpiresAt: time.Now().Add(admission.Lifetime),
	}, nil
}

type fixture struct {
	service      *Service
	participants *participants.Service
	calls        *calls.Service
	rooms        *rooms.Service
	users        *users.Service
	applications *applications.Service
	first        string
	second       string
	logs         *bytes.Buffer
}

func newFixture(t *testing.T) fixture {
	t.Helper()

	maintenanceURL := strings.TrimSpace(os.Getenv(testDatabaseURLEnvironment))
	if maintenanceURL == "" {
		t.Skipf("set %s to run the invitation integration tests", testDatabaseURLEnvironment)
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
		MaxConnections: 12,
		ConnectTimeout: 10 * time.Second,
		QueryTimeout:   5 * time.Second,
	}, logger)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(pool.Close)

	applicationService := applications.NewService(applications.NewStore(pool), logger)
	roomService := rooms.NewService(rooms.NewStore(pool), applicationService, logger)
	userService := users.NewService(users.NewStore(pool), applicationService, logger)
	callService := calls.NewService(calls.NewStore(pool), applicationService, roomService,
		issuingPlane{}, logger)
	participantService := participants.NewService(participants.NewStore(pool),
		applicationService, callService, roomService, userService, logger)

	setup := fixture{
		service: NewService(NewStore(pool), applicationService, callService,
			userService, participantService, logger),
		participants: participantService,
		calls:        callService,
		rooms:        roomService,
		users:        userService,
		applications: applicationService,
		first:        newApplication(t, applicationService, "First Tenant"),
		second:       newApplication(t, applicationService, "Second Tenant"),
		logs:         logs,
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

func (setup fixture) newCall(t *testing.T, applicationID string, capacity *int) calls.Call {
	t.Helper()

	room, err := setup.rooms.Create(context.Background(), applicationID, rooms.Definition{
		Name:            "Standup",
		MaxParticipants: capacity,
	})
	if err != nil {
		t.Fatalf("create a room: %v", err)
	}

	call, err := setup.calls.Start(context.Background(), applicationID, room.ID,
		calls.Definition{}, calls.ActorApplication)
	if err != nil {
		t.Fatalf("start a call: %v", err)
	}
	return call
}

func (setup fixture) newUser(t *testing.T, applicationID, subject string) string {
	t.Helper()

	user, _, err := setup.users.Resolve(context.Background(), applicationID,
		users.Identity{ExternalSubject: subject})
	if err != nil {
		t.Fatalf("resolve %q: %v", subject, err)
	}
	return user.ID
}

// invite issues an invitation and fails the test if it could not.
func (setup fixture) invite(t *testing.T, applicationID, callID, userID, role string) (Invitation, secret.Value) {
	t.Helper()

	invitation, value, err := setup.service.Issue(context.Background(), applicationID, callID,
		Request{UserID: userID, Role: role})
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	return invitation, value
}

/*
TestAnInvitationBecomesAPresenceAndTheMeansToConnect is the whole point of the
milestone in one test.

The application issues something; a party that is not the application presents
it; Convia turns it into a place in the call and a credential. Nothing about
this could have been enforced before M13, which is why it waited.
*/
func TestAnInvitationBecomesAPresenceAndTheMeansToConnect(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	call := setup.newCall(t, setup.first, nil)
	userID := setup.newUser(t, setup.first, "ana")
	invitation, value := setup.invite(t, setup.first, call.ID, userID, "moderator")

	if invitation.Status(time.Now().UTC()) != StatusPending {
		t.Errorf("a new invitation is %q", invitation.Status(time.Now().UTC()))
	}

	verified, err := setup.service.Authenticate(ctx, Render(invitation.ID, value))
	if err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}

	redeemed, participant, credential, err := setup.service.Redeem(ctx, verified)
	if err != nil {
		t.Fatalf("Redeem() error = %v", err)
	}

	if participant.UserID != userID {
		t.Errorf("the redemption admitted %q, not the person invited", participant.UserID)
	}
	if participant.Role != participants.RoleModerator {
		t.Errorf("the participant is a %q, not the role the invitation conferred", participant.Role)
	}
	if !credential.Issued() {
		t.Error("the redemption produced no credential to connect with")
	}

	// The invitation now points at what it produced, so an application can see
	// which participation came from which invitation without its own ledger.
	if redeemed.ParticipantID != participant.ID {
		t.Errorf("the invitation names participation %q, not %q", redeemed.ParticipantID, participant.ID)
	}
	if redeemed.RedeemedAt == nil {
		t.Error("the invitation does not record when it was redeemed")
	}
	if redeemed.Status(time.Now().UTC()) != StatusRedeemed {
		t.Errorf("a redeemed invitation reads as %q", redeemed.Status(time.Now().UTC()))
	}
}

/*
TestRedeemingAgainReturnsTheSamePresence is the reconnection case.

Somebody whose connection dropped opens the same link again. Joining is
idempotent by the person, so they arrive at the participation they already had
with a fresh credential, and the record of when they first joined is not
overwritten.
*/
func TestRedeemingAgainReturnsTheSamePresence(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	call := setup.newCall(t, setup.first, nil)
	userID := setup.newUser(t, setup.first, "ana")
	invitation, _ := setup.invite(t, setup.first, call.ID, userID, "member")

	first, participant, _, err := setup.service.Redeem(ctx, invitation)
	if err != nil {
		t.Fatalf("Redeem() error = %v", err)
	}

	again, same, credential, err := setup.service.Redeem(ctx, first)
	if err != nil {
		t.Fatalf("Redeem() again error = %v", err)
	}

	if same.ID != participant.ID {
		t.Errorf("redeeming again produced participation %q, not %q", same.ID, participant.ID)
	}
	if !credential.Issued() {
		t.Error("redeeming again produced no credential")
	}
	if !again.RedeemedAt.Equal(*first.RedeemedAt) {
		t.Errorf("redeeming again moved the first redemption from %v to %v",
			first.RedeemedAt, again.RedeemedAt)
	}
}

/*
TestAnInvitationThatCanNoLongerBeUsedSaysSoWithoutSayingWhy covers the three
ways one dies.

They are one answer on purpose. The holder is not the party that issued the
invitation, and telling them apart would let anyone with a dead link learn
whether it was withdrawn, whether it merely aged out, or whether somebody said
no on their behalf.
*/
func TestAnInvitationThatCanNoLongerBeUsedSaysSoWithoutSayingWhy(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	t.Run("withdrawn", func(t *testing.T) {
		call := setup.newCall(t, setup.first, nil)
		invitation, _ := setup.invite(t, setup.first, call.ID, setup.newUser(t, setup.first, "ana"), "member")

		revoked, err := setup.service.Revoke(ctx, setup.first, invitation.ID)
		if err != nil {
			t.Fatalf("Revoke() error = %v", err)
		}
		if _, _, _, err := setup.service.Redeem(ctx, revoked); !errors.Is(err, ErrUnusable) {
			t.Errorf("Redeem() error = %v, want %v", err, ErrUnusable)
		}
	})

	t.Run("refused by the invitee", func(t *testing.T) {
		call := setup.newCall(t, setup.first, nil)
		invitation, _ := setup.invite(t, setup.first, call.ID, setup.newUser(t, setup.first, "bob"), "member")

		declined, err := setup.service.Decline(ctx, invitation)
		if err != nil {
			t.Fatalf("Decline() error = %v", err)
		}
		if _, _, _, err := setup.service.Redeem(ctx, declined); !errors.Is(err, ErrUnusable) {
			t.Errorf("Redeem() error = %v, want %v", err, ErrUnusable)
		}
	})

	t.Run("out of time", func(t *testing.T) {
		call := setup.newCall(t, setup.first, nil)
		invitation, _ := setup.invite(t, setup.first, call.ID, setup.newUser(t, setup.first, "cleo"), "member")

		// The clock is not mocked here; the expiry is moved into the past,
		// which is the same condition and needs no seam in production code.
		invitation.ExpiresAt = time.Now().UTC().Add(-time.Minute)
		if _, _, _, err := setup.service.Redeem(ctx, invitation); !errors.Is(err, ErrUnusable) {
			t.Errorf("Redeem() error = %v, want %v", err, ErrUnusable)
		}
	})
}

/*
TestWithdrawingOutranksARedemptionThatAlreadyHappened is the rule that makes
revocation worth having.

The ordinary reason to withdraw an invitation is that it reached somebody it
should not have. A link that kept working because it had been used once would
defeat the purpose.
*/
func TestWithdrawingOutranksARedemptionThatAlreadyHappened(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	call := setup.newCall(t, setup.first, nil)
	invitation, _ := setup.invite(t, setup.first, call.ID, setup.newUser(t, setup.first, "ana"), "member")

	redeemed, _, _, err := setup.service.Redeem(ctx, invitation)
	if err != nil {
		t.Fatalf("Redeem() error = %v", err)
	}

	revoked, err := setup.service.Revoke(ctx, setup.first, redeemed.ID)
	if err != nil {
		t.Fatalf("Revoke() error = %v", err)
	}
	if revoked.Status(time.Now().UTC()) != StatusRevoked {
		t.Errorf("a withdrawn invitation reads as %q", revoked.Status(time.Now().UTC()))
	}
	if _, _, _, err := setup.service.Redeem(ctx, revoked); !errors.Is(err, ErrUnusable) {
		t.Errorf("Redeem() after a withdrawal error = %v, want %v", err, ErrUnusable)
	}
}

// TestDecliningAfterJoiningIsRefused keeps the record honest about what happened.
func TestDecliningAfterJoiningIsRefused(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	call := setup.newCall(t, setup.first, nil)
	invitation, _ := setup.invite(t, setup.first, call.ID, setup.newUser(t, setup.first, "ana"), "member")

	redeemed, _, _, err := setup.service.Redeem(ctx, invitation)
	if err != nil {
		t.Fatalf("Redeem() error = %v", err)
	}

	if _, err := setup.service.Decline(ctx, redeemed); !errors.Is(err, ErrAlreadyRedeemed) {
		t.Errorf("Decline() after redeeming error = %v, want %v", err, ErrAlreadyRedeemed)
	}
}

/*
TestAnInvitationCannotUndoARemoval is the property that would have made
invitations dangerous if it did not hold.

Someone a moderator put out of a call must not walk back in through a link they
were sent earlier. The rule is not restated here: redeeming goes through the
participants package, which is the only place it lives.
*/
func TestAnInvitationCannotUndoARemoval(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	call := setup.newCall(t, setup.first, nil)
	userID := setup.newUser(t, setup.first, "ana")
	invitation, _ := setup.invite(t, setup.first, call.ID, userID, "member")

	redeemed, participant, _, err := setup.service.Redeem(ctx, invitation)
	if err != nil {
		t.Fatalf("Redeem() error = %v", err)
	}

	if _, err := setup.participants.Remove(ctx, setup.first, participant.ID,
		participants.RemoverApplication, "", "Disruptive."); err != nil {
		t.Fatalf("Remove() error = %v", err)
	}

	if _, _, _, err := setup.service.Redeem(ctx, redeemed); !errors.Is(err, participants.ErrRemoved) {
		t.Errorf("Redeem() after a removal error = %v, want %v", err, participants.ErrRemoved)
	}
}

// TestAnEndedCallCannotBeJoinedByInvitation covers the other terminal state.
func TestAnEndedCallCannotBeJoinedByInvitation(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	call := setup.newCall(t, setup.first, nil)
	invitation, _ := setup.invite(t, setup.first, call.ID, setup.newUser(t, setup.first, "ana"), "member")

	if _, err := setup.calls.End(ctx, setup.first, call.ID, calls.ActorApplication, "Finished."); err != nil {
		t.Fatalf("End() error = %v", err)
	}

	if _, _, _, err := setup.service.Redeem(ctx, invitation); !errors.Is(err, ErrCallEnded) {
		t.Errorf("Redeem() error = %v, want %v", err, ErrCallEnded)
	}
}

/*
TestOnlyTheRightSecretAuthenticates is the credential half of the design.

An invitation is stored as a digest and nothing else, so a database dump does
not reveal a way into anybody's call. Every failure is the same failure, so a
caller cannot learn whether an identifier exists by watching which refusal
comes back.
*/
func TestOnlyTheRightSecretAuthenticates(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	call := setup.newCall(t, setup.first, nil)
	invitation, value := setup.invite(t, setup.first, call.ID, setup.newUser(t, setup.first, "ana"), "member")

	if _, err := setup.service.Authenticate(ctx, Render(invitation.ID, value)); err != nil {
		t.Fatalf("Authenticate() with the issued token error = %v", err)
	}

	refused := map[string]string{
		"another secret":        Render(invitation.ID, secret.New()),
		"an unknown invitation": Render(NewID(), value),
		"an application key":    "cvk_" + invitation.ID[len(idPrefix):] + "_" + string(value),
		"the identifier alone":  invitation.ID,
		"nonsense":              "not-a-key",
	}

	for name, token := range refused {
		if _, err := setup.service.Authenticate(ctx, token); !errors.Is(err, ErrUnauthenticated) {
			t.Errorf("Authenticate() with %s error = %v, want %v", name, err, ErrUnauthenticated)
		}
	}
}

/*
TestSuspendingAnApplicationWithdrawsItsInvitations means an operator does not
have to hunt down every outstanding link.

It is the same guarantee an application key already has, and for the same
reason: Convia stopping serving a tenant has to stop everything that tenant
issued.
*/
func TestSuspendingAnApplicationWithdrawsItsInvitations(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	call := setup.newCall(t, setup.first, nil)
	invitation, value := setup.invite(t, setup.first, call.ID, setup.newUser(t, setup.first, "ana"), "member")
	token := Render(invitation.ID, value)

	if _, err := setup.service.Authenticate(ctx, token); err != nil {
		t.Fatalf("Authenticate() before suspension error = %v", err)
	}

	if _, err := setup.applications.Suspend(ctx, setup.first); err != nil {
		t.Fatalf("Suspend() error = %v", err)
	}

	if _, err := setup.service.Authenticate(ctx, token); !errors.Is(err, ErrUnauthenticated) {
		t.Errorf("Authenticate() after suspension error = %v, want %v", err, ErrUnauthenticated)
	}
}

/*
TestOneTenantsInvitationIsInvisibleToAnother proves the isolation.

The second tenant is refused with the same answer it would get for an
identifier that never existed, so asking cannot be used to discover that
somebody else's invitation is real.
*/
func TestOneTenantsInvitationIsInvisibleToAnother(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	call := setup.newCall(t, setup.first, nil)
	invitation, _ := setup.invite(t, setup.first, call.ID, setup.newUser(t, setup.first, "ana"), "member")

	if _, err := setup.service.Get(ctx, setup.second, invitation.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get() across tenants error = %v, want %v", err, ErrNotFound)
	}
	if _, err := setup.service.Revoke(ctx, setup.second, invitation.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("Revoke() across tenants error = %v, want %v", err, ErrNotFound)
	}

	// The invitation is untouched, so a failed cross-tenant revocation cannot
	// be used to disable somebody else's link.
	still, err := setup.service.Get(ctx, setup.first, invitation.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if !still.Usable(time.Now().UTC()) {
		t.Error("another tenant's request changed the invitation")
	}
}

/*
TestTheInvitationSecretIsNeverWrittenDown is the credential-hygiene assertion.

An invitation is a way into a conversation. The audit trail records that one
was issued, to whom, and for which call, because that is what an incident needs
to know; the secret itself must not be anywhere it could be read later.
*/
func TestTheInvitationSecretIsNeverWrittenDown(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	call := setup.newCall(t, setup.first, nil)
	userID := setup.newUser(t, setup.first, "ana")

	setup.logs.Reset()
	invitation, value := setup.invite(t, setup.first, call.ID, userID, "member")

	if _, _, _, err := setup.service.Redeem(ctx, invitation); err != nil {
		t.Fatalf("Redeem() error = %v", err)
	}

	logged := setup.logs.String()
	if strings.Contains(logged, string(value)) {
		t.Errorf("the invitation secret was logged: %s", logged)
	}
	if strings.Contains(logged, Render(invitation.ID, value)) {
		t.Errorf("the rendered invitation token was logged: %s", logged)
	}
	if !strings.Contains(logged, "invitation.issued") || !strings.Contains(logged, "invitation.redeemed") {
		t.Errorf("the audit trail does not record issuing and redeeming: %s", logged)
	}
	if !strings.Contains(logged, invitation.ID) {
		t.Errorf("the audit trail does not say which invitation: %s", logged)
	}
}

/*
TestListingShowsWhatBecameOfEveryInvitation is the application's own view.

Every invitation is returned whatever its state, because the listing is also a
record of who was invited. No secret appears, because Convia does not have one.
*/
func TestListingShowsWhatBecameOfEveryInvitation(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	call := setup.newCall(t, setup.first, nil)
	other := setup.newCall(t, setup.first, nil)

	setup.invite(t, setup.first, call.ID, setup.newUser(t, setup.first, "ana"), "member")
	setup.invite(t, setup.first, call.ID, setup.newUser(t, setup.first, "bob"), "moderator")
	setup.invite(t, setup.first, other.ID, setup.newUser(t, setup.first, "cleo"), "member")

	all, err := setup.service.List(ctx, setup.first, ListOptions{})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(all.Invitations) != 3 {
		t.Errorf("List() returned %d invitations, want 3", len(all.Invitations))
	}

	narrowed, err := setup.service.List(ctx, setup.first, ListOptions{CallID: call.ID})
	if err != nil {
		t.Fatalf("List() by call error = %v", err)
	}
	if len(narrowed.Invitations) != 2 {
		t.Errorf("List() for one call returned %d invitations, want 2", len(narrowed.Invitations))
	}
	for _, invitation := range narrowed.Invitations {
		if invitation.CallID != call.ID {
			t.Errorf("the listing carries an invitation for %q", invitation.CallID)
		}
	}

	empty, err := setup.service.List(ctx, setup.second, ListOptions{})
	if err != nil {
		t.Fatalf("List() for another tenant error = %v", err)
	}
	if len(empty.Invitations) != 0 {
		t.Errorf("another tenant sees %d invitations", len(empty.Invitations))
	}
}

/*
TestInvitingSomeoneConviaDoesNotKnowIsRefused keeps an invitation from naming
nobody.

A person is a Convia user the application already told Convia about, and an
invitation to an identifier nothing resolves would be a link that could never
be redeemed.
*/
func TestInvitingSomeoneConviaDoesNotKnowIsRefused(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	call := setup.newCall(t, setup.first, nil)

	_, _, err := setup.service.Issue(ctx, setup.first, call.ID,
		Request{UserID: "usr_AAAAAAAAAAAAAAAAAAAAAAAAAA", Role: "member"})
	if !errors.Is(err, ErrUserNotFound) {
		t.Errorf("Issue() for an unknown person error = %v, want %v", err, ErrUserNotFound)
	}

	// Somebody else's user is equally unknown here, which is the tenant
	// boundary doing its job rather than a separate rule.
	stranger := setup.newUser(t, setup.second, "ana")
	if _, _, err := setup.service.Issue(ctx, setup.first, call.ID,
		Request{UserID: stranger, Role: "member"}); !errors.Is(err, ErrUserNotFound) {
		t.Errorf("Issue() for another tenant's person error = %v, want %v", err, ErrUserNotFound)
	}
}
