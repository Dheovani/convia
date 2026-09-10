package calls

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"convia/internal/api"
	"convia/internal/events"
	"convia/internal/media"
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

/*
MediaPlane is the behavior this package needs from whatever transports audio
and video.

It is declared here, in the consuming package, and it is exported only so that
the composition root can name what it is constructing. It is deliberately
narrow: a call begins, so a place for it must be realized; a call ends, so that
place must be released; and someone Convia has admitted needs a credential to
connect with. Nothing here decides *whether* anyone may connect — that is
settled before the boundary is reached.

media.Absent satisfies it, and is what a Convia with no media plane configured
uses. See docs/adr/0001-control-plane-media-plane-boundary.md.
*/
type MediaPlane interface {
	OpenSession(ctx context.Context, request media.SessionRequest) (media.Session, error)
	CloseSession(ctx context.Context, session media.Session) error
	IssueCredential(ctx context.Context, admission media.Admission) (media.Credential, error)
}

/*
announcer is the behavior this package needs to publish what happened.

It returns no error, and that is the whole of its contract: telling somebody a
call started must not be able to undo starting it. The call has already
committed by the time this runs, so a failure to announce is something to
record rather than something to report back.

The context is there because announcing is no longer only a fan-out to whoever
is listening: it also records what is owed to the destinations an application
registered, which is a write with a deadline. events.Announcer satisfies it.
*/
type announcer interface {
	Publish(ctx context.Context, event events.Event)
}

// Service applies Convia's rules for calls.
type Service struct {
	store   *Store
	tenants tenants
	rooms   roomLookup
	media   MediaPlane
	stream  announcer
	logger  *slog.Logger
}

func NewService(store *Store, owner tenants, places roomLookup,
	transport MediaPlane, stream announcer, logger *slog.Logger) *Service {
	return &Service{store: store, tenants: owner, rooms: places, media: transport,
		stream: stream, logger: logger}
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
	service.audit(ctx, events.CallStarted, call)

	/*
		The call record is the control-plane truth and the media session is
		realized from it, which is the order docs/calls.md committed to. It
		also has to be this order: the row is what holds the room, so
		realizing first would let two callers both realize a session for a
		room only one of them can have.
	*/
	if err := service.realize(ctx, call); err != nil {
		return Call{}, err
	}
	return call, nil
}

// unrealizedReason is recorded on a call whose media session never existed.
const unrealizedReason = "The media session could not be established."

/*
Admit issues the credential one person connects to this call with.

**Whether they may is not decided here.** Authorization, the person's standing,
their participation, and their role are all settled by the package that owns
them before this is reached, which is why the caller passes a call it has
already resolved rather than an identifier. What this method owns is the one
thing nothing else can reach: the call's media session, which is stored beside
the call and deliberately absent from the domain type, so an admission has to
come through here to find out which room it is for.

An un-issued credential is returned without an error when the deployment has no
media plane. That is not a failure — it is a Convia running exactly as
configured — and explaining it to a caller belongs to the layer that knows what
was asked for.
*/
func (service *Service) Admit(ctx context.Context, call Call, participantID string,
	lifetime time.Duration) (media.Credential, error) {

	/*
		Checked against the call the caller already holds rather than fetched
		again. It costs nothing and it means a future caller that forgets the
		rule is refused rather than quietly handing out a credential to a
		conversation that is over.
	*/
	if call.Ended() {
		return media.Credential{}, ErrNotFound
	}

	reference, err := service.store.Session(ctx, call.ApplicationID, call.ID)
	if err != nil {
		return media.Credential{}, fmt.Errorf("read the media session: %w", err)
	}

	credential, err := service.media.IssueCredential(ctx, media.Admission{
		Session:       media.Session{Reference: reference},
		ParticipantID: participantID,
		Lifetime:      lifetime,
	})

	if err != nil {
		if media.Retryable(err) {
			return media.Credential{}, ErrMediaUnavailable
		}

		service.logger.Error("the media plane refused to admit a participant",
			"error", err,
			"call_id", call.ID,
			"participant_id", participantID,
			"application_id", call.ApplicationID,
			"request_id", api.RequestIDFromContext(ctx),
		)

		return media.Credential{}, fmt.Errorf("admit to call: %w", err)
	}

	return credential, nil
}

