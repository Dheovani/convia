package credentials

import (
	"context"
	"errors"
	"fmt"
)

/*
ErrForbidden reports an operation the caller's credential does not permit.

It is distinct from ErrUnauthenticated because the remedy differs: the caller
proved who it is, but was not granted this. Presenting a different key will not
help; being granted the scope will.
*/
var ErrForbidden = errors.New("credential does not permit this operation")

/*
Authorized is the credential service acting with the authority of one verified
caller.

It exists so that authorization is a property of the operation rather than
something a handler has to remember. The application is taken from the verified
principal and never from the request, and no method runs without the scope it
requires, so reaching these operations through some future route, command, or
background job cannot skip the check.
*/
type Authorized struct {
	service   service
	principal Principal
}

/*
Authorize binds the service to the authority of a verified caller.

It takes the same narrow behavior the HTTP layer consumes, so that the
authorization rules can be tested without PostgreSQL. *Service satisfies it.
*/
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
Issue creates a credential for the caller's own application.

The requested scopes must be a subset of the caller's own. Without that rule a
key holding only credentials:write could mint one holding users:write, and
every credential would effectively carry every permission.
*/
func (authorized *Authorized) Issue(ctx context.Context, request Request) (Credential, Secret, error) {
	if err := authorized.permit(ScopeCredentialsWrite); err != nil {
		return Credential{}, "", err
	}

	for _, scope := range request.Scopes {
		if !authorized.principal.Allows(scope) {
			return Credential{}, "", fmt.Errorf("%w: it does not carry %q", ErrForbidden, string(scope))
		}
	}

	return authorized.service.Issue(ctx, authorized.principal.ApplicationID, request)
}

// Get returns one credential of the caller's own application.
func (authorized *Authorized) Get(ctx context.Context, id string) (Credential, error) {
	if err := authorized.permit(ScopeCredentialsRead); err != nil {
		return Credential{}, err
	}
	return authorized.service.Get(ctx, authorized.principal.ApplicationID, id)
}

// List returns one page of the caller's own credentials.
func (authorized *Authorized) List(ctx context.Context, options ListOptions) (Page, error) {
	if err := authorized.permit(ScopeCredentialsRead); err != nil {
		return Page{}, err
	}
	return authorized.service.List(ctx, authorized.principal.ApplicationID, options)
}

/*
Revoke withdraws one of the caller's own credentials.

A caller may revoke the key it is presenting. That is deliberate: during an
incident the holder of a leaked key is often the only party able to act
immediately, and refusing would protect nothing.
*/
func (authorized *Authorized) Revoke(ctx context.Context, id string) error {
	if err := authorized.permit(ScopeCredentialsWrite); err != nil {
		return err
	}
	return authorized.service.Revoke(ctx, authorized.principal.ApplicationID, id)
}
