package calls

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"convia/internal/api"
	"convia/internal/rooms"
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
so that a call cannot be read or ended for a tenant Convia has stopped serving.
*/
type tenants interface {
	Active(ctx context.Context, applicationID string) (bool, error)
}

/*
roomLookup is the behavior this package needs from the room domain.

A call happens in a room, so starting one asks the room whether it will have
it. Only reading is needed: the call domain never changes a room, which keeps
the dependency one-directional and the room the authority on its own state.
*/
type roomLookup interface {
	Get(ctx context.Context, applicationID, roomID string) (rooms.Room, error)
}

// Service applies Convia's rules for calls.
type Service struct {
	store   *Store
	tenants tenants
	rooms   roomLookup
	logger  *slog.Logger
}

func NewService(store *Store, owner tenants, places roomLookup, logger *slog.Logger) *Service {
	return &Service{store: store, tenants: owner, rooms: places, logger: logger}
}

/*
Definition is what a caller asks for when starting a call.

There is very little to ask for, which is the point: a call takes its
identity from the room it happens in and the moment it starts. The metadata is
the application's own annotation, stored without interpretation.
*/
type Definition struct {
	Metadata map[string]string
}

/*
ListOptions selects one page of an application's calls.

RoomID narrows a history to one room, which is the listing an application reads
most: what has happened, and is happening, in this place.
*/
type ListOptions struct {
	Limit  int
	Cursor string
	Status string
	RoomID string
}

// Page is one page of calls and the token that continues it.
type Page struct {
	Calls      []Call
	NextCursor string
}

/*
Start begins a conversation in a room.

A room hosting a conversation refuses a second rather than returning the
running one, because starting and finding are different questions: a caller
that received the current call could not tell whether it had just started
something. Reading the current call is a listing filtered by `active`.

Repeating a start safely is what the `Idempotency-Key` header is for, described
in docs/api-compatibility.md.
*/
func (service *Service) Start(ctx context.Context, applicationID, roomID string,
	definition Definition, by Actor) (Call, error) {
	room, err := service.requireRoom(ctx, applicationID, roomID)
	if err != nil {
		return Call{}, err
	}

	/*
		A closed room is refused and a deleted one is reported missing. The
		distinction matters: closed is a state the application chose and can
		undo, while deleted is gone from the API and saying "closed" would
		invite a caller to reopen something that is not there.
	*/
	switch room.Status {
	case rooms.StatusDeleted:
		return Call{}, ErrRoomNotFound
	case rooms.StatusClosed:
		return Call{}, ErrRoomClosed
	}

	metadata, err := NormalizeMetadata(definition.Metadata)
	if err != nil {
		return Call{}, err
	}

	started := now()
	call := Call{
		ID:            NewID(),
		ApplicationID: applicationID,
		RoomID:        room.ID,
		Status:        StatusActive,
		Metadata:      metadata,
		StartedBy:     by,
		CreatedAt:     started,
		UpdatedAt:     started,
	}

	if err := service.store.Create(ctx, call); err != nil {
		return Call{}, err
	}

	service.audit(ctx, "call.started", call)
	return call, nil
}

// Get returns one call of an application.
func (service *Service) Get(ctx context.Context, applicationID, id string) (Call, error) {
	if err := service.requireApplication(ctx, applicationID); err != nil {
		return Call{}, err
	}
	if !ValidID(id) {
		return Call{}, ErrNotFound
	}
	return service.store.Get(ctx, applicationID, id)
}

/*
List returns one page of an application's calls, newest first.

Every call is returned, ended ones included, because a call history is what
this listing is for. That is the opposite of rooms, where a deleted room is
hidden unless asked for: a deleted room is one an application can no longer
use, while an ended call is exactly what it wants to look back at.
*/
func (service *Service) List(ctx context.Context, applicationID string, options ListOptions) (Page, error) {
	if err := service.requireApplication(ctx, applicationID); err != nil {
		return Page{}, err
	}

	/*
		A room-scoped listing resolves the room first, so that asking for the
		history of a room that is not this application's answers "no such
		room" rather than an empty page a caller would read as "no calls yet".
	*/
	if options.RoomID != "" {
		if _, err := service.requireRoom(ctx, applicationID, options.RoomID); err != nil {
			return Page{}, err
		}
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

	page, hasMore, err := service.store.List(ctx, applicationID, options.RoomID, filter, cursor, limit)
	if err != nil {
		return Page{}, fmt.Errorf("list calls: %w", err)
	}

	result := Page{Calls: page}
	if hasMore {
		last := page[len(page)-1]
		result.NextCursor = Cursor{CreatedAt: last.CreatedAt, ID: last.ID}.Encode()
	}
	return result, nil
}

/*
End stops a conversation.

Ending an already-ended call succeeds and returns it unchanged, so a client
retrying after a timeout is never punished for it. Only the request that
actually ended the call records who ended it and why; a repeat does not
overwrite the first answer with its own.
*/
func (service *Service) End(ctx context.Context, applicationID, id string,
	by Actor, reason string) (Call, error) {
	normalized, err := NormalizeEndReason(reason)
	if err != nil {
		return Call{}, err
	}

	if err := service.requireApplication(ctx, applicationID); err != nil {
		return Call{}, err
	}
	if !ValidID(id) {
		return Call{}, ErrNotFound
	}

	call, ended, err := service.store.End(ctx, applicationID, id, by, normalized, now())
	if err != nil {
		return Call{}, err
	}
	if !ended {
		return call, nil
	}

	service.audit(ctx, "call.ended", call)
	return call, nil
}

/*
requireRoom resolves the room a call belongs to.

The room domain already refuses an application Convia does not serve, so this
translates its answers rather than repeating its checks. Both are reported as
missing rather than forbidden, so neither confirms that another tenant's room
exists.
*/
func (service *Service) requireRoom(ctx context.Context, applicationID, roomID string) (rooms.Room, error) {
	room, err := service.rooms.Get(ctx, applicationID, roomID)
	switch {
	case errors.Is(err, rooms.ErrApplicationNotFound):
		return rooms.Room{}, ErrApplicationNotFound
	case errors.Is(err, rooms.ErrNotFound):
		return rooms.Room{}, ErrRoomNotFound
	case err != nil:
		return rooms.Room{}, fmt.Errorf("resolve room: %w", err)
	}
	return room, nil
}

/*
requireApplication refuses work for an application Convia does not serve.

Reading the calls of a suspended tenant would report on a service it is no
longer receiving, so the caller is told the tenant is not being served instead.
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
audit records a call lifecycle change.

The metadata and the end reason are application-composed text that may say
something about the people in the call, so neither is recorded. What an
operator needs is which call changed, in which room, for which tenant, into
what state, and on whose authority.
*/
func (service *Service) audit(ctx context.Context, event string, call Call) {
	// The actor is whoever caused the event being recorded, which is the one
	// who ended the call once there is one, and otherwise the one who started it.
	actor := call.StartedBy
	if call.EndedBy != nil {
		actor = *call.EndedBy
	}

	service.logger.Info("audit event",
		"event", event,
		"call_id", call.ID,
		"application_id", call.ApplicationID,
		"room_id", call.RoomID,
		"call_status", string(call.Status),
		"actor", string(actor),
		"request_id", api.RequestIDFromContext(ctx),
	)
}

// now returns the timestamp Convia stores for a change.
func now() time.Time {
	return time.Now().UTC().Truncate(time.Microsecond)
}
