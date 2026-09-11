package messages

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"convia/internal/api"
	"convia/internal/invitations"
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
so that a history cannot be read or added to for a tenant Convia has stopped
serving.
*/
type tenants interface {
	Active(ctx context.Context, applicationID string) (bool, error)
}

/*
roomLookup is the behavior this package needs from the room domain.

Only reading is needed. A message never changes a room, which keeps the
dependency one-directional and leaves the room the authority on its own
lifecycle — including whether it is still open to being added to.
*/
type roomLookup interface {
	Get(ctx context.Context, applicationID, id string) (rooms.Room, error)
}

/*
userLookup is the behavior this package needs from the identity domain.

An author is somebody the application already told Convia about. Writing as an
identifier Convia does not know, or as somebody the application has suspended,
is refused rather than stored as an unattributable message.
*/
type userLookup interface {
	Get(ctx context.Context, applicationID, id string) (users.User, error)
}

/*
invitationLookup is the behavior this package needs to attribute a guest.

A guest has no user, so the invitation is the whole of their identity. It is
checked against the application so that one tenant cannot attribute a message
to an invitation another tenant issued, which the foreign key alone would
allow.
*/
type invitationLookup interface {
	Get(ctx context.Context, applicationID, id string) (invitations.Invitation, error)
}

/*
Service applies Convia's rules about what may be said, and where.

It does not decide **who** may say it beyond attribution being truthful: that a
named author exists, belongs to this application, and is somebody Convia still
serves. Whether a given person may reach a given room at all is per-person
authorization, and it is deliberately not here.
*/
type Service struct {
	store       *Store
	tenants     tenants
	rooms       roomLookup
	users       userLookup
	invitations invitationLookup
	logger      *slog.Logger
	now         func() time.Time
}

func NewService(store *Store, tenants tenants, rooms roomLookup, users userLookup,
	invitations invitationLookup, logger *slog.Logger) *Service {
	return &Service{
		store:       store,
		tenants:     tenants,
		rooms:       rooms,
		users:       users,
		invitations: invitations,
		logger:      logger,
		now:         func() time.Time { return time.Now().UTC().Truncate(time.Microsecond) },
	}
}

// Page is one window onto a room's history.
type Page struct {
	Messages   []Message
	NextCursor string
}

/*
HistoryOptions selects a window of a room's history.

An absent cursor means the end the direction starts from, so the common request
— "open this room" — carries no cursor at all.
*/
type HistoryOptions struct {
	Direction Direction
	After     *int64
	Limit     int
}

/*
Post records something somebody said in a room.

The author is checked before the room, so a message that cannot be truthfully
attributed never reaches a transaction. The room is then read to answer plainly
— a missing room should not cost a lock — but that read is **not** what the
append relies on: the store takes the room's row lock and re-reads its state
inside the transaction, so a room closed between this check and the insert is
still refused.
*/
func (service *Service) Post(ctx context.Context, applicationID, roomID string,
	author Author, body string) (Message, error) {
	if err := service.requireApplication(ctx, applicationID); err != nil {
		return Message{}, err
	}

	normalized, err := NormalizeBody(body)
	if err != nil {
		return Message{}, err
	}

	if err := service.requireAuthor(ctx, applicationID, author); err != nil {
		return Message{}, err
	}

	room, err := service.requireRoom(ctx, applicationID, roomID)
	if err != nil {
		return Message{}, err
	}

	if room.Status != rooms.StatusOpen {
		return Message{}, ErrRoomClosed
	}

	message, err := service.store.Append(ctx, Message{
		ID:            NewID(),
		ApplicationID: applicationID,
		RoomID:        room.ID,
		Author:        author,
		Body:          normalized,
		CreatedAt:     service.now(),
	})

	if err != nil {
		return Message{}, err
	}

	service.record(ctx, "message.posted", message)
	return message, nil
}

// Get returns one message within its application.
func (service *Service) Get(ctx context.Context, applicationID, id string) (Message, error) {
	if err := service.requireApplication(ctx, applicationID); err != nil {
		return Message{}, err
	}

	if !ValidID(id) {
		return Message{}, ErrNotFound
	}

	return service.store.Get(ctx, applicationID, id)
}

/*
History reads a window of what was said in a room.

The room is resolved first so that a history request for a room that does not
exist is told so, rather than being answered with an empty page that a client
would read as "nothing was ever said here".
*/
func (service *Service) History(ctx context.Context, applicationID, roomID string,
	options HistoryOptions) (Page, error) {
	if err := service.requireApplication(ctx, applicationID); err != nil {
		return Page{}, err
	}

	room, err := service.requireRoom(ctx, applicationID, roomID)
	if err != nil {
		return Page{}, err
	}

	direction := options.Direction
	if direction == "" {
		direction = Older
	}

	limit := options.Limit
	switch {
	case limit <= 0:
		limit = defaultPageSize
	case limit > maxPageSize:
		limit = maxPageSize
	}

	window, more, err := service.store.Page(ctx, applicationID, room.ID, direction, options.After, limit)
	if err != nil {
		return Page{}, err
	}

	page := Page{Messages: window}
	if more && len(window) > 0 {
		page.NextCursor = fmt.Sprintf("%d", window[len(window)-1].Sequence)
	}
	return page, nil
}

