package participants

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"convia/internal/api"
	"convia/internal/calls"
	"convia/internal/media"
	"convia/internal/rooms"
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
so that a roster cannot be read or changed for a tenant Convia has stopped
serving.
*/
type tenants interface {
	Active(ctx context.Context, applicationID string) (bool, error)
}

/*
callLookup is the behavior this package needs from the call domain.

Only reading is needed: participants never change a call, which keeps the
dependency one-directional and the call the authority on its own state.
*/
type callLookup interface {
	Get(ctx context.Context, applicationID, id string) (calls.Call, error)
	Admit(ctx context.Context, call calls.Call, participantID string,
		lifetime time.Duration) (media.Credential, error)
}

/*
roomLookup is the behavior this package needs from the room domain.

It exists for one reason: capacity. An application decides how many people
belong in a place, and the place is the room, so the limit is read from there
and counted here.
*/
type roomLookup interface {
	Get(ctx context.Context, applicationID, id string) (rooms.Room, error)
}

/*
userLookup is the behavior this package needs from the identity domain.

A participant is a person the application already told Convia about. Naming
someone Convia does not know, or someone the application has suspended, is
refused rather than admitted as an unknown identifier.
*/
type userLookup interface {
	Get(ctx context.Context, applicationID, id string) (users.User, error)
}

// Service applies Convia's rules for who is in a call.
type Service struct {
	store   *Store
	tenants tenants
	calls   callLookup
	rooms   roomLookup
	users   userLookup
	logger  *slog.Logger
}

func NewService(store *Store, owner tenants, conversations callLookup,
	places roomLookup, people userLookup, logger *slog.Logger) *Service {
	return &Service{
		store:   store,
		tenants: owner,
		calls:   conversations,
		rooms:   places,
		users:   people,
		logger:  logger,
	}
}

/*
Admission is what a caller asks for when adding someone to a call.

The user is named rather than described: a participant is a person the
application already told Convia about, so the roster refers to a Convia user
instead of accumulating a second copy of everyone's name.
*/
type Admission struct {
	UserID string
	Role   string
}

// ListOptions selects one page of a call's participants.
type ListOptions struct {
	Limit  int
	Cursor string
	Status string
}

// Page is one page of participants and the token that continues it.
type Page struct {
	Participants []Participant
	NextCursor   string
}

/*
Join admits someone to a call, reporting whether this request admitted them.

Joining is **idempotent by the person**: someone already in the call is
returned as they are, rather than added a second time. That is what makes a
reconnection safe — a client whose network dropped and came back is the same
person, and a roster showing them twice would be wrong in a way users notice
immediately.

Someone a moderator removed is refused. If a removal could be undone by
rejoining, removing would mean nothing.
*/
func (service *Service) Join(ctx context.Context, applicationID, callID string,
	admission Admission) (Participant, bool, error) {
	role, err := ParseRole(admission.Role)
	if err != nil {
		return Participant{}, false, err
	}

	call, err := service.requireCall(ctx, applicationID, callID)
	if err != nil {
		return Participant{}, false, err
	}
	if call.Ended() {
		return Participant{}, false, ErrCallEnded
	}

	if err := service.requireActiveUser(ctx, applicationID, admission.UserID); err != nil {
		return Participant{}, false, err
	}

	capacity, err := service.capacityOf(ctx, applicationID, call.RoomID)
	if err != nil {
		return Participant{}, false, err
	}

	joined := now()
	participant, admitted, err := service.store.Join(ctx, Participant{
		ID:            NewID(),
		ApplicationID: applicationID,
		CallID:        call.ID,
		UserID:        admission.UserID,
		Role:          role,
		CreatedAt:     joined,
		UpdatedAt:     joined,
	}, capacity)
	if err != nil {
		return Participant{}, false, err
	}
	if !admitted {
		return participant, false, nil
	}

	service.audit(ctx, "participant.joined", participant)
	return participant, true, nil
}

// Get returns one participant of an application.
func (service *Service) Get(ctx context.Context, applicationID, id string) (Participant, error) {
	if err := service.requireApplication(ctx, applicationID); err != nil {
		return Participant{}, err
	}
	if !ValidID(id) {
		return Participant{}, ErrNotFound
	}
	return service.store.Get(ctx, applicationID, id)
}

/*
List returns one page of a call's participants, newest first.

Everyone is returned, including those who left, because a roster is also a
record of who was there. Filtering by `joined` answers who is present now.
*/
func (service *Service) List(ctx context.Context, applicationID, callID string,
	options ListOptions) (Page, error) {
	call, err := service.requireCall(ctx, applicationID, callID)
	if err != nil {
		return Page{}, err
	}

	limit, err := pageSize(options.Limit)
	if err != nil {
		return Page{}, err
	}

	var filter *Status
	if options.Status != "" {
		status, err := ParseStatus(options.Status)
		if err != nil {
			return Page{}, err
		}
		filter = &status
	}

	var cursor *Cursor
	if options.Cursor != "" {
		decoded, err := DecodeCursor(options.Cursor)
		if err != nil {
			return Page{}, err
		}
		cursor = &decoded
	}

	page, hasMore, err := service.store.List(ctx, applicationID, call.ID, filter, cursor, limit)
	if err != nil {
		return Page{}, fmt.Errorf("list participants: %w", err)
	}

	result := Page{Participants: page}
	if hasMore {
		last := page[len(page)-1]
		result.NextCursor = Cursor{CreatedAt: last.CreatedAt, ID: last.ID}.Encode()
	}
	return result, nil
}

