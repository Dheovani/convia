package presence

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"convia/internal/api"
	"convia/internal/events"
	"convia/internal/users"
)

/*
MaxUsersPerRead bounds how many people one read may ask about.

A roster is the ordinary read and it has a size. This is the point past which a
caller is not drawing a list but exporting one, which presence is not for.
*/
const MaxUsersPerRead = 100

/*
tenants is the behavior this package needs from the tenancy domain.

It asks whether an application is being served rather than whether it exists,
so that presence cannot be asserted or read for a tenant Convia has stopped
serving.
*/
type tenants interface {
	Active(ctx context.Context, applicationID string) (bool, error)
}

/*
userLookup is the behavior this package needs from the identity domain.

Presence is about a person the application already told Convia about. Without
this, an application could assert presence for any string of the right shape
and use the ephemeral store as scratch space keyed by whatever it liked.

It is a read on the write path, which is the one place in Convia where a
heartbeat touches PostgreSQL. It is a lookup by primary key, and the
alternative — trusting the identifier — is how presence would stop being about
people.
*/
type userLookup interface {
	Get(ctx context.Context, applicationID, id string) (users.User, error)
}

/*
announcer is the behavior this package needs to say what changed.

It returns no error, for the reason internal/events/announcer.go gives: telling
somebody that presence moved must not be able to undo the move.
*/
type announcer interface {
	Publish(ctx context.Context, event events.Event)
}

/*
ErrUserSuspended reports a person the application has withdrawn.

Suspension exists to stop somebody being served. Continuing to report them as
available would make the suspension decorative, and refusing the assertion lets
them lapse to offline on the timer that is already running.
*/
var ErrUserSuspended = errors.New("the user is not active")

// Service applies Convia's rules for who is available.
type Service struct {
	store   Store
	tenants tenants
	users   userLookup
	stream  announcer
	logger  *slog.Logger
}

func NewService(store Store, owner tenants, people userLookup,
	stream announcer, logger *slog.Logger) *Service {
	return &Service{store: store, tenants: owner, users: people, stream: stream, logger: logger}
}

/*
Assert records or refreshes what one device says about one person.

The lifetime is Convia's to bound and the deadline is the store's to compute,
so nothing a caller sends decides when this lapses. What comes back is the
person's presence, not the device's: an application that asked one device to
report `away` while another is active is told the person is online, which is
the answer it would have had to work out for itself otherwise.
*/
func (service *Service) Assert(ctx context.Context, applicationID, userID string,
	assertion Assertion) (Presence, error) {
	if err := service.requireActiveUser(ctx, applicationID, userID); err != nil {
		return Presence{}, err
	}

	change, err := service.store.Assert(ctx, applicationID, userID, assertion)
	if err != nil {
		return Presence{}, service.wrap("assert presence", err)
	}

	service.announce(ctx, change)
	return change.After, nil
}

/*
Clear withdraws what one device said, or everything said about a person.

An empty device identifier is the second: an application saying somebody signed
out rather than closed one tab. Withdrawing a claim that was never made
succeeds and changes nothing, because a client that lost its connection and
retried its own sign-out must not receive an error for tidying up twice.
*/
func (service *Service) Clear(ctx context.Context, applicationID, userID, deviceID string) (Presence, error) {
	if err := service.requireUser(ctx, applicationID, userID); err != nil {
		return Presence{}, err
	}

	change, err := service.store.Clear(ctx, applicationID, userID, deviceID)
	if err != nil {
		return Presence{}, service.wrap("clear presence", err)
	}

	service.announce(ctx, change)
	return change.After, nil
}

/*
Get reports what Convia will say about one person.

A person nothing has asserted about is offline rather than absent, which is why
this cannot return ErrNotFound for a user that exists. The distinction matters
to a client: "Convia knows nobody by that name" and "nobody is saying anything
about them" are different answers, and only the first is a mistake.
*/
func (service *Service) Get(ctx context.Context, applicationID, userID string) (Presence, error) {
	if err := service.requireUser(ctx, applicationID, userID); err != nil {
		return Presence{}, err
	}

	presence, err := service.store.Get(ctx, applicationID, userID)
	if err != nil {
		return Presence{}, service.wrap("read presence", err)
	}
	return presence, nil
}