/*
Edit replaces what a message says.

The author is compared against the stored message rather than trusted from the
request, which is what keeps an application from rewriting one of its people's
words while claiming to be them.
*/
func (service *Service) Edit(ctx context.Context, applicationID, id string,
	author Author, body string) (Message, error) {
	existing, err := service.Get(ctx, applicationID, id)
	if err != nil {
		return Message{}, err
	}

	normalized, err := NormalizeBody(body)
	if err != nil {
		return Message{}, err
	}

	if existing.Deleted() {
		return Message{}, ErrDeleted
	}

	if existing.Author != author {
		return Message{}, ErrNotAuthor
	}

	message, err := service.store.Edit(ctx, applicationID, id, normalized, service.now())
	if err != nil {
		return Message{}, err
	}

	service.record(ctx, "message.edited", message)
	return message, nil
}

/*
Delete withdraws a message, leaving a tombstone where it was.

Deleting something already deleted is not an error: the caller asked for a state
the message is in, and reporting failure would make a retried request look like
a problem.
*/
func (service *Service) Delete(ctx context.Context, applicationID, id string,
	author Author) (Message, error) {
	existing, err := service.Get(ctx, applicationID, id)
	if err != nil {
		return Message{}, err
	}

	if existing.Author != author {
		return Message{}, ErrNotAuthor
	}

	if existing.Deleted() {
		return existing, nil
	}

	message, err := service.store.Delete(ctx, applicationID, id, service.now())
	if err != nil {
		return Message{}, err
	}

	service.record(ctx, "message.deleted", message)
	return message, nil
}

/*
requireAuthor checks that a message can be truthfully attributed.

A user must be one of this application's and must not be suspended or deleted.
A guest must be an invitation this application issued, and must be one that
names nobody — an invitation issued *for* a user is that user's, and accepting
it as a guest identity would let the same person appear in one room under two
different names.

Whether the author may still write — a guest whose call has ended, somebody
removed from a room — is per-person authorization and is not decided here.
*/
func (service *Service) requireAuthor(ctx context.Context, applicationID string, author Author) error {
	if !author.Valid() {
		return ValidationError{
			Field:   "author",
			Message: "A message names a user or an invitation, and exactly one of the two.",
		}
	}

	if author.Guest() {
		invitation, err := service.invitations.Get(ctx, applicationID, author.InvitationID)
		if errors.Is(err, invitations.ErrNotFound) {
			return ValidationError{Field: "author", Message: "The invitation does not exist."}
		}

		if err != nil {
			return fmt.Errorf("read the author's invitation: %w", err)
		}

		if !invitation.Guest() {
			return ValidationError{
				Field:   "author",
				Message: "The invitation names a user, who writes as that user rather than as a guest.",
			}
		}

		return nil
	}

	person, err := service.users.Get(ctx, applicationID, author.UserID)
	if errors.Is(err, users.ErrNotFound) {
		return ValidationError{Field: "author", Message: "The user does not exist."}
	}

	if err != nil {
		return fmt.Errorf("read the author: %w", err)
	}

	if person.Status != users.StatusActive {
		return ValidationError{
			Field:   "author",
			Message: "The user is not active and cannot be the author of a message.",
		}
	}

	return nil
}

/*
requireRoom resolves the room a message belongs to.

A deleted room is reported as missing rather than as deleted, because it is gone
from the API and a caller must not be able to learn that an identifier once
named something.
*/
func (service *Service) requireRoom(ctx context.Context, applicationID, roomID string) (rooms.Room, error) {
	if !rooms.ValidID(roomID) {
		return rooms.Room{}, ErrRoomNotFound
	}

	room, err := service.rooms.Get(ctx, applicationID, roomID)
	if errors.Is(err, rooms.ErrNotFound) || errors.Is(err, rooms.ErrApplicationNotFound) {
		return rooms.Room{}, ErrRoomNotFound
	}

	if err != nil {
		return rooms.Room{}, fmt.Errorf("read the room: %w", err)
	}

	if room.Status == rooms.StatusDeleted {
		return rooms.Room{}, ErrRoomNotFound
	}

	return room, nil
}

// requireApplication refuses to serve a tenant Convia has stopped serving.
func (service *Service) requireApplication(ctx context.Context, applicationID string) error {
	active, err := service.tenants.Active(ctx, applicationID)
	if err != nil {
		return fmt.Errorf("check the application: %w", err)
	}

	if !active {
		return ErrRoomNotFound
	}

	return nil
}

/*
record writes the audit line for something that happened to a message.

**The body is never logged.** An audit trail says that somebody wrote in a room
and when; repeating what they wrote would put every private conversation in the
log, which is shipped and retained and read by people who are not in the room.
*/
func (service *Service) record(ctx context.Context, event string, message Message) {
	attributes := []any{
		"message_id", message.ID,
		"application_id", message.ApplicationID,
		"room_id", message.RoomID,
		"sequence", message.Sequence,
		"request_id", api.RequestIDFromContext(ctx),
	}

	if message.Author.Guest() {
		attributes = append(attributes, "author_invitation_id", message.Author.InvitationID)
	} else {
		attributes = append(attributes, "author_user_id", message.Author.UserID)
	}

	service.logger.InfoContext(ctx, event, attributes...)
}
