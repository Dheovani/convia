package operator

import (
	"context"
	"errors"
	"fmt"
)

/*
ErrForbidden reports an operation the caller's operator credential does not
permit.

It is distinct from ErrUnauthenticated because the remedy differs: the caller
proved who it is, but was not granted this. Presenting a different key will not
help; being granted the scope will.
*/
var ErrForbidden = errors.New("operator credential does not permit this operation")

/*
service is the behavior the authorization layer wraps.

It is declared here, in the consuming layer, so that the authorization rules
can be tested without PostgreSQL. *Service satisfies it.
*/
type service interface {
	Issue(ctx context.Context, request Request) (Credential, Secret, error)
	Get(ctx context.Context, id string) (Credential, error)
	List(ctx context.Context, options ListOptions) (Page, error)
	Revoke(ctx context.Context, id string) error
}

/*
Authorized is the operator credential service acting with the authority of one
verified operator.

As everywhere in Convia, authorization is a property of the operation rather
than something a handler has to remember: no method here runs without the scope
it requires, so reaching these operations through some future route, command,
or background job cannot skip the check.
*/
type Authorized struct {
	service   service
	principal Principal
}

// Authorize binds the service to the authority of a verified operator.
func Authorize(service service, principal Principal) *Authorized {
	return &Authorized{service: service, principal: principal}
}

// permit refuses an operation the caller was not granted.
func (authorized *Authorized) permit(scope Scope) error {
	if !authorized.principal.Allows(scope) {
		return ErrForbidden
	}
	return nil
}

/*
Issue creates another operator credential.

The requested scopes must be a subset of the caller's own. This is the same
rule the tenant surface applies to itself, and it matters more here: without
it, a key carrying only operators:write could mint one carrying authority over
every tenant in Convia, and every operator credential would effectively be
unlimited.

The refusal happens before the credential is created, so the escalated key
never exists even briefly.
*/
func (authorized *Authorized) Issue(ctx context.Context, request Request) (Credential, Secret, error) {
	if err := authorized.permit(ScopeOperatorsWrite); err != nil {
		return Credential{}, "", err
	}

	for _, scope := range request.Scopes {
		if !authorized.principal.Allows(scope) {
			return Credential{}, "", fmt.Errorf("%w: it does not carry %q", ErrForbidden, string(scope))
		}
	}

	return authorized.service.Issue(ctx, request)
}

// Get returns one operator credential, never its secret.
func (authorized *Authorized) Get(ctx context.Context, id string) (Credential, error) {
	if err := authorized.permit(ScopeOperatorsRead); err != nil {
		return Credential{}, err
	}
	return authorized.service.Get(ctx, id)
}

// List returns one page of operator credentials.
func (authorized *Authorized) List(ctx context.Context, options ListOptions) (Page, error) {
	if err := authorized.permit(ScopeOperatorsRead); err != nil {
		return Page{}, err
	}
	return authorized.service.List(ctx, options)
}

/*
Revoke withdraws an operator credential immediately.

A caller may revoke the key it is presenting. That is deliberate: during an
incident the holder of a leaked key is often the only party able to act
immediately, and refusing would protect nothing.
*/
func (authorized *Authorized) Revoke(ctx context.Context, id string) error {
	if err := authorized.permit(ScopeOperatorsWrite); err != nil {
		return err
	}
	return authorized.service.Revoke(ctx, id)
}
