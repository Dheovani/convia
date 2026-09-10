package presence

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"convia/internal/credentials"
	"convia/internal/events"
	"convia/internal/users"
)

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// servedTenant is an application Convia is serving, which is what every test
// here needs the tenancy domain to say.
type servedTenant struct{ active bool }

func (tenant servedTenant) Active(context.Context, string) (bool, error) {
	return tenant.active, nil
}

// knownPeople is the identity domain answering about one person.
type knownPeople struct {
	user users.User
	err  error
}

func (people knownPeople) Get(context.Context, string, string) (users.User, error) {
	return people.user, people.err
}

// heard records the events a service published, which is what most of these
// tests are actually about.
type heard struct{ events []events.Event }

func (sink *heard) Publish(_ context.Context, event events.Event) {
	sink.events = append(sink.events, event)
}

// unreachable is a store that cannot be reached, which is the one failure
// presence has no durable answer for.
type unreachable struct{}

var errNoStore = errors.New("dial tcp: connection refused")

func (unreachable) Assert(context.Context, string, string, Assertion) (Change, error) {
	return Change{}, errNoStore
}
func (unreachable) Clear(context.Context, string, string, string) (Change, error) {
	return Change{}, errNoStore
}
func (unreachable) Get(context.Context, string, string) (Presence, error) {
	return Presence{}, errNoStore
}
func (unreachable) GetMany(context.Context, string, []string) ([]Presence, error) {
	return nil, errNoStore
}
func (unreachable) Lapse(context.Context, int) ([]Change, error) { return nil, errNoStore }

// serving builds a service over an in-process store, with the people and the
// listener a test wants.
func serving(t *testing.T, store Store, person users.User) (*Service, *heard) {
	t.Helper()

	sink := &heard{}
	return NewService(store, servedTenant{active: true}, knownPeople{user: person}, sink, quiet()), sink
}

// active is somebody the application is serving.
func active() users.User {
	return users.User{ID: "usr_1", ApplicationID: "app_1", Status: users.StatusActive}
}

/*
TestOnlyAMoveIsAnnounced is the volume decision, checked at the level that
makes it.

A heartbeat arrives every twenty seconds per person per device. Announcing each
one would make presence the loudest thing on every stream of the tenant while
telling subscribers nothing they did not have.
*/
func TestOnlyAMoveIsAnnounced(t *testing.T) {
	store, _ := held(t)
	service, sink := serving(t, store, active())
	ctx := context.Background()

	for range 5 {
		if _, err := service.Assert(ctx, "app_1", "usr_1", says("laptop", StateOnline)); err != nil {
			t.Fatalf("Assert() error = %v", err)
		}
	}

	if len(sink.events) != 1 {
		t.Fatalf("five heartbeats produced %d events, want 1", len(sink.events))
	}

	announced := sink.events[0]
	if announced.Type != events.PresenceChanged {
		t.Errorf("announced %q, want %q", announced.Type, events.PresenceChanged)
	}
	if announced.Subject.Type != events.SubjectUser || announced.Subject.ID != "usr_1" {
		t.Errorf("announced about %v, want the user usr_1", announced.Subject)
	}
	if announced.Data["state"] != string(StateOnline) {
		t.Errorf("announced state %v, want %q", announced.Data["state"], StateOnline)
	}
	if announced.Data["previous"] != string(StateOffline) {
		t.Errorf("announced previous %v, want %q", announced.Data["previous"], StateOffline)
	}
}

/*
TestAnEventCarriesNothingAboutTheDevice keeps an application-composed value out
of a stream that reaches every subscriber of a tenant at once.

The device identifier is the application's own string. It is bounded and
validated, but it is not a value Convia assigned, and internal/events says why
that line matters.
*/
func TestAnEventCarriesNothingAboutTheDevice(t *testing.T) {
	store, _ := held(t)
	service, sink := serving(t, store, active())

	if _, err := service.Assert(context.Background(), "app_1", "usr_1",
		says("ana-macbook-pro-2019", StateBusy)); err != nil {
		t.Fatalf("Assert() error = %v", err)
	}

	if len(sink.events) != 1 {
		t.Fatalf("published %d events, want 1", len(sink.events))
	}
	for key, value := range sink.events[0].Data {
		if text, isText := value.(string); isText && text == "ana-macbook-pro-2019" {
			t.Errorf("the event carries the device identifier under %q", key)
		}
	}
}

