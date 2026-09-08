package rooms

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
	Create(ctx context.Context, applicationID string, definition Definition) (Room, error)
	Get(ctx context.Context, applicationID, id string) (Room, error)
	GetByAlias(ctx context.Context, applicationID, alias string) (Room, error)
	List(ctx context.Context, applicationID string, options ListOptions) (Page, error)
	Update(ctx context.Context, applicationID, id string, change Change, expectedVersion string) (Room, error)
	Close(ctx context.Context, applicationID, id string) (Room, error)
	Reopen(ctx context.Context, applicationID, id string) (Room, error)
	Delete(ctx context.Context, applicationID, id string) error
}

/*
Authorized is the rooms service acting with the authority of one verified
application.

The application is taken from the verified principal and never from the
request, and no method runs without the scope it requires, so reaching these
operations through some future route, command, or background job cannot skip
the check. There is no request field that could name a different application,
which makes a tenant-crossing bug unrepresentable rather than merely unlikely.
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

// Create registers a room for the caller's own application.
func (authorized *Authorized) Create(ctx context.Context, definition Definition) (Room, error) {
	if err := authorized.permit(credentials.ScopeRoomsWrite); err != nil {
		return Room{}, err
	}
	return authorized.service.Create(ctx, authorized.principal.ApplicationID, definition)
}

// Get returns one of the caller's own rooms.
func (authorized *Authorized) Get(ctx context.Context, id string) (Room, error) {
	if err := authorized.permit(credentials.ScopeRoomsRead); err != nil {
		return Room{}, err
	}
	return authorized.service.Get(ctx, authorized.principal.ApplicationID, id)
}

// GetByAlias returns the caller's own room by the name it chose.
func (authorized *Authorized) GetByAlias(ctx context.Context, alias string) (Room, error) {
	if err := authorized.permit(credentials.ScopeRoomsRead); err != nil {
		return Room{}, err
	}
	return authorized.service.GetByAlias(ctx, authorized.principal.ApplicationID, alias)
}

// List returns one page of the caller's own rooms.
func (authorized *Authorized) List(ctx context.Context, options ListOptions) (Page, error) {
	if err := authorized.permit(credentials.ScopeRoomsRead); err != nil {
		return Page{}, err
	}
	return authorized.service.List(ctx, authorized.principal.ApplicationID, options)
}

// Update changes attributes of the caller's own room.
func (authorized *Authorized) Update(ctx context.Context, id string,
	change Change, expectedVersion string) (Room, error) {
	if err := authorized.permit(credentials.ScopeRoomsWrite); err != nil {
		return Room{}, err
	}
	return authorized.service.Update(ctx, authorized.principal.ApplicationID, id, change, expectedVersion)
}

// Close stops one of the caller's own rooms accepting new calls.
func (authorized *Authorized) Close(ctx context.Context, id string) (Room, error) {
	if err := authorized.permit(credentials.ScopeRoomsWrite); err != nil {
		return Room{}, err
	}
	return authorized.service.Close(ctx, authorized.principal.ApplicationID, id)
}

// Reopen returns one of the caller's own closed rooms to service.
func (authorized *Authorized) Reopen(ctx context.Context, id string) (Room, error) {
	if err := authorized.permit(credentials.ScopeRoomsWrite); err != nil {
		return Room{}, err
	}
	return authorized.service.Reopen(ctx, authorized.principal.ApplicationID, id)
}

// Delete removes one of the caller's own rooms from the API surface.
func (authorized *Authorized) Delete(ctx context.Context, id string) error {
	if err := authorized.permit(credentials.ScopeRoomsWrite); err != nil {
		return err
	}
	return authorized.service.Delete(ctx, authorized.principal.ApplicationID, id)
}