/*
GetMany reports what Convia will say about several people, in the order asked.

Every named user is answered for, including the ones nothing is asserting
about, so a client drawing a roster gets a row per person rather than having to
work out which of its identifiers went missing. A user the application does not
have is refused rather than answered as offline: it is a mistake in the
request, and reporting it as an absence would hide a typo forever.
*/
func (service *Service) GetMany(ctx context.Context, applicationID string, userIDs []string) ([]Presence, error) {
	if err := service.requireApplication(ctx, applicationID); err != nil {
		return nil, err
	}

	if len(userIDs) == 0 {
		return nil, ValidationError{
			Field:   "user_id",
			Message: "Name at least one user to read presence for.",
		}
	}
	if len(userIDs) > MaxUsersPerRead {
		return nil, ValidationError{
			Field:   "user_id",
			Message: fmt.Sprintf("Presence may be read for at most %d users at a time.", MaxUsersPerRead),
		}
	}

	for _, userID := range userIDs {
		if _, err := service.users.Get(ctx, applicationID, userID); err != nil {
			return nil, translateUserError(err)
		}
	}

	answers, err := service.store.GetMany(ctx, applicationID, userIDs)
	if err != nil {
		return nil, service.wrap("read presence", err)
	}
	return answers, nil
}

/*
Sweep announces the presence that lapsed since it last ran.

It is the other half of expiry. A read already ignores a claim past its
deadline, so this changes no answer; what it changes is whether anybody is
told. Without it, a subscriber that saw somebody arrive would never see them
leave unless they left on purpose — and leaving on purpose is the case that
does not need announcing, because the application did it.

Claiming an expired entry is exclusive across the deployment, so one instance
announces and the others find nothing, whichever of them happens to look first.
*/
func (service *Service) Sweep(ctx context.Context, limit int) (int, error) {
	lapsed, err := service.store.Lapse(ctx, limit)
	if err != nil {
		return 0, service.wrap("sweep lapsed presence", err)
	}

	announced := 0
	for _, change := range lapsed {
		if change.Moved() {
			announced++
		}
		service.announce(ctx, change)
	}
	return announced, nil
}

/*
announce publishes a change an application would notice, and nothing else.

A heartbeat that refreshes a deadline without moving the state is the
overwhelming majority of presence writes, and every one of them would otherwise
become an event on every stream of the tenant, carried to every instance, for a
client to compare against what it already had. Filtering here rather than at
the subscriber is what keeps presence from being the loudest thing Convia
delivers while saying the least.

Presence is not audited. Everything else Convia announces is written to the
audit log beside the event, because it is a change to a record; this is a claim
with a timer, it arrives thousands of times more often, and where a person is
at a given minute is the last thing that should end up in a durable log.
*/
func (service *Service) announce(ctx context.Context, change Change) {
	if !change.Moved() {
		return
	}

	service.stream.Publish(ctx, events.New(events.PresenceChanged, change.ApplicationID, change.UserID,
		api.RequestIDFromContext(ctx), events.Data{
			"state":    string(change.After.State),
			"previous": string(change.Before.State),
		}))
}

// requireUser refuses presence for somebody the application does not have.
func (service *Service) requireUser(ctx context.Context, applicationID, userID string) error {
	if err := service.requireApplication(ctx, applicationID); err != nil {
		return err
	}

	if _, err := service.users.Get(ctx, applicationID, userID); err != nil {
		return translateUserError(err)
	}
	return nil
}

// requireActiveUser additionally refuses presence for somebody the application
// has withdrawn.
func (service *Service) requireActiveUser(ctx context.Context, applicationID, userID string) error {
	if err := service.requireApplication(ctx, applicationID); err != nil {
		return err
	}

	person, err := service.users.Get(ctx, applicationID, userID)
	if err != nil {
		return translateUserError(err)
	}
	if person.Status != users.StatusActive {
		return ErrUserSuspended
	}
	return nil
}

/*
requireApplication refuses presence for a tenant Convia has stopped serving.

A suspended application is reported as absent rather than suspended, so that
one tenant learns nothing about another from the difference.
*/
func (service *Service) requireApplication(ctx context.Context, applicationID string) error {
	active, err := service.tenants.Active(ctx, applicationID)
	if err != nil {
		return fmt.Errorf("check the application: %w", err)
	}
	if !active {
		return ErrApplicationNotFound
	}
	return nil
}

/*
translateUserError reports an identity failure in this package's terms.

A user the application does not have and an application that no longer exists
are both reported as their own absence, so that neither answer describes
anything a caller could not already see.
*/
func translateUserError(err error) error {
	switch {
	case errors.Is(err, users.ErrNotFound):
		return ErrNotFound
	case errors.Is(err, users.ErrApplicationNotFound):
		return ErrApplicationNotFound
	default:
		return fmt.Errorf("look up the user: %w", err)
	}
}

/*
wrap reports a store failure as the one condition presence has no answer for.

Everything else in Convia can fall back on PostgreSQL. Presence cannot: if the
ephemeral store is unreachable there is no second copy, and answering "offline"
would be a false statement about people rather than a degraded one. So the
failure is reported, and the caller is told to try again.
*/
func (service *Service) wrap(what string, err error) error {
	if errors.Is(err, ErrTooManyDevices) {
		return err
	}

	service.logger.Error("the presence store could not be reached", "error", err, "operation", what)
	return fmt.Errorf("%s: %w", what, ErrUnavailable)
}