/*
TestSweepingAnnouncesTheDepartureNothingElseWould is the reason the sweeper
exists.

Leaving on purpose is announced by the operation that did it. Going quiet is
announced by nothing, because nothing happened — which is exactly the case an
application cannot detect for itself.
*/
func TestSweepingAnnouncesTheDepartureNothingElseWould(t *testing.T) {
	store, advance := held(t)
	service, sink := serving(t, store, active())
	ctx := context.Background()

	if _, err := service.Assert(ctx, "app_1", "usr_1", says("laptop", StateOnline)); err != nil {
		t.Fatalf("Assert() error = %v", err)
	}
	advance(DefaultLifetime + time.Second)

	announced, err := service.Sweep(ctx, 100)
	if err != nil {
		t.Fatalf("Sweep() error = %v", err)
	}
	if announced != 1 {
		t.Fatalf("swept %d departures, want 1", announced)
	}

	last := sink.events[len(sink.events)-1]
	if last.Data["state"] != string(StateOffline) || last.Data["previous"] != string(StateOnline) {
		t.Errorf("swept event says %v -> %v, want online -> offline", last.Data["previous"], last.Data["state"])
	}
}

/*
TestASuspendedUserIsNotReportedAsAvailable keeps a suspension from being
decorative.

Suspension exists to stop somebody being served. Refusing the assertion lets
the claim they already have lapse on the timer that is already running, which
is the quietest correct outcome.
*/
func TestASuspendedUserIsNotReportedAsAvailable(t *testing.T) {
	suspended := active()
	suspended.Status = users.StatusSuspended

	store, _ := held(t)
	service, _ := serving(t, store, suspended)

	if _, err := service.Assert(context.Background(), "app_1", "usr_1",
		says("laptop", StateOnline)); !errors.Is(err, ErrUserSuspended) {
		t.Errorf("Assert() for a suspended user error = %v, want %v", err, ErrUserSuspended)
	}
}

/*
TestPresenceIsOnlyForPeopleTheApplicationHas stops the ephemeral store from
becoming scratch space keyed by whatever a caller liked.

Without the lookup, an application could assert presence for any string of the
right shape and read it back, which is a general-purpose key-value store
wearing a roster's clothes.
*/
func TestPresenceIsOnlyForPeopleTheApplicationHas(t *testing.T) {
	store, _ := held(t)
	service := NewService(store, servedTenant{active: true},
		knownPeople{err: users.ErrNotFound}, &heard{}, quiet())
	ctx := context.Background()

	if _, err := service.Assert(ctx, "app_1", "usr_X", says("laptop", StateOnline)); !errors.Is(err, ErrNotFound) {
		t.Errorf("Assert() for a stranger error = %v, want %v", err, ErrNotFound)
	}
	if _, err := service.Get(ctx, "app_1", "usr_X"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get() for a stranger error = %v, want %v", err, ErrNotFound)
	}
}

// TestASuspendedTenantIsReportedAsAbsent keeps one application from learning
// anything about another from the difference between the two answers.
func TestASuspendedTenantIsReportedAsAbsent(t *testing.T) {
	store, _ := held(t)
	service := NewService(store, servedTenant{active: false},
		knownPeople{user: active()}, &heard{}, quiet())

	if _, err := service.Get(context.Background(), "app_1", "usr_1"); !errors.Is(err, ErrApplicationNotFound) {
		t.Errorf("Get() for a suspended tenant error = %v, want %v", err, ErrApplicationNotFound)
	}
}

