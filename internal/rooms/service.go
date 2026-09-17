package rooms

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"convia/internal/api"
	"convia/internal/events"
	"convia/internal/transaction"
	"convia/internal/users"
)

const (
	// defaultPageSize and maxPageSize implement the pagination bounds defined
	// in docs/api-conventions.md.
	defaultPageSize = 25
	maxPageSize     = 100
)

/*
tenants is the behavior this package needs from the tenancy package.

It asks whether an application is being served rather than whether it exists,
so that a room cannot be created for a tenant Convia has stopped serving.
*/
type tenants interface {
	Active(ctx context.Context, applicationID string) (bool, error)
}

/*
people is the behavior this package needs from the identity domain.

It arrives with membership and for no other reason. A room needed to know
nothing about individual people while only applications addressed one; giving
somebody a place in a room is the first operation that has to check the person
is real, is this application's, and has not been suspended.

Only reading is needed, so the dependency stays one-directional: the identity
domain knows nothing about rooms.
*/
type people interface {
	Get(ctx context.Context, applicationID, id string) (users.User, error)
}

/*
announcer is the behavior this package needs to publish what happened.

It is called inside the transaction that made the change, and records the
event there: an error means the change must not commit either. See
docs/adr/0017. events.Announcer satisfies it.
*/
type announcer interface {
	Publish(ctx context.Context, event events.Event) error
}

// Service applies Convia's rules for rooms.
type Service struct {
	store   *Store
	tenants tenants
	people  people
	stream  announcer
	logger  *slog.Logger
	now     func() time.Time

	// calls is told when a room changes under the conversation it is holding.
	// It is nil until InformCalls is called, and then nothing is told.
	calls conversations
}

/*
conversations is what a room tells the calls held in it when it changes under
them.

It is declared here and implemented by the participants package, which is the
only direction the dependency can go: calls and participants read rooms, so a
room cannot import them back. The two methods are the two ways a room can pull
the ground out from under a conversation — going away, and somebody losing
their place in it — and neither returns anything, because the room has already
changed by the time it says so and nothing a call does may undo that.
*/
type conversations interface {
	RoomDeleted(ctx context.Context, applicationID, roomID string)
	MemberGone(ctx context.Context, applicationID, roomID, userID string)
}

/*
InformCalls tells the rooms service what to tell when a room changes under a
call.

It is set after construction, and only once, by the composition root. The calls
and participants services are built from this one, so it cannot be a
constructor argument without a cycle; it is set before the server starts, so no
request ever sees it change.
*/
func (service *Service) InformCalls(calls conversations) {
	service.calls = calls
}

func NewService(store *Store, owner tenants, directory people, stream announcer, logger *slog.Logger) *Service {
	return &Service{
		store:   store,
		tenants: owner,
		people:  directory,
		stream:  stream,
		logger:  logger,
		now:     func() time.Time { return time.Now().UTC().Truncate(time.Microsecond) },
	}
}

/*
Definition is what a caller asks for when creating a room.

Alias is optional and is what makes the room durable: with one, the application
can address the room by a name it chose; without one, the room is anonymous and
referred to only by its identifier.
*/
type Definition struct {
	Alias           string
	Name            string
	Metadata        map[string]string
	MaxParticipants *int
}

/*
Change is what a caller asks to modify, with absent meaning "leave alone".

Every field is a pointer so that clearing a value and omitting it stay
distinguishable: sending an empty alias removes it, and not sending one leaves
whatever is stored.
*/
type Change struct {
	Alias           *string
	Name            *string
	Metadata        *map[string]string
	MaxParticipants **int
}

// ListOptions selects one page of an application's rooms.
type ListOptions struct {
	Limit  int
	Cursor string
	Status string
}

// Page is one page of rooms and the token that continues it.
type Page struct {
	Rooms      []Room
	NextCursor string
}

