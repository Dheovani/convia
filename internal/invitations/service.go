package invitations

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"convia/internal/api"
	"convia/internal/calls"
	"convia/internal/media"
	"convia/internal/participants"
	"convia/internal/secret"
	"convia/internal/users"
)

const (
	// defaultPageSize and maxPageSize implement the pagination bounds defined
	// in docs/api-conventions.md.
	defaultPageSize = 25
	maxPageSize     = 100
)

/*
tenants is the behavior this package needs from the tenancy domain.

It asks whether an application is being served rather than whether it exists,
so that an invitation stops working the moment Convia stops serving the tenant
that issued it, without anyone having to revoke every outstanding link.
*/
type tenants interface {
	Active(ctx context.Context, applicationID string) (bool, error)
}

/*
callLookup is the behavior this package needs from the call domain.

Only reading: an invitation never changes a call, and the call stays the
authority on its own state.
*/
type callLookup interface {
	Get(ctx context.Context, applicationID, id string) (calls.Call, error)
}

// userLookup confirms the person an invitation names is one Convia knows.
type userLookup interface {
	Get(ctx context.Context, applicationID, id string) (users.User, error)
}

/*
participation is the behavior this package needs to turn an invitation into a
presence.

Redeeming does not reimplement joining. Every rule about who may be in a call —
capacity, suspension, a removal that must not be undone — lives in the
participants package, and an invitation that carried its own copy of them would
be a second place to be right about one thing.
*/
type participation interface {
	Join(ctx context.Context, applicationID, callID string,
		admission participants.Admission) (participants.Participant, bool, error)
	Session(ctx context.Context, applicationID, id string) (participants.Participant, media.Credential, error)
}

// Service applies Convia's rules for invitations.
type Service struct {
	store        *Store
	tenants      tenants
	calls        callLookup
	users        userLookup
	participants participation
	logger       *slog.Logger
}

func NewService(store *Store, owner tenants, conversations callLookup, people userLookup,
	presence participation, logger *slog.Logger) *Service {
	return &Service{
		store:        store,
		tenants:      owner,
		calls:        conversations,
		users:        people,
		participants: presence,
		logger:       logger,
	}
}

/*
Request is what an application asks for when inviting someone.

The role is the one the invitation confers when it is redeemed, and it is the
participants' own vocabulary rather than a second one.
*/
type Request struct {
	UserID    string
	Role      string
	ExpiresIn time.Duration
}

// ListOptions selects one page of an application's invitations.
type ListOptions struct {
	Limit  int
	Cursor string
	CallID string
}

// Page is one page of invitations and the token that continues it.
type Page struct {
	Invitations []Invitation
	NextCursor  string
}

/*
Issue creates an invitation and returns the secret exactly once.

The secret is generated here, stored only as a digest, and returned to the
caller in the same breath. Convia cannot show it again, which is the same
promise it makes about an application key: a credential nobody but the holder
has is one a database dump does not reveal.
*/
func (service *Service) Issue(ctx context.Context, applicationID, callID string,
	request Request) (Invitation, secret.Value, error) {

	role, err := participants.ParseRole(request.Role)
	if err != nil {
		return Invitation{}, "", translateRole(err)
	}

	lifetime, err := NormalizeLifetime(request.ExpiresIn)
	if err != nil {
		return Invitation{}, "", err
	}

	call, err := service.requireCall(ctx, applicationID, callID)
	if err != nil {
		return Invitation{}, "", err
	}
	if call.Ended() {
		return Invitation{}, "", ErrCallEnded
	}

	if err := service.requireUser(ctx, applicationID, request.UserID); err != nil {
		return Invitation{}, "", err
	}

	issued := now()
	invitation := Invitation{
		ID:            NewID(),
		ApplicationID: applicationID,
		CallID:        call.ID,
		UserID:        request.UserID,
		Role:          string(role),
		ExpiresAt:     issued.Add(lifetime),
		CreatedAt:     issued,
		UpdatedAt:     issued,
	}

	value := secret.New()
	if err := service.store.Create(ctx, invitation, secret.Digest(value)); err != nil {
		return Invitation{}, "", err
	}

	service.audit(ctx, "invitation.issued", invitation)
	return invitation, value, nil
}

// Get returns one invitation of an application.
func (service *Service) Get(ctx context.Context, applicationID, id string) (Invitation, error) {
	if err := service.requireApplication(ctx, applicationID); err != nil {
		return Invitation{}, err
	}
	if !ValidID(id) {
		return Invitation{}, ErrNotFound
	}
	return service.store.Get(ctx, applicationID, id)
}

