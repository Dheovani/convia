package participants

import (
	"context"

	"convia/internal/operator"
)

/*
OperatorAuthorized is the participants service acting with the authority of one
verified operator.

It differs from [Authorized] in where the tenant comes from. An application
addressing its own calls takes the tenant from its key, so no request field
could name another. An operator acts on someone else by definition, so the
tenant comes from the path — and the authority to cross into it was proved by
an operator key carrying a tenants scope, which no application key can hold.

**An operator may read a roster and remove someone from it, and nothing else.**
That is the same line the call domain draws, for the same reason. Removing is
administration: it is the lever an operator needs when someone must be put out
of a conversation and the application cannot do it. Admitting someone, or
promoting them to moderator, is not — it would put Convia in the position of
arranging a conversation nobody asked it to arrange.
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

// Get returns one of a tenant's participants.
func (authorized *OperatorAuthorized) Get(ctx context.Context, applicationID, id string) (Participant, error) {
	if err := authorized.permit(operator.ScopeTenantsRead); err != nil {
		return Participant{}, err
	}
	return authorized.service.Get(ctx, applicationID, id)
}

// List returns one page of a tenant's call roster.
func (authorized *OperatorAuthorized) List(ctx context.Context, applicationID, callID string,
	options ListOptions) (Page, error) {
	if err := authorized.permit(operator.ScopeTenantsRead); err != nil {
		return Page{}, err
	}
	return authorized.service.List(ctx, applicationID, callID, options)
}

/*
Remove puts someone out of a tenant's call, recording that an operator did it.

No acting participant is named. An operator acts on its own authority from
outside the conversation, so there is no moderator for Convia to check and none
to credit: the record says an operator did this.
*/
func (authorized *OperatorAuthorized) Remove(ctx context.Context, applicationID, id, reason string) (Participant, error) {
	if err := authorized.permit(operator.ScopeTenantsWrite); err != nil {
		return Participant{}, err
	}
	return authorized.service.Remove(ctx, applicationID, id, RemoverOperator, "", reason)
}