/*
Leave records that someone left of their own accord.

Leaving twice succeeds and changes nothing, so a client retrying after a
timeout is never punished for it, and someone who was removed is not quietly
converted into someone who left.
*/
func (service *Service) Leave(ctx context.Context, applicationID, id string) (Participant, error) {
	participant, err := service.locate(ctx, applicationID, id)
	if err != nil {
		return Participant{}, err
	}

	departed, left, err := service.store.Leave(ctx, applicationID, participant.ID, now())
	if err != nil {
		return Participant{}, err
	}
	if !left {
		return departed, nil
	}

	service.audit(ctx, "participant.left", departed)
	return departed, nil
}

/*
Remove puts someone out of a call.

The authority is the caller's own — an application may remove someone from its
own call, and an operator may too. When the application names a participant as
acting, Convia checks that participant was entitled to: Convia does not decide
whether the application may remove someone, because it already may. What
Convia decides is whether the moderator it was told about is one, since Convia
is what holds the roster.
*/
func (service *Service) Remove(ctx context.Context, applicationID, id string,
	authority Remover, actingID, reason string) (Participant, error) {
	normalized, err := NormalizeRemovalReason(reason)
	if err != nil {
		return Participant{}, err
	}

	participant, err := service.locate(ctx, applicationID, id)
	if err != nil {
		return Participant{}, err
	}

	if actingID != "" {
		if err := service.requireModerator(ctx, applicationID, participant.CallID, actingID); err != nil {
			return Participant{}, err
		}
		authority = RemoverParticipant
	}

	removed, changed, err := service.store.Remove(ctx, applicationID, participant.ID,
		authority, actingID, normalized, now())
	if err != nil {
		return Participant{}, err
	}
	if !changed {
		return removed, nil
	}

	service.audit(ctx, "participant.removed", removed)
	return removed, nil
}

/*
SetRole changes what a participant may do.

As with removal, naming an acting participant makes Convia check that person is
a moderator. Promoting someone is exactly the authority the role exists for, so
it is guarded the same way taking someone out of the call is.
*/
func (service *Service) SetRole(ctx context.Context, applicationID, id, role, actingID string) (Participant, error) {
	/*
		An absent role defaults to a member when someone joins, because the
		safe direction for a mistyped field is the lesser authority. Changing a
		role is different: an omitted field there would silently demote
		someone, so it has to be stated.
	*/
	if role == "" {
		return Participant{}, ValidationError{Field: "role", Message: "The role must be stated."}
	}

	parsed, err := ParseRole(role)
	if err != nil {
		return Participant{}, err
	}

	participant, err := service.locate(ctx, applicationID, id)
	if err != nil {
		return Participant{}, err
	}
	if !participant.Present() {
		return Participant{}, ErrGone
	}

	if actingID != "" {
		if err := service.requireModerator(ctx, applicationID, participant.CallID, actingID); err != nil {
			return Participant{}, err
		}
	}

	changed, err := service.store.SetRole(ctx, applicationID, participant.ID, parsed, now())
	if err != nil {
		return Participant{}, err
	}

	service.audit(ctx, "participant.role_changed", changed)
	return changed, nil
}

/*
credentialLifetime is how long a connection credential a client is given stays
valid.

It is deliberately short. The credential is presented once, to open a
connection, and the connection outlives it: nothing forces a client out when it
expires. What a short life bounds is how long a copy taken from a log, a
crash report, or a device somebody no longer has can still be used to walk into
a conversation.

The cost is real and is documented rather than hidden: a client that loses its
connection after the credential expires cannot reconnect with it and has to ask
for another. That is one request against an endpoint the application already
calls, and it is the trade this milestone chose.
*/
const credentialLifetime = 5 * time.Minute

