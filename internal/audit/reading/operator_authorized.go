/*
Package reading is the audit trail as an operator reaches it.

It is a package of its own for a reason that is structural rather than tidy:
the trail's core is written to by every domain, including the one that issues
operator keys, and reading the trail is authorized by an operator scope. Kept
together, the two would need each other -- the operator package recording to
the trail, the trail checking an operator's scope -- and Go refuses the cycle.
Apart, the core depends on nothing, which is what lets everything else depend
on it.
*/
package reading

import (
	"context"
	"errors"

	"convia/internal/audit"
	"convia/internal/operator"
)

/*
ErrForbidden reports a caller asking the trail something their scopes do not
permit.

It is the same refusal every other domain makes for the same reason: an operator
without the scope learns that they lack it, and nothing about what the trail
holds, because the question never reaches it.
*/
var ErrForbidden = errors.New("the credential does not permit reading the audit trail")

/*
OperatorAuthorized is the audit service acting with the authority of one
verified operator.

**Reading the trail is its own scope**, and that is the "strict access controls"
half of `M21-008` rather than a preference. Every other operator scope is
bounded by what it is about -- tenants, their credentials, their rooms -- and
the trail is bounded by nothing: it holds every action every tenant's key ever
took, which rooms exist, which people were removed from them, and when. An
operator who can list applications has been given a directory; an operator who
can read the trail has been given the history of the installation. Folding the
second into the first would hand it to everybody who needed the first.

There is no write side here. Entries are written by the change that caused them,
by the service making that change, and an operator who could append one could
write a history that did not happen.
*/
type OperatorAuthorized struct {
	service   searcher
	principal operator.Principal
}

// searcher is the read half of the service, which is all an operator reaches.
type searcher interface {
	Search(ctx context.Context, options audit.SearchOptions) (audit.Page, error)
}

// AuthorizeOperator binds the service to the authority of a verified operator.
func AuthorizeOperator(service searcher, principal operator.Principal) *OperatorAuthorized {
	return &OperatorAuthorized{service: service, principal: principal}
}

// Search answers a question about the trail for an operator permitted to ask.
func (authorized *OperatorAuthorized) Search(ctx context.Context, options audit.SearchOptions) (audit.Page, error) {
	if !authorized.principal.Allows(operator.ScopeAuditRead) {
		return audit.Page{}, ErrForbidden
	}
	return authorized.service.Search(ctx, options)
}
