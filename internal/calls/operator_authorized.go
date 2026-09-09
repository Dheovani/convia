package calls

import (
	"context"

	"convia/internal/operator"
)

/*
OperatorAuthorized is the calls service acting with the authority of one
verified operator.

It differs from [Authorized] in where the tenant comes from. An application
addressing its own calls takes the tenant from its key, so no request field
could name another. An operator acts on someone else by definition, so the
tenant comes from the path — and the authority to cross into it was proved by
an operator key carrying a tenants scope, which no application key can hold.

**An operator cannot start a call.** Every other resource an operator
administers, it can also create, and this one deliberately breaks that
symmetry. Starting a conversation between an application's people is not
administration; it would put Convia in the position of originating something
nobody asked for. Ending one is administration: it is the lever an operator
needs when a conversation must stop and the application cannot stop it.
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

// Get returns one of a tenant's calls.
func (authorized *OperatorAuthorized) Get(ctx context.Context, applicationID, id string) (Call, error) {
	if err := authorized.permit(operator.ScopeTenantsRead); err != nil {
		return Call{}, err
	}
	return authorized.service.Get(ctx, applicationID, id)
}

// List returns one page of a tenant's calls.
func (authorized *OperatorAuthorized) List(ctx context.Context, applicationID string, options ListOptions) (Page, error) {
	if err := authorized.permit(operator.ScopeTenantsRead); err != nil {
		return Page{}, err
	}
	return authorized.service.List(ctx, applicationID, options)
}

// End stops a tenant's conversation, recording that an operator did it.
func (authorized *OperatorAuthorized) End(ctx context.Context, applicationID, id, reason string) (Call, error) {
	if err := authorized.permit(operator.ScopeTenantsWrite); err != nil {
		return Call{}, err
	}
	return authorized.service.End(ctx, applicationID, id, ActorOperator, reason)
}
