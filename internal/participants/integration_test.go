package participants

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"log/slog"
	"net/url"
	"os"
	"strconv"
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
	"convia/internal/media"
	"convia/internal/rooms"
	"convia/internal/users"
)

/*
testDatabaseURLEnvironment points these tests at a PostgreSQL instance.

The tests are skipped when it is unset, so `go test ./...` runs without
infrastructure, and every test works in its own database so runs never share
state.
*/
const testDatabaseURLEnvironment = "CONVIA_TEST_DATABASE_URL"

// fixture is a service under test together with two tenants to isolate.
type fixture struct {
	service      *Service
	calls        *calls.Service
	rooms        *rooms.Service
	users        *users.Service
	applications *applications.Service
	broker       *events.Broker
	first        string
	second       string
	logs         *bytes.Buffer
}

/*
newFixture builds a fixture with no media plane, which is what most of these
tests want: they are about who is in a call, not about what carries it.

Tests that need a media plane use newFixtureWith.
*/
func newFixture(t *testing.T) fixture {
	t.Helper()
	return newFixtureWith(t, media.Absent{})
}

func newFixtureWith(t *testing.T, plane calls.MediaPlane) fixture {
	t.Helper()

	maintenanceURL := strings.TrimSpace(os.Getenv(testDatabaseURLEnvironment))
	if maintenanceURL == "" {
		t.Skipf("set %s to run the participant integration tests", testDatabaseURLEnvironment)
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
	userService := users.NewService(users.NewStore(pool), applicationService, logger)
	roomService := rooms.NewService(rooms.NewStore(pool), applicationService, userService, logger)
	broker := events.NewBroker()
	// No durable sink: these tests are about what the domain announces,
	// not about where it is later delivered.
	announcer := events.NewAnnouncer(broker, nil, logger)
	callService := calls.NewService(calls.NewStore(pool), applicationService, roomService,
		plane, announcer, logger)

	setup := fixture{
		service: NewService(NewStore(pool), applicationService, callService,
			roomService, userService, announcer, logger),
		calls:        callService,
		rooms:        roomService,
		users:        userService,
		applications: applicationService,
		broker:       broker,
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

	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect to run %q: %v", statement, err)
	}
	defer pool.Close()

	if _, err := pool.Exec(ctx, statement); err != nil {
		t.Fatalf("run %q: %v", statement, err)
	}
}

/*
newCall creates a room with an optional capacity and starts a call in it.

Almost every test needs somewhere for people to be, and the capacity is the one
thing that varies, so it is the only parameter.
*/
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

// newUser resolves one of an application's people into a Convia user.
func (setup fixture) newUser(t *testing.T, applicationID, subject string) string {
	t.Helper()

	user, _, err := setup.users.Resolve(context.Background(), applicationID,
		users.Identity{ExternalSubject: subject})
	if err != nil {
		t.Fatalf("resolve %q: %v", subject, err)
	}
	return user.ID
}

// join admits someone and fails the test if it could not.
func (setup fixture) join(t *testing.T, applicationID, callID, userID string, role Role) Participant {
	t.Helper()

	participant, _, err := setup.service.Join(context.Background(), applicationID, callID,
		Admission{UserID: userID, Role: string(role)})
	if err != nil {
		t.Fatalf("Join() error = %v", err)
	}
	return participant
}

func capacityOf(limit int) *int {
	return &limit
}

/*
TestJoiningIsIdempotentByThePerson is what makes a reconnection safe.

A client whose network dropped and came back is the same person. Admitting them
again would put them in the roster twice, which is wrong in a way users notice
immediately.
*/
func TestJoiningIsIdempotentByThePerson(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	call := setup.newCall(t, setup.first, nil)
	user := setup.newUser(t, setup.first, "ada")

	first, admitted, err := setup.service.Join(ctx, setup.first, call.ID, Admission{UserID: user})
	if err != nil {
		t.Fatalf("Join() error = %v", err)
	}
	if !admitted {
		t.Fatal("the first join did not report admitting anyone")
	}

	second, admittedAgain, err := setup.service.Join(ctx, setup.first, call.ID, Admission{UserID: user})
	if err != nil {
		t.Fatalf("Join() on the reconnection error = %v", err)
	}
	if admittedAgain {
		t.Error("a reconnection reported admitting someone who was already there")
	}
	if second.ID != first.ID {
		t.Errorf("participant = %s, want the one already in the call (%s)", second.ID, first.ID)
	}

	page, err := setup.service.List(ctx, setup.first, call.ID, ListOptions{})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(page.Participants) != 1 {
		t.Errorf("the roster holds %d entries, want the person listed once", len(page.Participants))
	}
}

/*
TestOnlyOneOfManySimultaneousJoinsAdmits is the race the unique index settles.

The same person arriving twice at once must produce one participation, not two.
*/
func TestOnlyOneOfManySimultaneousJoinsAdmits(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	call := setup.newCall(t, setup.first, nil)
	user := setup.newUser(t, setup.first, "ada")

	const racers = 8
	var (
		start    sync.WaitGroup
		finished sync.WaitGroup
		mutex    sync.Mutex
	)
	admissions := 0
	identifiers := map[string]bool{}

	start.Add(1)
	for range racers {
		finished.Add(1)
		go func() {
			defer finished.Done()
			start.Wait()

			participant, admitted, err := setup.service.Join(ctx, setup.first, call.ID,
				Admission{UserID: user})

			mutex.Lock()
			defer mutex.Unlock()
			if err != nil {
				t.Errorf("Join() error = %v", err)
				return
			}
			if admitted {
				admissions++
			}
			identifiers[participant.ID] = true
		}()
	}

	start.Done()
	finished.Wait()

	if admissions != 1 {
		t.Errorf("%d of %d simultaneous joins reported an admission, want exactly one", admissions, racers)
	}
	if len(identifiers) != 1 {
		t.Errorf("the callers received %d different participants, want one", len(identifiers))
	}
}

/*
TestCapacityIsEnforcedAgainstSimultaneousJoins is the reason joining takes a
lock on the call.

Counting the room and then inserting would let two joins both see the last seat
free and both take it. No amount of application-level checking closes that
window; serializing joins to one call does.
*/
func TestCapacityIsEnforcedAgainstSimultaneousJoins(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	const seats = 3
	const arrivals = 10

	call := setup.newCall(t, setup.first, capacityOf(seats))

	people := make([]string, arrivals)
	for index := range people {
		people[index] = setup.newUser(t, setup.first, "person-"+strconv.Itoa(index))
	}

	var (
		start    sync.WaitGroup
		finished sync.WaitGroup
		mutex    sync.Mutex
	)
	admitted, refused := 0, 0

	start.Add(1)
	for _, user := range people {
		finished.Add(1)
		go func() {
			defer finished.Done()
			start.Wait()

			_, joined, err := setup.service.Join(ctx, setup.first, call.ID, Admission{UserID: user})

			mutex.Lock()
			defer mutex.Unlock()
			switch {
			case err == nil && joined:
				admitted++
			case errors.Is(err, ErrCallFull):
				refused++
			default:
				t.Errorf("Join() error = %v", err)
			}
		}()
	}

	start.Done()
	finished.Wait()

	if admitted != seats {
		t.Errorf("%d people were admitted, want the %d the room declared", admitted, seats)
	}
	if admitted+refused != arrivals {
		t.Errorf("%d arrivals were accounted for, want %d", admitted+refused, arrivals)
	}

	present, err := setup.service.List(ctx, setup.first, call.ID, ListOptions{Status: string(StatusJoined)})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(present.Participants) != seats {
		t.Errorf("the roster holds %d people, want %d", len(present.Participants), seats)
	}
}

/*
TestLeavingFreesASeat proves capacity bounds the present rather than the history.

Only people still in the call occupy it, which is what lets a small room host a
long conversation people come and go from.
*/
func TestLeavingFreesASeat(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	call := setup.newCall(t, setup.first, capacityOf(1))
	ada := setup.newUser(t, setup.first, "ada")
	grace := setup.newUser(t, setup.first, "grace")

	first := setup.join(t, setup.first, call.ID, ada, RoleMember)

	if _, _, err := setup.service.Join(ctx, setup.first, call.ID, Admission{UserID: grace}); !errors.Is(err, ErrCallFull) {
		t.Fatalf("Join() error = %v, want %v", err, ErrCallFull)
	}

	if _, err := setup.service.Leave(ctx, setup.first, first.ID); err != nil {
		t.Fatalf("Leave() error = %v", err)
	}

	setup.join(t, setup.first, call.ID, grace, RoleMember)

	everyone, err := setup.service.List(ctx, setup.first, call.ID, ListOptions{})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(everyone.Participants) != 2 {
		t.Errorf("the roster holds %d entries, want both stints recorded", len(everyone.Participants))
	}
}

/*
TestARoomWithoutACapacityAdmitsAnyone proves an absent limit is not a limit of
zero: the application declined to state one.
*/
func TestARoomWithoutACapacityAdmitsAnyone(t *testing.T) {
	setup := newFixture(t)
	call := setup.newCall(t, setup.first, nil)

	for index := range 5 {
		user := setup.newUser(t, setup.first, "person-"+strconv.Itoa(index))
		setup.join(t, setup.first, call.ID, user, RoleMember)
	}

	page, err := setup.service.List(context.Background(), setup.first, call.ID,
		ListOptions{Status: string(StatusJoined)})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(page.Participants) != 5 {
		t.Errorf("the roster holds %d people, want everyone admitted", len(page.Participants))
	}
}

/*
TestLeavingIsRepeatable keeps a retry from being punished, and keeps someone
who was removed from being quietly converted into someone who left.
*/
func TestLeavingIsRepeatable(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	call := setup.newCall(t, setup.first, nil)
	participant := setup.join(t, setup.first, call.ID, setup.newUser(t, setup.first, "ada"), RoleMember)

	first, err := setup.service.Leave(ctx, setup.first, participant.ID)
	if err != nil {
		t.Fatalf("Leave() error = %v", err)
	}
	if first.Status != StatusLeft || first.LeftAt == nil {
		t.Fatalf("status = %q with left_at %v, want a recorded departure", first.Status, first.LeftAt)
	}

	second, err := setup.service.Leave(ctx, setup.first, participant.ID)
	if err != nil {
		t.Fatalf("Leave() on the repeat error = %v", err)
	}
	if !second.LeftAt.Equal(*first.LeftAt) {
		t.Error("the repeat moved the moment the person left")
	}
}

/*
TestRemovalIsTerminalForThatCall is why `removed` is a state and not a reason.

If someone a moderator put out could simply rejoin, removing them would mean
nothing.
*/
func TestRemovalIsTerminalForThatCall(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	call := setup.newCall(t, setup.first, nil)
	user := setup.newUser(t, setup.first, "ada")
	participant := setup.join(t, setup.first, call.ID, user, RoleMember)

	removed, err := setup.service.Remove(ctx, setup.first, participant.ID,
		RemoverApplication, "", "  disruptive behaviour  ")
	if err != nil {
		t.Fatalf("Remove() error = %v", err)
	}

	switch {
	case removed.Status != StatusRemoved:
		t.Errorf("status = %q, want %q", removed.Status, StatusRemoved)
	case removed.LeftAt == nil:
		t.Error("the removal recorded no moment")
	case removed.RemovedBy == nil || *removed.RemovedBy != RemoverApplication:
		t.Errorf("removed_by = %v, want %q", removed.RemovedBy, RemoverApplication)
	case removed.RemovalReason != "disruptive behaviour":
		t.Errorf("reason = %q, want it trimmed and stored", removed.RemovalReason)
	}

	if _, _, err := setup.service.Join(ctx, setup.first, call.ID, Admission{UserID: user}); !errors.Is(err, ErrRemoved) {
		t.Fatalf("Join() after a removal error = %v, want %v", err, ErrRemoved)
	}
}

/*
TestARemovedPersonMayJoinAnotherCall keeps a removal bounded to the conversation
it happened in. A removal is not a ban on the application's whole service.
*/
func TestARemovedPersonMayJoinAnotherCall(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	user := setup.newUser(t, setup.first, "ada")

	first := setup.newCall(t, setup.first, nil)
	participant := setup.join(t, setup.first, first.ID, user, RoleMember)
	if _, err := setup.service.Remove(ctx, setup.first, participant.ID, RemoverApplication, "", ""); err != nil {
		t.Fatalf("Remove() error = %v", err)
	}

	second := setup.newCall(t, setup.first, nil)
	setup.join(t, setup.first, second.ID, user, RoleMember)
}

/*
TestOnlyAModeratorMayRemoveSomeone is the rule Convia can actually enforce.

Convia does not decide whether the application may remove someone: it already
may, on its own authority. What Convia decides is whether the participant the
application named was entitled to, because Convia is what holds the roster.
*/
func TestOnlyAModeratorMayRemoveSomeone(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	call := setup.newCall(t, setup.first, nil)
	moderator := setup.join(t, setup.first, call.ID, setup.newUser(t, setup.first, "ada"), RoleModerator)
	member := setup.join(t, setup.first, call.ID, setup.newUser(t, setup.first, "grace"), RoleMember)
	target := setup.join(t, setup.first, call.ID, setup.newUser(t, setup.first, "alan"), RoleMember)

	if _, err := setup.service.Remove(ctx, setup.first, target.ID,
		RemoverApplication, member.ID, ""); !errors.Is(err, ErrNotAModerator) {
		t.Fatalf("Remove() named by a member error = %v, want %v", err, ErrNotAModerator)
	}

	stillThere, err := setup.service.Get(ctx, setup.first, target.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if !stillThere.Present() {
		t.Fatal("a refused removal took the participant out of the call anyway")
	}

	removed, err := setup.service.Remove(ctx, setup.first, target.ID,
		RemoverApplication, moderator.ID, "")
	if err != nil {
		t.Fatalf("Remove() named by a moderator error = %v", err)
	}
	if removed.RemovedBy == nil || *removed.RemovedBy != RemoverParticipant {
		t.Errorf("removed_by = %v, want %q", removed.RemovedBy, RemoverParticipant)
	}
	if removed.RemovedByID != moderator.ID {
		t.Errorf("removed_by_participant_id = %q, want the moderator %q", removed.RemovedByID, moderator.ID)
	}
}

/*
TestAModeratorsAuthorityDoesNotReachAnotherCall closes the obvious way to
borrow authority: naming a moderator from somewhere else.
*/
func TestAModeratorsAuthorityDoesNotReachAnotherCall(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	elsewhere := setup.newCall(t, setup.first, nil)
	outsider := setup.join(t, setup.first, elsewhere.ID,
		setup.newUser(t, setup.first, "ada"), RoleModerator)

	call := setup.newCall(t, setup.first, nil)
	target := setup.join(t, setup.first, call.ID, setup.newUser(t, setup.first, "grace"), RoleMember)

	if _, err := setup.service.Remove(ctx, setup.first, target.ID,
		RemoverApplication, outsider.ID, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Remove() error = %v, want %v", err, ErrNotFound)
	}
}

/*
TestAModeratorWhoLeftHasNoAuthority proves the role travels with presence.

Someone who is gone cannot act on a conversation they are no longer in.
*/
func TestAModeratorWhoLeftHasNoAuthority(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	call := setup.newCall(t, setup.first, nil)
	moderator := setup.join(t, setup.first, call.ID, setup.newUser(t, setup.first, "ada"), RoleModerator)
	target := setup.join(t, setup.first, call.ID, setup.newUser(t, setup.first, "grace"), RoleMember)

	if _, err := setup.service.Leave(ctx, setup.first, moderator.ID); err != nil {
		t.Fatalf("Leave() error = %v", err)
	}

	if _, err := setup.service.Remove(ctx, setup.first, target.ID,
		RemoverApplication, moderator.ID, ""); !errors.Is(err, ErrGone) {
		t.Fatalf("Remove() error = %v, want %v", err, ErrGone)
	}
}

/*
TestPromotingSomeoneIsGuardedLikeARemoval proves the moderator role guards the
authority that creates more moderators.
*/
func TestPromotingSomeoneIsGuardedLikeARemoval(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	call := setup.newCall(t, setup.first, nil)
	moderator := setup.join(t, setup.first, call.ID, setup.newUser(t, setup.first, "ada"), RoleModerator)
	member := setup.join(t, setup.first, call.ID, setup.newUser(t, setup.first, "grace"), RoleMember)

	if _, err := setup.service.SetRole(ctx, setup.first, member.ID,
		string(RoleModerator), member.ID); !errors.Is(err, ErrNotAModerator) {
		t.Fatalf("SetRole() named by a member error = %v, want %v", err, ErrNotAModerator)
	}

	promoted, err := setup.service.SetRole(ctx, setup.first, member.ID,
		string(RoleModerator), moderator.ID)
	if err != nil {
		t.Fatalf("SetRole() error = %v", err)
	}
	if promoted.Role != RoleModerator {
		t.Errorf("role = %q, want %q", promoted.Role, RoleModerator)
	}

	// The newly promoted moderator can now act.
	if _, err := setup.service.Remove(ctx, setup.first, moderator.ID,
		RemoverApplication, member.ID, ""); err != nil {
		t.Fatalf("Remove() by the promoted moderator error = %v", err)
	}
}

/*
TestAnAbsentRoleIsTheLesserOne proves the safe default.

A mistyped or omitted field must never hand someone the authority to remove
other people, and changing a role must be stated rather than implied.
*/
func TestAnAbsentRoleIsTheLesserOne(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	call := setup.newCall(t, setup.first, nil)
	participant := setup.join(t, setup.first, call.ID, setup.newUser(t, setup.first, "ada"), "")

	if participant.Role != RoleMember {
		t.Errorf("role = %q, want a join without a role to be %q", participant.Role, RoleMember)
	}

	var validation ValidationError
	if _, err := setup.service.SetRole(ctx, setup.first, participant.ID, "", ""); !errors.As(err, &validation) {
		t.Fatalf("SetRole() with no role error = %v, want a ValidationError", err)
	}
	if _, _, err := setup.service.Join(ctx, setup.first, call.ID,
		Admission{UserID: setup.newUser(t, setup.first, "grace"), Role: "owner"}); !errors.As(err, &validation) {
		t.Fatalf("Join() with an unknown role error = %v, want a ValidationError", err)
	}
}

/*
TestAnEndedCallTakesNoOneNewAndLosesNobody proves the participants of a
finished conversation are its history, and history does not change.
*/
func TestAnEndedCallTakesNoOneNewAndLosesNobody(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	call := setup.newCall(t, setup.first, nil)
	participant := setup.join(t, setup.first, call.ID, setup.newUser(t, setup.first, "ada"), RoleMember)

	if _, err := setup.calls.End(ctx, setup.first, call.ID, calls.ActorApplication, ""); err != nil {
		t.Fatalf("End() error = %v", err)
	}

	if _, _, err := setup.service.Join(ctx, setup.first, call.ID,
		Admission{UserID: setup.newUser(t, setup.first, "grace")}); !errors.Is(err, ErrCallEnded) {
		t.Fatalf("Join() error = %v, want %v", err, ErrCallEnded)
	}

	roster, err := setup.service.List(ctx, setup.first, call.ID, ListOptions{})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(roster.Participants) != 1 || roster.Participants[0].ID != participant.ID {
		t.Error("the roster of an ended call no longer shows who was in it")
	}
}

/*
TestASuspendedPersonCannotJoin keeps a suspension from being decorative.

Suspension exists to stop a person being served, and letting them into a
conversation would be serving them.
*/
func TestASuspendedPersonCannotJoin(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	call := setup.newCall(t, setup.first, nil)
	user := setup.newUser(t, setup.first, "ada")

	if _, err := setup.users.Suspend(ctx, setup.first, user); err != nil {
		t.Fatalf("Suspend() error = %v", err)
	}

	if _, _, err := setup.service.Join(ctx, setup.first, call.ID,
		Admission{UserID: user}); !errors.Is(err, ErrUserSuspended) {
		t.Fatalf("Join() error = %v, want %v", err, ErrUserSuspended)
	}
}

/*
TestAnUnknownPersonCannotJoin proves a participant is someone the application
already told Convia about, rather than an identifier accepted on faith.
*/
func TestAnUnknownPersonCannotJoin(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	call := setup.newCall(t, setup.first, nil)

	// Somebody else's user is as unknown here as one that never existed.
	stranger := setup.newUser(t, setup.second, "ada")

	for name, id := range map[string]string{
		"another tenant's user": stranger,
		"an unknown user":       "usr_AAAAAAAAAAAAAAAAAAAAAAAAAA",
		"a malformed id":        "not-an-id",
	} {
		t.Run(name, func(t *testing.T) {
			_, _, err := setup.service.Join(ctx, setup.first, call.ID, Admission{UserID: id})
			if !errors.Is(err, ErrUserNotFound) {
				t.Errorf("Join() error = %v, want %v", err, ErrUserNotFound)
			}
		})
	}
}

/*
TestOneApplicationCannotReachAnothersRoster is the isolation guarantee.

Crossing answers "not found" rather than "forbidden", because forbidden would
confirm the participant exists.
*/
func TestOneApplicationCannotReachAnothersRoster(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	call := setup.newCall(t, setup.first, nil)
	participant := setup.join(t, setup.first, call.ID, setup.newUser(t, setup.first, "ada"), RoleMember)

	if _, err := setup.service.Get(ctx, setup.second, participant.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get() across tenants error = %v, want %v", err, ErrNotFound)
	}
	if _, err := setup.service.Leave(ctx, setup.second, participant.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("Leave() across tenants error = %v, want %v", err, ErrNotFound)
	}
	if _, err := setup.service.Remove(ctx, setup.second, participant.ID,
		RemoverApplication, "", ""); !errors.Is(err, ErrNotFound) {
		t.Errorf("Remove() across tenants error = %v, want %v", err, ErrNotFound)
	}
	if _, err := setup.service.List(ctx, setup.second, call.ID, ListOptions{}); !errors.Is(err, ErrCallNotFound) {
		t.Errorf("List() across tenants error = %v, want %v", err, ErrCallNotFound)
	}

	intact, err := setup.service.Get(ctx, setup.first, participant.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if !intact.Present() {
		t.Error("the other tenant's attempt changed the participant")
	}
}

/*
TestTheRosterPagesNewestFirst proves the listing is a record of who was there,
not only of who is there now.
*/
func TestTheRosterPagesNewestFirst(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	call := setup.newCall(t, setup.first, nil)

	var ordered []string
	for index := range 5 {
		participant := setup.join(t, setup.first, call.ID,
			setup.newUser(t, setup.first, "person-"+strconv.Itoa(index)), RoleMember)
		ordered = append(ordered, participant.ID)
	}

	first, err := setup.service.List(ctx, setup.first, call.ID, ListOptions{Limit: 2})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(first.Participants) != 2 || first.NextCursor == "" {
		t.Fatalf("first page held %d entries with cursor %q, want 2 and a continuation",
			len(first.Participants), first.NextCursor)
	}
	if first.Participants[0].ID != ordered[4] {
		t.Errorf("first result = %s, want the newest arrival %s", first.Participants[0].ID, ordered[4])
	}

	var seen []string
	cursor := ""
	for range 5 {
		page, err := setup.service.List(ctx, setup.first, call.ID, ListOptions{Limit: 2, Cursor: cursor})
		if err != nil {
			t.Fatalf("List() error = %v", err)
		}
		for _, participant := range page.Participants {
			seen = append(seen, participant.ID)
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}

	if len(seen) != len(ordered) {
		t.Fatalf("paging saw %d entries, want %d", len(seen), len(ordered))
	}
	for index, id := range seen {
		want := ordered[len(ordered)-1-index]
		if id != want {
			t.Errorf("position %d = %s, want %s", index, id, want)
		}
	}
}

/*
TestTheStatusFilterAnswersWhoIsHereNow is how a caller reads a live roster
without a separate endpoint for it.
*/
func TestTheStatusFilterAnswersWhoIsHereNow(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	call := setup.newCall(t, setup.first, nil)
	present := setup.join(t, setup.first, call.ID, setup.newUser(t, setup.first, "ada"), RoleMember)
	departed := setup.join(t, setup.first, call.ID, setup.newUser(t, setup.first, "grace"), RoleMember)
	ejected := setup.join(t, setup.first, call.ID, setup.newUser(t, setup.first, "alan"), RoleMember)

	if _, err := setup.service.Leave(ctx, setup.first, departed.ID); err != nil {
		t.Fatalf("Leave() error = %v", err)
	}
	if _, err := setup.service.Remove(ctx, setup.first, ejected.ID, RemoverApplication, "", ""); err != nil {
		t.Fatalf("Remove() error = %v", err)
	}

	expected := map[Status]string{
		StatusJoined:  present.ID,
		StatusLeft:    departed.ID,
		StatusRemoved: ejected.ID,
	}
	for status, id := range expected {
		page, err := setup.service.List(ctx, setup.first, call.ID, ListOptions{Status: string(status)})
		if err != nil {
			t.Fatalf("List(%q) error = %v", status, err)
		}
		if len(page.Participants) != 1 || page.Participants[0].ID != id {
			t.Errorf("the %q filter returned %d entries, want only %s", status, len(page.Participants), id)
		}
	}

	everyone, err := setup.service.List(ctx, setup.first, call.ID, ListOptions{})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(everyone.Participants) != 3 {
		t.Errorf("an unfiltered roster returned %d entries, want all three", len(everyone.Participants))
	}

	if _, err := setup.service.List(ctx, setup.first, call.ID, ListOptions{Status: "present"}); err == nil {
		t.Error("a status Convia does not recognize was accepted as a filter")
	}
}

/*
TestASuspendedTenantIsNotServed proves the tenant check reaches every operation.
*/
func TestASuspendedTenantIsNotServed(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	call := setup.newCall(t, setup.first, nil)
	participant := setup.join(t, setup.first, call.ID, setup.newUser(t, setup.first, "ada"), RoleMember)

	if _, err := setup.applications.Suspend(ctx, setup.first); err != nil {
		t.Fatalf("Suspend() error = %v", err)
	}

	if _, _, err := setup.service.Join(ctx, setup.first, call.ID, Admission{UserID: "usr_AAAAAAAAAAAAAAAAAAAAAAAAAA"}); !errors.Is(err, ErrApplicationNotFound) {
		t.Errorf("Join() error = %v, want %v", err, ErrApplicationNotFound)
	}
	if _, err := setup.service.Get(ctx, setup.first, participant.ID); !errors.Is(err, ErrApplicationNotFound) {
		t.Errorf("Get() error = %v, want %v", err, ErrApplicationNotFound)
	}
	if _, err := setup.service.List(ctx, setup.first, call.ID, ListOptions{}); !errors.Is(err, ErrApplicationNotFound) {
		t.Errorf("List() error = %v, want %v", err, ErrApplicationNotFound)
	}
	if _, err := setup.service.Leave(ctx, setup.first, participant.ID); !errors.Is(err, ErrApplicationNotFound) {
		t.Errorf("Leave() error = %v, want %v", err, ErrApplicationNotFound)
	}
}

/*
TestTheAuditRecordsTheRosterWithoutTheLabels keeps application text out of the
audit trail.

Every value recorded is one Convia assigned. The removal reason is composed by
the application and may say something about the person removed.
*/
func TestTheAuditRecordsTheRosterWithoutTheLabels(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	const secretReason = "harassed-another-attendee"

	call := setup.newCall(t, setup.first, nil)
	user := setup.newUser(t, setup.first, "ada")
	participant := setup.join(t, setup.first, call.ID, user, RoleMember)

	if _, err := setup.service.Remove(ctx, setup.first, participant.ID,
		RemoverApplication, "", secretReason); err != nil {
		t.Fatalf("Remove() error = %v", err)
	}

	recorded := setup.logs.String()

	for _, event := range []string{"participant.joined", "participant.removed"} {
		if !strings.Contains(recorded, event) {
			t.Errorf("the audit trail does not record %q", event)
		}
	}
	for _, expected := range []string{participant.ID, call.ID, user, setup.first} {
		if !strings.Contains(recorded, expected) {
			t.Errorf("the audit trail does not record %q", expected)
		}
	}
	if strings.Contains(recorded, secretReason) {
		t.Errorf("the audit trail recorded application-composed text: %q", secretReason)
	}
}

/*
TestAnInvalidIdentifierIsMissingRatherThanAnError proves a malformed identifier
is answered the same way an unknown one is, so probing tells a caller nothing.
*/
func TestAnInvalidIdentifierIsMissingRatherThanAnError(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	for _, id := range []string{"", "call_7KQZP4XN2VJH6TBWMDR3YAFC5E", "part_lowercase", "not-an-id"} {
		if _, err := setup.service.Get(ctx, setup.first, id); !errors.Is(err, ErrNotFound) {
			t.Errorf("Get(%q) error = %v, want %v", id, err, ErrNotFound)
		}
	}
}