// List returns one page of an application's invitations, newest first.
func (service *Service) List(ctx context.Context, applicationID string, options ListOptions) (Page, error) {
	if err := service.requireApplication(ctx, applicationID); err != nil {
		return Page{}, err
	}

	if options.CallID != "" {
		if _, err := service.requireCall(ctx, applicationID, options.CallID); err != nil {
			return Page{}, err
		}
	}

	limit, err := pageSize(options.Limit)
	if err != nil {
		return Page{}, err
	}

	var cursor *Cursor
	if options.Cursor != "" {
		decoded, err := DecodeCursor(options.Cursor)
		if err != nil {
			return Page{}, err
		}
		cursor = &decoded
	}

	page, hasMore, err := service.store.List(ctx, applicationID, options.CallID, cursor, limit)
	if err != nil {
		return Page{}, err
	}

	result := Page{Invitations: page}
	if hasMore {
		last := page[len(page)-1]
		result.NextCursor = Cursor{CreatedAt: last.CreatedAt, ID: last.ID}.Encode()
	}
	return result, nil
}

/*
Revoke withdraws an invitation.

It takes effect immediately and it outranks everything, including a redemption
that already happened: an application withdrawing a link means the holder must
not be able to use it again, and the ordinary reason to revoke one is that it
reached somebody it should not have.

Revoking does not remove anyone already in the call. Somebody who joined is a
participant, and putting them out is a decision about a participant rather than
about a piece of paper they used to get in.
*/
func (service *Service) Revoke(ctx context.Context, applicationID, id string) (Invitation, error) {
	if err := service.requireApplication(ctx, applicationID); err != nil {
		return Invitation{}, err
	}
	if !ValidID(id) {
		return Invitation{}, ErrNotFound
	}

	revoked, err := service.store.Revoke(ctx, applicationID, id, now())
	if err != nil {
		return Invitation{}, err
	}

	service.audit(ctx, "invitation.revoked", revoked)
	return revoked, nil
}

/*
Authenticate verifies a presented invitation and returns it.

It answers who the holder is, not whether they may act. An invitation that has
expired, been revoked, or been declined still authenticates: the holder really
does hold it, and telling them apart from somebody presenting nonsense is what
lets Redeem explain that a link no longer works instead of answering the same
way it would to a stranger.

Every failure is the same failure, so a caller cannot learn whether an
identifier exists by watching which refusal comes back.
*/
func (service *Service) Authenticate(ctx context.Context, token string) (Invitation, error) {
	id, value, ok := ParseToken(token)
	if !ok {
		return Invitation{}, ErrUnauthenticated
	}

	invitation, digest, err := service.store.ForVerification(ctx, id)
	if errors.Is(err, ErrNotFound) {
		return Invitation{}, ErrUnauthenticated
	}
	if err != nil {
		return Invitation{}, fmt.Errorf("authenticate invitation: %w", err)
	}

	if !secret.Matches(digest, value) {
		return Invitation{}, ErrUnauthenticated
	}

	/*
		An invitation stops working when its application stops being served,
		without anyone having to revoke every outstanding link.
	*/
	active, err := service.tenants.Active(ctx, invitation.ApplicationID)
	if err != nil {
		return Invitation{}, fmt.Errorf("check application: %w", err)
	}
	if !active {
		return Invitation{}, ErrUnauthenticated
	}
	return invitation, nil
}

// ErrUnauthenticated reports a presented invitation Convia will not act on.
var ErrUnauthenticated = errors.New("the presented invitation does not authenticate")

/*
Redeem turns an invitation into a presence and the means to connect.

Both happen in one request because the holder has no way to reach either on
their own: they hold an invitation, not an application key, so a redemption
that produced only a participation would leave them in a call they cannot
connect to.

**Redeeming again is allowed while the invitation is usable**, and is the
ordinary case rather than an edge one. Somebody whose connection dropped opens
the same link again; joining is idempotent by the person, so they arrive at the
participation they already had, with a fresh credential.

None of the rules about who may be in a call are restated here. Capacity, a
suspended user, and a removal that must not be undone are all decided by the
participants package, which is the only place they live.
*/
func (service *Service) Redeem(ctx context.Context, invitation Invitation) (Invitation, participants.Participant, media.Credential, error) {
	if !invitation.Usable(now()) {
		return Invitation{}, participants.Participant{}, media.Credential{}, ErrUnusable
	}

	call, err := service.requireCall(ctx, invitation.ApplicationID, invitation.CallID)
	if err != nil {
		return Invitation{}, participants.Participant{}, media.Credential{}, err
	}
	if call.Ended() {
		return Invitation{}, participants.Participant{}, media.Credential{}, ErrCallEnded
	}

	participant, _, err := service.participants.Join(ctx, invitation.ApplicationID, invitation.CallID,
		participants.Admission{UserID: invitation.UserID, Role: invitation.Role})
	if err != nil {
		return Invitation{}, participants.Participant{}, media.Credential{}, err
	}

	_, credential, err := service.participants.Session(ctx, invitation.ApplicationID, participant.ID)
	if err != nil {
		return Invitation{}, participants.Participant{}, media.Credential{}, err
	}

	redeemed, err := service.store.Redeem(ctx, invitation.ID, participant.ID, now())
	if err != nil {
		/*
			The person is in the call and holds a credential; only the record
			of which invitation let them in is missing. Failing the request
			would tell them they are not in a conversation they are in, so the
			gap is logged and the redemption stands.
		*/
		service.logger.Error("a redeemed invitation was not recorded",
			"error", err,
			"invitation_id", invitation.ID,
			"participant_id", participant.ID,
			"application_id", invitation.ApplicationID,
			"request_id", api.RequestIDFromContext(ctx),
		)
		redeemed = invitation
	}

	if invitation.RedeemedAt == nil {
		service.audit(ctx, "invitation.redeemed", redeemed)
	}
	return redeemed, participant, credential, nil
}

