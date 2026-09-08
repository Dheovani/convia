package credentials

import (
	"context"

	"convia/internal/operator"
)

/*
OperatorAuthorized is the credentials service acting with the authority of one
verified operator.

This is how a tenant gets its first key, which is the bootstrap the operator
surface exists for: an application cannot issue its own first credential,
because issuing requires presenting one.

There is deliberately no subset rule here, unlike [Authorized.Issue]. An
operator is not escalating when it grants an application scope it does not
itself hold, because operator scopes and application scopes are different
authorities entirely — ScopeTenantsWrite *is* the authority to mint tenant
keys. What bounds it is that the scopes must be ones the application surface
recognizes, which normalization enforces.
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

// Issue creates a credential for a tenant, returning its secret once.
func (authorized *OperatorAuthorized) Issue(ctx context.Context, applicationID string, request Request) (Credential, Secret, error) {
	if err := authorized.permit(operator.ScopeTenantsWrite); err != nil {
		return Credential{}, "", err
	}
	return authorized.service.Issue(ctx, applicationID, request)
}

// Get returns one of a tenant's credentials, never its secret.
func (authorized *OperatorAuthorized) Get(ctx context.Context, applicationID, id string) (Credential, error) {
	if err := authorized.permit(operator.ScopeTenantsRead); err != nil {
		return Credential{}, err
	}
	return authorized.service.Get(ctx, applicationID, id)
}

// List returns one page of a tenant's credentials.
func (authorized *OperatorAuthorized) List(ctx context.Context, applicationID string, options ListOptions) (Page, error) {
	if err := authorized.permit(operator.ScopeTenantsRead); err != nil {
		return Page{}, err
	}
	return authorized.service.List(ctx, applicationID, options)
}

// Revoke withdraws one of a tenant's credentials immediately.
func (authorized *OperatorAuthorized) Revoke(ctx context.Context, applicationID, id string) error {
	if err := authorized.permit(operator.ScopeTenantsWrite); err != nil {
		return err
	}
	return authorized.service.Revoke(ctx, applicationID, id)
}
