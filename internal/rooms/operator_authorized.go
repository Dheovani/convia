package rooms

import (
	"context"

	"convia/internal/operator"
)

/*
OperatorAuthorized is the rooms service acting with the authority of one
verified operator.

It differs from [Authorized] in where the tenant comes from. An application
addressing its own rooms takes the tenant from its key, so no request field
could name another. An operator acts on someone else by definition, so the
tenant comes from the path — and the authority to cross into it was proved by
an operator key carrying a tenants scope, which no application key can hold.
*/
type OperatorAuthorized struct {
	service   service
	principal operator.Principal
}

// AuthorizeOperator binds the service to the authority of a verified operator.
func AuthorizeOperator(service service, principal operator.Principal) *OperatorAuthorized {
	return &OperatorAuthorized{service: service, principal: principal}
}

// permit refuses an operation the caller was not granted.
func (authorized *OperatorAuthorized) permit(scope operator.Scope) error {
	if !authorized.principal.Allows(scope) {
		return ErrForbidden
	}
	return nil
}

// Create registers a room for a tenant.
func (authorized *OperatorAuthorized) Create(ctx context.Context, applicationID string, definition Definition) (Room, error) {
	if err := authorized.permit(operator.ScopeTenantsWrite); err != nil {
		return Room{}, err
	}
	return authorized.service.Create(ctx, applicationID, definition)
}

// Get returns one of a tenant's rooms.
func (authorized *OperatorAuthorized) Get(ctx context.Context, applicationID, id string) (Room, error) {
	if err := authorized.permit(operator.ScopeTenantsRead); err != nil {
		return Room{}, err
	}
	return authorized.service.Get(ctx, applicationID, id)
}

// GetByAlias returns one of a tenant's rooms by the name the tenant chose.
func (authorized *OperatorAuthorized) GetByAlias(ctx context.Context, applicationID, alias string) (Room, error) {
	if err := authorized.permit(operator.ScopeTenantsRead); err != nil {
		return Room{}, err
	}
	return authorized.service.GetByAlias(ctx, applicationID, alias)
}

// List returns one page of a tenant's rooms.
func (authorized *OperatorAuthorized) List(ctx context.Context, applicationID string, options ListOptions) (Page, error) {
	if err := authorized.permit(operator.ScopeTenantsRead); err != nil {
		return Page{}, err
	}
	return authorized.service.List(ctx, applicationID, options)
}

// Update changes attributes of a tenant's room under optimistic concurrency.
func (authorized *OperatorAuthorized) Update(ctx context.Context, applicationID, id string,
	change Change, expectedVersion string) (Room, error) {
	if err := authorized.permit(operator.ScopeTenantsWrite); err != nil {
		return Room{}, err
	}
	return authorized.service.Update(ctx, applicationID, id, change, expectedVersion)
}

// Close stops a tenant's room accepting new calls.
func (authorized *OperatorAuthorized) Close(ctx context.Context, applicationID, id string) (Room, error) {
	if err := authorized.permit(operator.ScopeTenantsWrite); err != nil {
		return Room{}, err
	}
	return authorized.service.Close(ctx, applicationID, id)
}

// Reopen returns a tenant's closed room to service.
func (authorized *OperatorAuthorized) Reopen(ctx context.Context, applicationID, id string) (Room, error) {
	if err := authorized.permit(operator.ScopeTenantsWrite); err != nil {
		return Room{}, err
	}
	return authorized.service.Reopen(ctx, applicationID, id)
}

// Delete removes a tenant's room from the API surface.
func (authorized *OperatorAuthorized) Delete(ctx context.Context, applicationID, id string) error {
	if err := authorized.permit(operator.ScopeTenantsWrite); err != nil {
		return err
	}
	return authorized.service.Delete(ctx, applicationID, id)
}
