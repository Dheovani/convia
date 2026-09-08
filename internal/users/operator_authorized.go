package users

import (
	"context"

	"convia/internal/operator"
)

/*
OperatorAuthorized is the users service acting with the authority of one
verified operator.

It differs from [Authorized] in where the tenant comes from. An application
addressing its own users takes the tenant from its key, so no request field
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

// Resolve maps one of a tenant's people to a Convia user.
func (authorized *OperatorAuthorized) Resolve(ctx context.Context, applicationID string, identity Identity) (User, bool, error) {
	if err := authorized.permit(operator.ScopeTenantsWrite); err != nil {
		return User{}, false, err
	}
	return authorized.service.Resolve(ctx, applicationID, identity)
}

// Get returns one of a tenant's users.
func (authorized *OperatorAuthorized) Get(ctx context.Context, applicationID, id string) (User, error) {
	if err := authorized.permit(operator.ScopeTenantsRead); err != nil {
		return User{}, err
	}
	return authorized.service.Get(ctx, applicationID, id)
}

// List returns one page of a tenant's users.
func (authorized *OperatorAuthorized) List(ctx context.Context, applicationID string, options ListOptions) (Page, error) {
	if err := authorized.permit(operator.ScopeTenantsRead); err != nil {
		return Page{}, err
	}
	return authorized.service.List(ctx, applicationID, options)
}

// Update changes a tenant's user under optimistic concurrency.
func (authorized *OperatorAuthorized) Update(ctx context.Context, applicationID, id string,
	attributes Attributes, expectedVersion string) (User, error) {
	if err := authorized.permit(operator.ScopeTenantsWrite); err != nil {
		return User{}, err
	}
	return authorized.service.Update(ctx, applicationID, id, attributes, expectedVersion)
}

// Suspend withdraws a tenant's user from service.
func (authorized *OperatorAuthorized) Suspend(ctx context.Context, applicationID, id string) (User, error) {
	if err := authorized.permit(operator.ScopeTenantsWrite); err != nil {
		return User{}, err
	}
	return authorized.service.Suspend(ctx, applicationID, id)
}

// Activate restores a tenant's suspended user.
func (authorized *OperatorAuthorized) Activate(ctx context.Context, applicationID, id string) (User, error) {
	if err := authorized.permit(operator.ScopeTenantsWrite); err != nil {
		return User{}, err
	}
	return authorized.service.Activate(ctx, applicationID, id)
}

// Delete removes a tenant's user from the API surface.
func (authorized *OperatorAuthorized) Delete(ctx context.Context, applicationID, id string) error {
	if err := authorized.permit(operator.ScopeTenantsWrite); err != nil {
		return err
	}
	return authorized.service.Delete(ctx, applicationID, id)
}
