package users

import (
	"context"
	"errors"

	"convia/internal/credentials"
)

/*
ErrForbidden reports an operation the caller's credential does not permit.

It is distinct from a missing user: the caller proved who it is and the user may
well exist, but this credential was not granted the operation.
*/
var ErrForbidden = errors.New("credential does not permit this operation")

/*
Authorized is the user service acting with the authority of one verified caller.

It exists so that authorization is a property of the operation rather than
something a handler has to remember. The application is taken from the verified
principal and never from the request, which removes the possibility of a
tenant-crossing bug at the transport layer: there is no request field that
could name a different application.
*/
type Authorized struct {
	service   service
	principal credentials.Principal
}

/*
Authorize binds the service to the authority of a verified caller.

It takes the same narrow behavior the HTTP layer consumes, so that the
authorization rules can be tested without PostgreSQL. *Service satisfies it.
*/
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

// Resolve maps one of the caller's own people to a Convia user.
func (authorized *Authorized) Resolve(ctx context.Context, identity Identity) (User, bool, error) {
	if err := authorized.permit(credentials.ScopeUsersWrite); err != nil {
		return User{}, false, err
	}
	return authorized.service.Resolve(ctx, authorized.principal.ApplicationID, identity)
}

// Get returns one of the caller's own users.
func (authorized *Authorized) Get(ctx context.Context, id string) (User, error) {
	if err := authorized.permit(credentials.ScopeUsersRead); err != nil {
		return User{}, err
	}
	return authorized.service.Get(ctx, authorized.principal.ApplicationID, id)
}

// List returns one page of the caller's own users.
func (authorized *Authorized) List(ctx context.Context, options ListOptions) (Page, error) {
	if err := authorized.permit(credentials.ScopeUsersRead); err != nil {
		return Page{}, err
	}
	return authorized.service.List(ctx, authorized.principal.ApplicationID, options)
}

// Update changes the attributes of one of the caller's own users.
func (authorized *Authorized) Update(ctx context.Context, id string,
	attributes Attributes, expectedVersion string) (User, error) {
	if err := authorized.permit(credentials.ScopeUsersWrite); err != nil {
		return User{}, err
	}
	return authorized.service.Update(ctx, authorized.principal.ApplicationID, id, attributes, expectedVersion)
}

// Suspend withdraws one of the caller's own users.
func (authorized *Authorized) Suspend(ctx context.Context, id string) (User, error) {
	if err := authorized.permit(credentials.ScopeUsersWrite); err != nil {
		return User{}, err
	}
	return authorized.service.Suspend(ctx, authorized.principal.ApplicationID, id)
}

// Activate restores one of the caller's own suspended users.
func (authorized *Authorized) Activate(ctx context.Context, id string) (User, error) {
	if err := authorized.permit(credentials.ScopeUsersWrite); err != nil {
		return User{}, err
	}
	return authorized.service.Activate(ctx, authorized.principal.ApplicationID, id)
}

// Delete removes one of the caller's own users from the API surface.
func (authorized *Authorized) Delete(ctx context.Context, id string) error {
	if err := authorized.permit(credentials.ScopeUsersWrite); err != nil {
		return err
	}
	return authorized.service.Delete(ctx, authorized.principal.ApplicationID, id)
}