/*
Decline records that the invitee said no.

It is terminal, and it is the invitee's own decision rather than the
application's: declining an invitation somebody sent you is not the same act as
the sender withdrawing it, and a record that confused the two would be wrong
about who changed their mind.

Declining after redeeming is refused. The person is already in the call, and a
decline would record something that did not happen.
*/
func (service *Service) Decline(ctx context.Context, invitation Invitation) (Invitation, error) {
	if !invitation.Usable(now()) {
		return Invitation{}, ErrUnusable
	}
	if invitation.RedeemedAt != nil {
		return Invitation{}, ErrAlreadyRedeemed
	}

	declined, err := service.store.Decline(ctx, invitation.ID, now())
	if err != nil {
		return Invitation{}, err
	}

	service.audit(ctx, "invitation.declined", declined)
	return declined, nil
}

// ErrAlreadyRedeemed reports an invitee declining after they have already joined.
var ErrAlreadyRedeemed = errors.New("the invitation has already been redeemed")

func (service *Service) requireCall(ctx context.Context, applicationID, callID string) (calls.Call, error) {
	call, err := service.calls.Get(ctx, applicationID, callID)
	switch {
	case errors.Is(err, calls.ErrApplicationNotFound):
		return calls.Call{}, ErrApplicationNotFound
	case errors.Is(err, calls.ErrNotFound):
		return calls.Call{}, ErrCallNotFound
	case err != nil:
		return calls.Call{}, fmt.Errorf("resolve call: %w", err)
	}
	return call, nil
}

/*
requireUser confirms the person exists without deciding whether they may join.

Suspension is deliberately not checked here. An invitation is permission for
later, and whether somebody may take part is settled when they redeem it — by
the participants package, which is where that rule lives. Refusing to issue an
invitation to a suspended user would be a second, weaker copy of a check that
already happens at the moment it matters.
*/
func (service *Service) requireUser(ctx context.Context, applicationID, userID string) error {
	if userID == "" {
		return ValidationError{Field: "user_id", Message: "The user must be stated."}
	}

	_, err := service.users.Get(ctx, applicationID, userID)
	switch {
	case errors.Is(err, users.ErrNotFound):
		return ErrUserNotFound
	case errors.Is(err, users.ErrApplicationNotFound):
		return ErrApplicationNotFound
	case err != nil:
		return fmt.Errorf("resolve user: %w", err)
	}
	return nil
}

func (service *Service) requireApplication(ctx context.Context, applicationID string) error {
	active, err := service.tenants.Active(ctx, applicationID)
	if err != nil {
		return fmt.Errorf("check application: %w", err)
	}
	if !active {
		return ErrApplicationNotFound
	}
	return nil
}

// translateRole restates the participants' own validation error as this package's.
func translateRole(err error) error {
	var validation participants.ValidationError
	if errors.As(err, &validation) {
		return ValidationError{Field: validation.Field, Message: validation.Message}
	}
	return err
}

func pageSize(requested int) (int, error) {
	switch {
	case requested == 0:
		return defaultPageSize, nil
	case requested < 0 || requested > maxPageSize:
		return 0, ValidationError{
			Field:   "limit",
			Message: fmt.Sprintf("The limit must be between 1 and %d.", maxPageSize),
		}
	default:
		return requested, nil
	}
}

/*
audit records an invitation event.

The secret is never part of one, and neither is anything the application wrote:
an invitation carries no free text, so there is nothing here that could say
something about the person it was sent to.
*/
func (service *Service) audit(ctx context.Context, event string, invitation Invitation) {
	service.logger.Info(event,
		"invitation_id", invitation.ID,
		"application_id", invitation.ApplicationID,
		"call_id", invitation.CallID,
		"user_id", invitation.UserID,
		"role", invitation.Role,
		"status", string(invitation.Status(now())),
		"request_id", api.RequestIDFromContext(ctx),
	)
}

// now is the clock the domain reads, in UTC so that stored moments compare.
func now() time.Time {
	return time.Now().UTC()
}
