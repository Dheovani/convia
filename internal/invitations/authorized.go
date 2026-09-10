package invitations

import (
	"context"
	"errors"

	"convia/internal/credentials"
	"convia/internal/media"
	"convia/internal/participants"
	"convia/internal/secret"
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
	Issue(ctx context.Context, applicationID, callID string, request Request) (Invitation, secret.Value, error)
	Get(ctx context.Context, applicationID, id string) (Invitation, error)
	List(ctx context.Context, applicationID string, options ListOptions) (Page, error)
	Revoke(ctx context.Context, applicationID, id string) (Invitation, error)
	Redeem(ctx context.Context, invitation Invitation) (Invitation, participants.Participant, media.Credential, error)
	Decline(ctx context.Context, invitation Invitation) (Invitation, error)
}

/*
Authorized is the invitations service acting with the authority of one verified
application.

The application is taken from the verified principal and never from the
request, and no method runs without the scope it requires, so reaching these
operations through some future route, command, or background job cannot skip
the check.

Redeeming and declining are deliberately absent. They are not an application's
to perform: the whole reason invitations waited for M13 is that the party
presenting one must not be the party that granted it, and an application able
to redeem its own invitation would put that back exactly as it was.
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

// Issue creates an invitation to one of the application's own calls.
func (authorized *Authorized) Issue(ctx context.Context, callID string, request Request) (Invitation, secret.Value, error) {
	if err := authorized.permit(credentials.ScopeInvitationsWrite); err != nil {
		return Invitation{}, "", err
	}
	return authorized.service.Issue(ctx, authorized.principal.ApplicationID, callID, request)
}

// Get returns one of the application's own invitations.
func (authorized *Authorized) Get(ctx context.Context, id string) (Invitation, error) {
	if err := authorized.permit(credentials.ScopeInvitationsRead); err != nil {
		return Invitation{}, err
	}
	return authorized.service.Get(ctx, authorized.principal.ApplicationID, id)
}

// List returns one page of the application's own invitations.
func (authorized *Authorized) List(ctx context.Context, options ListOptions) (Page, error) {
	if err := authorized.permit(credentials.ScopeInvitationsRead); err != nil {
		return Page{}, err
	}
	return authorized.service.List(ctx, authorized.principal.ApplicationID, options)
}

// Revoke withdraws one of the application's own invitations.
func (authorized *Authorized) Revoke(ctx context.Context, id string) (Invitation, error) {
	if err := authorized.permit(credentials.ScopeInvitationsWrite); err != nil {
		return Invitation{}, err
	}
	return authorized.service.Revoke(ctx, authorized.principal.ApplicationID, id)
}
