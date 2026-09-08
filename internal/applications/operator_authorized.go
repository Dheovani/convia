package applications

import (
	"context"
	"errors"

	"convia/internal/operator"
)

/*
ErrForbidden reports an operation the caller's operator credential does not
permit.

It is distinct from an authentication failure because the remedy differs: the
caller proved who it is, but was not granted this. Presenting a different key
will not help; being granted the scope will.
*/
var ErrForbidden = errors.New("operator credential does not permit this operation")

/*
OperatorAuthorized is the applications service acting with the authority of one
verified operator.

Managing tenants is an operator action, not something an application does to
itself, so there is no tenant-facing counterpart to this type. The application
being acted on comes from the request path, which is correct here and only
here: an operator acts on someone else by definition, and the authority to do
so was proved by the key rather than by the path.
*/
type OperatorAuthorized struct {
	service   service
	principal operator.Principal
}

/*
AuthorizeOperator binds the service to the authority of a verified operator.

It takes the same narrow behavior the HTTP layer consumes, so the authorization
rules can be tested without PostgreSQL. *Service satisfies it.
*/
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

// Create registers a new tenant.
func (authorized *OperatorAuthorized) Create(ctx context.Context, name string) (Application, error) {
	if err := authorized.permit(operator.ScopeApplicationsWrite); err != nil {
		return Application{}, err
	}
	return authorized.service.Create(ctx, name)
}

// Get returns one tenant.
func (authorized *OperatorAuthorized) Get(ctx context.Context, id string) (Application, error) {
	if err := authorized.permit(operator.ScopeApplicationsRead); err != nil {
		return Application{}, err
	}
	return authorized.service.Get(ctx, id)
}

// List returns one page of tenants.
func (authorized *OperatorAuthorized) List(ctx context.Context, options ListOptions) (Page, error) {
	if err := authorized.permit(operator.ScopeApplicationsRead); err != nil {
		return Page{}, err
	}
	return authorized.service.List(ctx, options)
}

// Rename changes a tenant's name under optimistic concurrency.
func (authorized *OperatorAuthorized) Rename(ctx context.Context, id, name, expectedVersion string) (Application, error) {
	if err := authorized.permit(operator.ScopeApplicationsWrite); err != nil {
		return Application{}, err
	}
	return authorized.service.Rename(ctx, id, name, expectedVersion)
}

/*
Suspend stops Convia serving a tenant.

This withdraws every credential the application holds at once, because
authentication checks that the application is active on every request. It is
therefore the widest single action an operator can take, and it needs the write
scope for that reason.
*/
func (authorized *OperatorAuthorized) Suspend(ctx context.Context, id string) (Application, error) {
	if err := authorized.permit(operator.ScopeApplicationsWrite); err != nil {
		return Application{}, err
	}
	return authorized.service.Suspend(ctx, id)
}

// Activate restores a suspended tenant, and with it every key it holds.
func (authorized *OperatorAuthorized) Activate(ctx context.Context, id string) (Application, error) {
	if err := authorized.permit(operator.ScopeApplicationsWrite); err != nil {
		return Application{}, err
	}
	return authorized.service.Activate(ctx, id)
}

// Delete removes a tenant from the API surface.
func (authorized *OperatorAuthorized) Delete(ctx context.Context, id string) error {
	if err := authorized.permit(operator.ScopeApplicationsWrite); err != nil {
		return err
	}
	return authorized.service.Delete(ctx, id)
}