/*
TestAnUnreachableStoreIsSaidRatherThanAnsweredAsOffline is the one place
presence differs from everything else in Convia.

Everywhere else a store that cannot be reached has a durable answer behind it.
Presence has none, so answering "offline" would be a false statement about
people rather than a degraded one, and an application would act on it.
*/
func TestAnUnreachableStoreIsSaidRatherThanAnsweredAsOffline(t *testing.T) {
	service := NewService(unreachable{}, servedTenant{active: true},
		knownPeople{user: active()}, &heard{}, quiet())
	ctx := context.Background()

	if _, err := service.Get(ctx, "app_1", "usr_1"); !errors.Is(err, ErrUnavailable) {
		t.Errorf("Get() with no store error = %v, want %v", err, ErrUnavailable)
	}
	if _, err := service.Assert(ctx, "app_1", "usr_1", says("laptop", StateOnline)); !errors.Is(err, ErrUnavailable) {
		t.Errorf("Assert() with no store error = %v, want %v", err, ErrUnavailable)
	}
}

// TestReadingIsBounded keeps presence from being used to export a directory.
func TestReadingIsBounded(t *testing.T) {
	store, _ := held(t)
	service, _ := serving(t, store, active())
	ctx := context.Background()

	if _, err := service.GetMany(ctx, "app_1", nil); err == nil {
		t.Error("GetMany() accepted a request naming nobody")
	}

	tooMany := make([]string, MaxUsersPerRead+1)
	for index := range tooMany {
		tooMany[index] = "usr_1"
	}
	if _, err := service.GetMany(ctx, "app_1", tooMany); err == nil {
		t.Errorf("GetMany() accepted %d users", len(tooMany))
	}
}

/*
TestReadingAndWritingAreSeparatePermissions is the split M17-005 asks for,
checked where it is enforced.

An application that reports presence from its session tier and reads it from
its API tier gives each of them one of the two. A key that can say where
somebody is should not thereby be able to ask.
*/
func TestReadingAndWritingAreSeparatePermissions(t *testing.T) {
	store, _ := held(t)
	service, _ := serving(t, store, active())
	ctx := context.Background()

	writer := Authorize(service, credentials.Principal{
		ApplicationID: "app_1", Scopes: []credentials.Scope{credentials.ScopePresenceWrite}})
	reader := Authorize(service, credentials.Principal{
		ApplicationID: "app_1", Scopes: []credentials.Scope{credentials.ScopePresenceRead}})

	if _, err := writer.Assert(ctx, "usr_1", says("laptop", StateOnline)); err != nil {
		t.Errorf("a writer could not assert: %v", err)
	}
	if _, err := writer.Get(ctx, "usr_1"); !errors.Is(err, ErrForbidden) {
		t.Errorf("a writer could read: %v", err)
	}
	if _, err := reader.Get(ctx, "usr_1"); err != nil {
		t.Errorf("a reader could not read: %v", err)
	}
	if _, err := reader.Clear(ctx, "usr_1", "laptop"); !errors.Is(err, ErrForbidden) {
		t.Errorf("a reader could withdraw: %v", err)
	}
}

/*
TestTheTenantComesFromTheCredential is the tenancy guarantee stated as an
absence.

There is no request field naming an application anywhere on this surface, so
the check is that the authorized service uses the principal's and nothing else
could be substituted.
*/
func TestTheTenantComesFromTheCredential(t *testing.T) {
	store, _ := held(t)
	service, _ := serving(t, store, active())
	ctx := context.Background()

	first := Authorize(service, credentials.Principal{ApplicationID: "app_1",
		Scopes: []credentials.Scope{credentials.ScopePresenceRead, credentials.ScopePresenceWrite}})
	second := Authorize(service, credentials.Principal{ApplicationID: "app_2",
		Scopes: []credentials.Scope{credentials.ScopePresenceRead}})

	if _, err := first.Assert(ctx, "usr_1", says("laptop", StateBusy)); err != nil {
		t.Fatalf("Assert() error = %v", err)
	}

	presence, err := second.Get(ctx, "usr_1")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if presence.State != StateOffline {
		t.Errorf("another tenant reads %q, want %q", presence.State, StateOffline)
	}
}