/*
Session issues the credential a client connects to a call with.

It is the one place Convia decides that a particular person may take part in a
particular conversation right now, and it re-decides it every time. A
participant who was removed, whose user was suspended, or whose call has ended
gets nothing, however recently they were let in — which is what makes removal
mean something despite the media plane having no way to be told about it.

The checks are the ones joining already applies, deliberately: a rule enforced
in two places is a rule that will eventually be enforced in one.
*/
func (service *Service) Session(ctx context.Context, applicationID, id string) (Participant, media.Credential, error) {
	participant, err := service.locate(ctx, applicationID, id)
	if err != nil {
		return Participant{}, media.Credential{}, err
	}

	/*
		Left and removed are both refused, and neither reveals which. Someone
		who left may be readmitted by joining again, which is the application's
		decision to make rather than something this endpoint should quietly do
		on their behalf.
	*/
	if !participant.Present() {
		return Participant{}, media.Credential{}, ErrGone
	}

	call, err := service.requireCall(ctx, applicationID, participant.CallID)
	if err != nil {
		return Participant{}, media.Credential{}, err
	}

	if call.Ended() {
		return Participant{}, media.Credential{}, ErrCallEnded
	}

	if err := service.requireActiveUser(ctx, applicationID, participant.UserID); err != nil {
		return Participant{}, media.Credential{}, err
	}

	credential, err := service.calls.Admit(ctx, call, participant.ID, credentialLifetime)
	if err != nil {
		return Participant{}, media.Credential{}, err
	}

	if !credential.Issued() {
		return Participant{}, media.Credential{}, ErrNoMediaPlane
	}

	service.audit(ctx, "participant.session_issued", participant)
	return participant, credential, nil
}

/*
requireModerator refuses a participant acting beyond their role.

Three things are checked, and each has its own way of being wrong: the acting
participant must be one of this application's, must be in the same call as the
person being acted on, and must be present and a moderator. Someone naming a
moderator from a different call would otherwise borrow authority that does not
reach.
*/
func (service *Service) requireModerator(ctx context.Context, applicationID, callID, actingID string) error {
	if !ValidID(actingID) {
		return ErrNotFound
	}

	acting, err := service.store.Get(ctx, applicationID, actingID)
	if err != nil {
		return err
	}
	if acting.CallID != callID {
		return ErrNotFound
	}
	if !acting.Present() {
		return ErrGone
	}
	if acting.Role != RoleModerator {
		return ErrNotAModerator
	}
	return nil
}

// locate resolves a participant of an application that Convia still serves.
func (service *Service) locate(ctx context.Context, applicationID, id string) (Participant, error) {
	if err := service.requireApplication(ctx, applicationID); err != nil {
		return Participant{}, err
	}
	if !ValidID(id) {
		return Participant{}, ErrNotFound
	}
	return service.store.Get(ctx, applicationID, id)
}

/*
requireCall resolves the call a participant belongs to.

The call domain already refuses an application Convia does not serve, so this
translates its answers rather than repeating its checks. Both are reported as
missing rather than forbidden, so neither confirms that another tenant's call
exists.
*/
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
requireActiveUser refuses someone the application does not serve.

A person the application suspended must not be let into a conversation, or the
suspension would be decorative. A deleted or unknown user is reported the same
way, so probing an identifier tells a caller nothing.
*/
func (service *Service) requireActiveUser(ctx context.Context, applicationID, userID string) error {
	if userID == "" {
		return ValidationError{Field: "user_id", Message: "The user must be stated."}
	}

	user, err := service.users.Get(ctx, applicationID, userID)
	switch {
	case errors.Is(err, users.ErrNotFound):
		return ErrUserNotFound
	case errors.Is(err, users.ErrApplicationNotFound):
		return ErrApplicationNotFound
	case err != nil:
		return fmt.Errorf("resolve user: %w", err)
	}

	if user.Status != users.StatusActive {
		return ErrUserSuspended
	}
	return nil
}

/*
capacityOf reads the limit the room declared.

The limit lives on the room because that is where an application decided how
many people belong in a place. The call is where it is counted, which is what
docs/rooms.md said would happen once there were participants to count.
*/
func (service *Service) capacityOf(ctx context.Context, applicationID, roomID string) (*int, error) {
	room, err := service.rooms.Get(ctx, applicationID, roomID)
	if err != nil {
		return nil, fmt.Errorf("resolve room: %w", err)
	}
	return room.MaxParticipants, nil
}

/*
requireApplication refuses work for an application Convia does not serve.

Reading a roster for a suspended tenant would report on a service it is no
longer receiving, so the caller is told the tenant is missing instead.
*/
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

/*
pageSize validates a requested page size.

An oversized limit is rejected rather than silently clamped, so that a client
never believes it received every result it asked for.
*/
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
audit records a change to who is in a call.

Every value recorded is one Convia assigned: identifiers, a state, a role, an
authority. The removal reason is not recorded, because it is composed by the
application and may say something about the person removed — a test asserts it
stays out.
*/
func (service *Service) audit(ctx context.Context, event string, participant Participant) {
	attributes := []any{
		"event", event,
		"participant_id", participant.ID,
		"application_id", participant.ApplicationID,
		"call_id", participant.CallID,
		"user_id", participant.UserID,
		"participant_status", string(participant.Status),
		"participant_role", string(participant.Role),
		"request_id", api.RequestIDFromContext(ctx),
	}
	if participant.RemovedBy != nil {
		attributes = append(attributes, "removed_by", string(*participant.RemovedBy))
	}
	if participant.RemovedByID != "" {
		attributes = append(attributes, "removed_by_participant_id", participant.RemovedByID)
	}

	service.logger.Info("audit event", attributes...)
}

// now returns the timestamp Convia stores for a change.
func now() time.Time {
	return time.Now().UTC().Truncate(time.Microsecond)
}
