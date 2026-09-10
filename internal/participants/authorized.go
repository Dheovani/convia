package participants

import (
	"context"
	"errors"

	"convia/internal/credentials"
	"convia/internal/media"
)

/*
ErrForbidden reports an operation the caller's credential does not permit.

It is distinct from an authentication failure because the remedy differs: the
caller proved who it is, but was not granted this. Presenting a different key
will not help; being granted the scope will.

It is also distinct from [ErrNotAModerator], which is not about the credential
at all: that is a participant inside the call acting beyond their role.
*/
var ErrForbidden = errors.New("credential does not permit this operation")

/*
service is the behavior the authorization and HTTP layers consume.

It is declared here, in the consuming layer, so that both can be tested without
PostgreSQL. *Service satisfies it.
*/
type service interface {
	Join(ctx context.Context, applicationID, callID string, admission Admission) (Participant, bool, error)
	Get(ctx context.Context, applicationID, id string) (Participant, error)
	List(ctx context.Context, applicationID, callID string, options ListOptions) (Page, error)
	Leave(ctx context.Context, applicationID, id string) (Participant, error)
	Remove(ctx context.Context, applicationID, id string, authority Remover, actingID, reason string) (Participant, error)
	SetRole(ctx context.Context, applicationID, id, role, actingID string) (Participant, error)
	Session(ctx context.Context, applicationID, id string) (Participant, media.Credential, error)
}

/*
Authorized is the participants service acting with the authority of one
verified application.

The application is taken from the verified principal and never from the
request, and no method runs without the scope it requires, so reaching these
operations through some future route, command, or background job cannot skip
the check.

The removing authority is decided here too. An application removing someone
without naming a moderator does so as itself, and that is recorded as such: a
request body cannot claim an authority the credential did not prove.
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

// Join admits someone to one of the caller's own calls.
func (authorized *Authorized) Join(ctx context.Context, callID string,
	admission Admission) (Participant, bool, error) {
	if err := authorized.permit(credentials.ScopeParticipantsWrite); err != nil {
		return Participant{}, false, err
	}
	return authorized.service.Join(ctx, authorized.principal.ApplicationID, callID, admission)
}

/*
Session issues the credential a client connects with.

It requires the write scope rather than the read one. Reading who is in a call
and handing somebody the means to take part in it are different powers, and an
integration granted only the first must not be able to exercise the second.
*/
func (authorized *Authorized) Session(ctx context.Context, id string) (Participant, media.Credential, error) {
	if err := authorized.permit(credentials.ScopeParticipantsWrite); err != nil {
		return Participant{}, media.Credential{}, err
	}
	return authorized.service.Session(ctx, authorized.principal.ApplicationID, id)
}

// Get returns one participant of the caller's own call.
func (authorized *Authorized) Get(ctx context.Context, id string) (Participant, error) {
	if err := authorized.permit(credentials.ScopeParticipantsRead); err != nil {
		return Participant{}, err
	}
	return authorized.service.Get(ctx, authorized.principal.ApplicationID, id)
}

// List returns one page of a call's roster.
func (authorized *Authorized) List(ctx context.Context, callID string, options ListOptions) (Page, error) {
	if err := authorized.permit(credentials.ScopeParticipantsRead); err != nil {
		return Page{}, err
	}
	return authorized.service.List(ctx, authorized.principal.ApplicationID, callID, options)
}

// Leave records that someone left one of the caller's own calls.
func (authorized *Authorized) Leave(ctx context.Context, id string) (Participant, error) {
	if err := authorized.permit(credentials.ScopeParticipantsWrite); err != nil {
		return Participant{}, err
	}
	return authorized.service.Leave(ctx, authorized.principal.ApplicationID, id)
}

/*
Remove puts someone out of one of the caller's own calls.

An acting participant may be named, and when one is, Convia checks that they
are a moderator of the same call. Without one the application removes on its
own authority, which it holds over its own calls.
*/
func (authorized *Authorized) Remove(ctx context.Context, id, actingID, reason string) (Participant, error) {
	if err := authorized.permit(credentials.ScopeParticipantsWrite); err != nil {
		return Participant{}, err
	}
	return authorized.service.Remove(ctx, authorized.principal.ApplicationID, id,
		RemoverApplication, actingID, reason)
}

// SetRole changes what a participant of the caller's own call may do.
func (authorized *Authorized) SetRole(ctx context.Context, id, role, actingID string) (Participant, error) {
	if err := authorized.permit(credentials.ScopeParticipantsWrite); err != nil {
		return Participant{}, err
	}
	return authorized.service.SetRole(ctx, authorized.principal.ApplicationID, id, role, actingID)
}