/*
realize asks the media plane for somewhere this call can happen.

A call that cannot be realized is **ended**, not left standing. The row holds
the room, so leaving it would block the room with a conversation that never
happened, which docs/calls.md named as the worse failure. The attempt stays in
the history with a reason rather than being erased.
*/
func (service *Service) realize(ctx context.Context, call Call) error {
	session, err := service.media.OpenSession(ctx, media.SessionRequest{CallID: call.ID})
	if err != nil {
		service.abandon(ctx, call)

		if media.Retryable(err) {
			return ErrMediaUnavailable
		}
		/*
			A refusal is terminal: retrying will not fix a misconfiguration or
			a credential the provider does not accept. It is logged with its
			detail and reported as an internal condition, because telling a
			caller to retry would be telling it to wait for something that
			will not change on its own.
		*/
		service.logger.Error("the media plane refused to realize a call",
			"error", err,
			"call_id", call.ID,
			"application_id", call.ApplicationID,
			"request_id", api.RequestIDFromContext(ctx),
		)
		return fmt.Errorf("realize call: %w", err)
	}

	if !session.Realized() {
		return nil
	}

	if err := service.store.AttachSession(ctx, call.ApplicationID, call.ID, session.Reference); err != nil {
		/*
			A session exists that Convia cannot remember, which is worse than
			none: nothing would ever release it. It is released now, and the
			call is abandoned with it.
		*/
		service.release(ctx, call, session)
		service.abandon(ctx, call)
		return err
	}
	return nil
}

/*
abandon ends a call whose media session could not be realized, freeing its room.

It is best-effort by necessity: it is reached because something already failed,
and the same outage may take this write with it. A room left held by a call
that never happened is reported loudly rather than hidden, because a
reconciliation job is what fixes it.
*/
func (service *Service) abandon(ctx context.Context, call Call) {
	ended, changed, err := service.store.End(ctx, call.ApplicationID, call.ID,
		call.StartedBy, unrealizedReason, now())
	if err != nil {
		service.logger.Error("a room is held by a call whose media session failed",
			"error", err,
			"call_id", call.ID,
			"room_id", call.RoomID,
			"application_id", call.ApplicationID,
			"request_id", api.RequestIDFromContext(ctx),
		)
		return
	}
	if changed {
		service.audit(ctx, events.CallEnded, ended)
	}
}

/*
release lets the media plane know a session is finished.

It is best-effort and never changes the answer a caller receives. Convia's
record is the control-plane truth, and making an ending depend on a provider
being reachable would let an outage keep conversations open in Convia that
ended in reality. A session that could not be released is logged; reclaiming
one belongs to the adapter, which is the only thing that can list what its
provider still holds.
*/
func (service *Service) release(ctx context.Context, call Call, session media.Session) {
	if !session.Realized() {
		return
	}

	if err := service.media.CloseSession(ctx, session); err != nil {
		service.logger.Error("a media session was not released",
			"error", err,
			"call_id", call.ID,
			"application_id", call.ApplicationID,
			"request_id", api.RequestIDFromContext(ctx),
		)
	}
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

	service.releaseSessionOf(ctx, call)
	service.audit(ctx, events.CallEnded, call)
	return call, nil
}

/*
releaseSessionOf finds the session realizing a call and releases it.

The reference is asked for rather than carried on the call, which is what keeps
it out of every representation a handler could return. Failing to read it is
logged and does not change the ending, for the same reason failing to close the
session does not.
*/
func (service *Service) releaseSessionOf(ctx context.Context, call Call) {
	reference, err := service.store.Session(ctx, call.ApplicationID, call.ID)
	if err != nil {
		service.logger.Error("could not read the media session of an ended call",
			"error", err,
			"call_id", call.ID,
			"application_id", call.ApplicationID,
			"request_id", api.RequestIDFromContext(ctx),
		)
		return
	}
	service.release(ctx, call, media.Session{Reference: reference})
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
audit records a call lifecycle change, and announces it to whoever is listening.

The metadata and the end reason are application-composed text that may say
something about the people in the call, so neither is recorded. What an
operator needs is which call changed, in which room, for which tenant, into
what state, and on whose authority.

Recording and announcing happen together on purpose. They describe one
occurrence, and separating them would let the audit trail and the live stream
drift into two accounts of the same conversation.
*/
func (service *Service) audit(ctx context.Context, kind events.Type, call Call) {
	// The actor is whoever caused the event being recorded, which is the one
	// who ended the call once there is one, and otherwise the one who started it.
	actor := call.StartedBy
	if call.EndedBy != nil {
		actor = *call.EndedBy
	}

	service.logger.Info("audit event",
		"event", string(kind),
		"call_id", call.ID,
		"application_id", call.ApplicationID,
		"room_id", call.RoomID,
		"call_status", string(call.Status),
		"actor", string(actor),
		"request_id", api.RequestIDFromContext(ctx),
	)

	service.stream.Publish(ctx, events.New(kind, call.ApplicationID, call.ID,
		api.RequestIDFromContext(ctx), events.Data{
			"room_id": call.RoomID,
			"status":  string(call.Status),
			"actor":   string(actor),
		}))
}

// now returns the timestamp Convia stores for a change.
func now() time.Time {
	return time.Now().UTC().Truncate(time.Microsecond)
}