/*
Create registers a room for an application.

An alias another room already holds is refused rather than returning that room.
Creation and lookup are different questions, and answering both from one
endpoint would make it impossible for a caller to tell whether it just created
something. Repeating a creation safely is what the `Idempotency-Key` header in
docs/api-compatibility.md is for.
*/
func (service *Service) Create(ctx context.Context, applicationID string, definition Definition) (Room, error) {
	if err := service.requireApplication(ctx, applicationID); err != nil {
		return Room{}, err
	}

	room, err := define(applicationID, definition)
	if err != nil {
		return Room{}, err
	}

	if err := service.store.Create(ctx, room); err != nil {
		return Room{}, err
	}

	service.audit(ctx, "room.created", room)
	return room, nil
}

/*
CreateFor makes a room with one person already in it.

It is how somebody signed in to Convia's own product opens a place to talk. The
membership is part of the creation rather than a step after it, for the reason
Store.CreateWithMember gives: a room nobody is in is a room no person can reach.

The person is checked first, so a suspended one is refused before anything is
written rather than left owning a room they cannot open.
*/
func (service *Service) CreateFor(ctx context.Context, applicationID, userID string,
	definition Definition) (Room, error) {
	if err := service.requireApplication(ctx, applicationID); err != nil {
		return Room{}, err
	}

	if err := service.requirePerson(ctx, applicationID, userID); err != nil {
		return Room{}, err
	}

	room, err := define(applicationID, definition)
	if err != nil {
		return Room{}, err
	}
	// Whoever opens a room owns it. See docs/adr/0013.
	room.Personal = true
	room.OwnerUserID = userID

	member := Member{
		ApplicationID: applicationID,
		RoomID:        room.ID,
		UserID:        userID,
		CreatedAt:     room.CreatedAt,
	}

	err = service.store.Atomically(ctx, func(ctx context.Context) error {
		if err := service.store.CreateWithMember(ctx, room, member); err != nil {
			return err
		}
		return service.announceMembership(ctx, events.MemberAdded, member)
	})

	if err != nil {
		return Room{}, err
	}

	service.audit(ctx, "room.created", room)
	return room, nil
}

// define validates what a caller asked for and builds the room it describes,
// without writing anything.
func define(applicationID string, definition Definition) (Room, error) {
	alias, err := NormalizeAlias(definition.Alias)
	if err != nil {
		return Room{}, err
	}

	name, err := NormalizeName(definition.Name)
	if err != nil {
		return Room{}, err
	}

	metadata, err := NormalizeMetadata(definition.Metadata)
	if err != nil {
		return Room{}, err
	}

	capacity, err := NormalizeMaxParticipants(definition.MaxParticipants)
	if err != nil {
		return Room{}, err
	}

	created := now()
	room := Room{
		ID:              NewID(),
		ApplicationID:   applicationID,
		Alias:           alias,
		Name:            name,
		Metadata:        metadata,
		MaxParticipants: capacity,
		Status:          StatusOpen,
		CreatedAt:       created,
		UpdatedAt:       created,
	}
	return room, nil
}

// Get returns one room of an application.
func (service *Service) Get(ctx context.Context, applicationID, id string) (Room, error) {
	if err := service.requireApplication(ctx, applicationID); err != nil {
		return Room{}, err
	}
	if !ValidID(id) {
		return Room{}, ErrNotFound
	}
	return service.store.Get(ctx, applicationID, id)
}

/*
GetByAlias returns the room an application named.

This is what makes a durable room usable without Convia's identifier: an
application that chose "weekly-standup" can address it by that name forever.
*/
func (service *Service) GetByAlias(ctx context.Context, applicationID, alias string) (Room, error) {
	if err := service.requireApplication(ctx, applicationID); err != nil {
		return Room{}, err
	}

	normalized, err := NormalizeAlias(alias)
	if err != nil {
		return Room{}, err
	}
	if normalized == "" {
		return Room{}, ErrNotFound
	}
	return service.store.GetByAlias(ctx, applicationID, normalized)
}

