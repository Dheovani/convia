package presence

import (
	"context"
	"errors"

	"convia/internal/credentials"
)

/*
ErrForbidden reports an operation the caller's credential does not permit.

It is distinct from a missing user: the caller proved who it is and the person
may well exist, but this credential was not granted the operation.
*/
var ErrForbidden = errors.New("credential does not permit this operation")

/*
service is the behavior the HTTP layer consumes.

It is declared here rather than in the handler so that authorization is the
only way to reach the service from outside the package, and so the rules can be
tested without a store of any kind.
*/
type service interface {
	Assert(ctx context.Context, applicationID, userID string, assertion Assertion) (Presence, error)
	Clear(ctx context.Context, applicationID, userID, deviceID string) (Presence, error)
	Get(ctx context.Context, applicationID, userID string) (Presence, error)
	GetMany(ctx context.Context, applicationID string, userIDs []string) ([]Presence, error)
}

/*
Authorized is the presence service acting with the authority of one verified
caller.

The application comes from the verified principal and never from the request,
so there is no field anywhere that could name another tenant's people. Reading
and writing are separate permissions, because reporting where somebody is and
learning where they are are different things to be trusted with.
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

// Assert records or refreshes what one of the caller's devices says.
func (authorized *Authorized) Assert(ctx context.Context, userID string, assertion Assertion) (Presence, error) {
	if err := authorized.permit(credentials.ScopePresenceWrite); err != nil {
		return Presence{}, err
	}
	return authorized.service.Assert(ctx, authorized.principal.ApplicationID, userID, assertion)
}

// Clear withdraws what one device said, or everything said about a person.
func (authorized *Authorized) Clear(ctx context.Context, userID, deviceID string) (Presence, error) {
	if err := authorized.permit(credentials.ScopePresenceWrite); err != nil {
		return Presence{}, err
	}
	return authorized.service.Clear(ctx, authorized.principal.ApplicationID, userID, deviceID)
}

// Get reports what Convia will say about one of the caller's people.
func (authorized *Authorized) Get(ctx context.Context, userID string) (Presence, error) {
	if err := authorized.permit(credentials.ScopePresenceRead); err != nil {
		return Presence{}, err
	}
	return authorized.service.Get(ctx, authorized.principal.ApplicationID, userID)
}

// GetMany reports what Convia will say about several of the caller's people.
func (authorized *Authorized) GetMany(ctx context.Context, userIDs []string) ([]Presence, error) {
	if err := authorized.permit(credentials.ScopePresenceRead); err != nil {
		return nil, err
	}
	return authorized.service.GetMany(ctx, authorized.principal.ApplicationID, userIDs)
}
