package calls

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
	Start(ctx context.Context, applicationID, roomID string, definition Definition, by Actor) (Call, error)
	Get(ctx context.Context, applicationID, id string) (Call, error)
	List(ctx context.Context, applicationID string, options ListOptions) (Page, error)
	End(ctx context.Context, applicationID, id string, by Actor, reason string) (Call, error)
}

/*
Authorized is the calls service acting with the authority of one verified
application.

The application is taken from the verified principal and never from the
request, and no method runs without the scope it requires, so reaching these
operations through some future route, command, or background job cannot skip
the check. There is no request field that could name a different application,
which makes a tenant-crossing bug unrepresentable rather than merely unlikely.

The actor recorded on a transition is decided here too, for the same reason:
who is acting is something the credential proved, not something a request body
may claim.
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

// Start begins a conversation in one of the caller's own rooms.
func (authorized *Authorized) Start(ctx context.Context, roomID string, definition Definition) (Call, error) {
	if err := authorized.permit(credentials.ScopeCallsWrite); err != nil {
		return Call{}, err
	}
	return authorized.service.Start(ctx, authorized.principal.ApplicationID, roomID, definition, ActorApplication)
}

// Get returns one of the caller's own calls.
func (authorized *Authorized) Get(ctx context.Context, id string) (Call, error) {
	if err := authorized.permit(credentials.ScopeCallsRead); err != nil {
		return Call{}, err
	}
	return authorized.service.Get(ctx, authorized.principal.ApplicationID, id)
}

// List returns one page of the caller's own calls.
func (authorized *Authorized) List(ctx context.Context, options ListOptions) (Page, error) {
	if err := authorized.permit(credentials.ScopeCallsRead); err != nil {
		return Page{}, err
	}
	return authorized.service.List(ctx, authorized.principal.ApplicationID, options)
}

// End stops one of the caller's own conversations.
func (authorized *Authorized) End(ctx context.Context, id, reason string) (Call, error) {
	if err := authorized.permit(credentials.ScopeCallsWrite); err != nil {
		return Call{}, err
	}
	return authorized.service.End(ctx, authorized.principal.ApplicationID, id, ActorApplication, reason)
}
