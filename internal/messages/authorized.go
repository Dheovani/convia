package messages

import (
	"context"
	"errors"

	"convia/internal/credentials"
)

/*
ErrForbidden reports an operation the caller's credential does not permit.

It is distinct from an authentication failure because the remedy differs: the
caller proved who it is, but was not granted this. Presenting a different key
will not help; being granted the scope will.
*/
var ErrForbidden = errors.New("credential does not permit this operation")

/*
service is the behavior the authorization and HTTP layers consume.

It is declared here, in the consuming layer, so that both can be tested without
PostgreSQL. *Service satisfies it.
*/
type service interface {
	Post(ctx context.Context, applicationID, roomID string, author Author, body string) (Message, error)
	Get(ctx context.Context, applicationID, id string) (Message, error)
	History(ctx context.Context, applicationID, roomID string, options HistoryOptions) (Page, error)
	Edit(ctx context.Context, applicationID, id string, author Author, body string) (Message, error)
	Delete(ctx context.Context, applicationID, id string, author Author) (Message, error)
	MarkRead(ctx context.Context, applicationID, roomID, userID string, sequence int64) (ReadState, error)
	ReadState(ctx context.Context, applicationID, roomID, userID string) (ReadState, error)
}

/*
Authorized is the messages service acting with the authority of one verified
application.

The application is taken from the verified principal and never from the
request, and no method runs without the scope it requires, so reaching these
operations through some future route, command, or background job cannot skip
the check. There is no request field that could name a different application,
which makes a tenant-crossing bug unrepresentable rather than merely unlikely.

**The author is a different question from the authority.** The credential says
which application is calling; the request says which of that application's
people is speaking. Convia checks that the second is one of the first's, and
checks nothing else about them — deciding whether a particular person may be in
a particular room is per-person authorization, which does not exist yet.
*/
type Authorized struct {
	service   service
	principal credentials.Principal
}

// Authorize binds the service to the authority of a verified caller.
func Authorize(service service, principal credentials.Principal) *Authorized {
	return &Authorized{service: service, principal: principal}
}

// permit refuses an operation the caller was not granted.
func (authorized *Authorized) permit(scope credentials.Scope) error {
	if !authorized.principal.Allows(scope) {
		return ErrForbidden
	}
	return nil
}

// Post records something one of the caller's own people said.
func (authorized *Authorized) Post(ctx context.Context, roomID string,
	author Author, body string) (Message, error) {
	if err := authorized.permit(credentials.ScopeMessagesWrite); err != nil {
		return Message{}, err
	}
	return authorized.service.Post(ctx, authorized.principal.ApplicationID, roomID, author, body)
}

// Get returns one message from the caller's own application.
func (authorized *Authorized) Get(ctx context.Context, id string) (Message, error) {
	if err := authorized.permit(credentials.ScopeMessagesRead); err != nil {
		return Message{}, err
	}
	return authorized.service.Get(ctx, authorized.principal.ApplicationID, id)
}

// History returns one window of a room's history.
func (authorized *Authorized) History(ctx context.Context, roomID string,
	options HistoryOptions) (Page, error) {
	if err := authorized.permit(credentials.ScopeMessagesRead); err != nil {
		return Page{}, err
	}
	return authorized.service.History(ctx, authorized.principal.ApplicationID, roomID, options)
}

// Edit replaces what one of the caller's own people said.
func (authorized *Authorized) Edit(ctx context.Context, id string,
	author Author, body string) (Message, error) {
	if err := authorized.permit(credentials.ScopeMessagesWrite); err != nil {
		return Message{}, err
	}
	return authorized.service.Edit(ctx, authorized.principal.ApplicationID, id, author, body)
}

// Delete withdraws a message one of the caller's own people wrote.
func (authorized *Authorized) Delete(ctx context.Context, id string, author Author) (Message, error) {
	if err := authorized.permit(credentials.ScopeMessagesWrite); err != nil {
		return Message{}, err
	}
	return authorized.service.Delete(ctx, authorized.principal.ApplicationID, id, author)
}

// MarkRead records that one of the caller's own people has read a room.
func (authorized *Authorized) MarkRead(ctx context.Context, roomID, userID string, sequence int64) (ReadState, error) {
	if err := authorized.permit(credentials.ScopeMessagesWrite); err != nil {
		return ReadState{}, err
	}
	return authorized.service.MarkRead(ctx, authorized.principal.ApplicationID, roomID, userID, sequence)
}

/*
ReadState reports how far one of the caller's own people has read in a room.

It needs the read scope rather than the write one even though marking needs
write: the unread count is derived from the history, so answering it is telling
the caller how much of a conversation exists that they have not seen.
*/
func (authorized *Authorized) ReadState(ctx context.Context, roomID, userID string) (ReadState, error) {
	if err := authorized.permit(credentials.ScopeMessagesRead); err != nil {
		return ReadState{}, err
	}
	return authorized.service.ReadState(ctx, authorized.principal.ApplicationID, roomID, userID)
}