// List returns one page of an application's rooms, newest first.
func (service *Service) List(ctx context.Context, applicationID string, options ListOptions) (Page, error) {
	if err := service.requireApplication(ctx, applicationID); err != nil {
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

	page, hasMore, err := service.store.List(ctx, applicationID, filter, cursor, limit)
	if err != nil {
		return Page{}, fmt.Errorf("list rooms: %w", err)
	}

	result := Page{Rooms: page}
	if hasMore {
		last := page[len(page)-1]
		result.NextCursor = Cursor{CreatedAt: last.CreatedAt, ID: last.ID}.Encode()
	}
	return result, nil
}

/*
Update changes the attributes an application owns.

The lifecycle is deliberately not among them. Moving a room between open and
closed is a transition with its own meaning and its own audit event, so it has
its own operations rather than being reachable by writing a status field.
*/
func (service *Service) Update(ctx context.Context, applicationID, id string,
	change Change, expectedVersion string) (Room, error) {
	if change.Alias == nil && change.Name == nil && change.Metadata == nil && change.MaxParticipants == nil {
		return Room{}, ValidationError{Field: "body", Message: "The request must change at least one attribute."}
	}

	/*
		What the caller sent is validated before anything is read, so that a
		malformed request is rejected the same way whether or not the room
		exists.
	*/
	normalized, err := service.normalizeChange(change)
	if err != nil {
		return Room{}, err
	}

	if err := service.requireApplication(ctx, applicationID); err != nil {
		return Room{}, err
	}
	if !ValidID(id) {
		return Room{}, ErrNotFound
	}

	guard, err := service.precondition(ctx, applicationID, id, expectedVersion)
	if err != nil {
		return Room{}, err
	}

	var room Room
	err = service.store.Atomically(ctx, func(ctx context.Context) error {
		var err error
		room, err = service.store.Update(ctx, applicationID, id, normalized, now(), guard)
		if err != nil {
			return err
		}
		return service.announceRoom(ctx, events.RoomUpdated, room.ApplicationID, room.ID)
	})

	if err != nil {
		return Room{}, err
	}

	service.audit(ctx, "room.updated", room)
	return room, nil
}

// normalizeChange validates every attribute a caller asked to change.
func (service *Service) normalizeChange(change Change) (Change, error) {
	normalized := Change{}

	if change.Alias != nil {
		alias, err := NormalizeAlias(*change.Alias)
		if err != nil {
			return Change{}, err
		}
		normalized.Alias = &alias
	}
	if change.Name != nil {
		name, err := NormalizeName(*change.Name)
		if err != nil {
			return Change{}, err
		}
		normalized.Name = &name
	}
	if change.Metadata != nil {
		metadata, err := NormalizeMetadata(*change.Metadata)
		if err != nil {
			return Change{}, err
		}
		normalized.Metadata = &metadata
	}
	if change.MaxParticipants != nil {
		capacity, err := NormalizeMaxParticipants(*change.MaxParticipants)
		if err != nil {
			return Change{}, err
		}
		normalized.MaxParticipants = &capacity
	}
	return normalized, nil
}

/*
Close stops a room accepting new calls without losing it.

What it does to calls already in progress is M09's decision to implement, and
this is the shape it must take: closing marks the room, and the call plane
refuses to *start* anything new in it. Ending a conversation people are having
because an administrator tidied up a listing would be the wrong default, so a
call in progress runs to its end.
*/
func (service *Service) Close(ctx context.Context, applicationID, id string) (Room, error) {
	return service.transition(ctx, applicationID, id, StatusClosed, events.RoomClosed)
}

// Reopen returns a closed room to service.
func (service *Service) Reopen(ctx context.Context, applicationID, id string) (Room, error) {
	return service.transition(ctx, applicationID, id, StatusOpen, events.RoomReopened)
}

/*
transition moves a room between lifecycle states.

Repeating a transition succeeds and announces nothing further, so a client that
retries after a timeout is never punished for it, and nobody is told twice.
Whether it changed is read just before the write, in the same transaction; two
transitions racing each other may both announce, which costs a subscriber one
extra read.
*/
func (service *Service) transition(
	ctx context.Context,
	applicationID,
	id string,
	status Status,
	event events.Type,
) (Room, error) {
	if err := service.requireApplication(ctx, applicationID); err != nil {
		return Room{}, err
	}
	if !ValidID(id) {
		return Room{}, ErrNotFound
	}

	var room Room
	err := service.store.Atomically(ctx, func(ctx context.Context) error {
		before, err := service.store.Get(ctx, applicationID, id)
		if err != nil {
			return err
		}

		room, err = service.store.SetStatus(ctx, applicationID, id, status, now())
		if err != nil {
			return err
		}

		if before.Status == status {
			return nil
		}

		return service.announceRoom(ctx, event, room.ApplicationID, room.ID)
	})

	if err != nil {
		return Room{}, err
	}

	service.audit(ctx, string(event), room)
	return room, nil
}

/*
announceRoom tells whoever is listening that a room changed. It names the room
and nothing else: its name is a label somebody chose, and whoever may read it
reads it back.
*/
func (service *Service) announceRoom(ctx context.Context, kind events.Type, applicationID, roomID string) error {
	return service.stream.Publish(ctx, events.New(kind, applicationID, roomID, api.RequestIDFromContext(ctx), nil))
}

/*
Delete removes a room from the API surface.

The record is retained for the erasure window rather than destroyed, so the
deletion stays recoverable and the alias stays reserved. Deleting an
already-deleted room succeeds.
*/
func (service *Service) Delete(ctx context.Context, applicationID, id string) error {
	if err := service.requireApplication(ctx, applicationID); err != nil {
		return err
	}
	if !ValidID(id) {
		return ErrNotFound
	}

	var deleted bool
	err := service.store.Atomically(ctx, func(ctx context.Context) error {
		var err error
		deleted, err = service.store.Delete(ctx, applicationID, id, now())
		if err != nil || !deleted {
			return err
		}

		// Nobody can find a call in a room that is gone, so the call ends with it.
		if service.calls != nil {
			transaction.AfterCommit(ctx, func(ctx context.Context) {
				service.calls.RoomDeleted(ctx, applicationID, id)
			})
		}
		return service.announceRoom(ctx, events.RoomDeleted, applicationID, id)
	})

	if err != nil || !deleted {
		return err
	}

	service.audit(ctx, "room.deleted", Room{ID: id, ApplicationID: applicationID})
	return nil
}

/*
precondition turns a client's entity tag into a storage-level guard.

The version is a digest a client cannot construct, so it is resolved against
the stored room rather than trusted. A caller that sent no condition gets no
guard, which is what makes If-Match optional.
*/
func (service *Service) precondition(ctx context.Context, applicationID, id, expectedVersion string) (*time.Time, error) {
	if expectedVersion == "" {
		return nil, nil
	}

	stored, err := service.store.Get(ctx, applicationID, id)
	if err != nil {
		return nil, err
	}
	if stored.Status == StatusDeleted {
		return nil, ErrDeleted
	}
	if stored.Version() != expectedVersion {
		return nil, ErrPreconditionFailed
	}

	guard := stored.UpdatedAt
	return &guard, nil
}

/*
requireApplication refuses work for an application Convia does not serve.

Creating a room for a suspended tenant would produce one nobody could use, so
the caller is told the tenant is not being served instead.
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
audit records a room lifecycle change.

The alias and name are application-chosen labels that may carry meaning about
the people using them, so neither is recorded. What an operator needs is which
room changed, for which tenant, and into what state.
*/
func (service *Service) audit(ctx context.Context, event string, room Room) {
	service.logger.Info("audit event",
		"event", event,
		"room_id", room.ID,
		"application_id", room.ApplicationID,
		"room_status", string(room.Status),
		"request_id", api.RequestIDFromContext(ctx),
	)
}

// now returns the timestamp Convia stores for a change.
func now() time.Time {
	return time.Now().UTC().Truncate(time.Microsecond)
}
