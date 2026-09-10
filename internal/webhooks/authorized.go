package webhooks

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

[Dispatcher.Enqueue] is deliberately absent. Queuing a delivery is Convia's own
act on the way out of a domain operation, not an operation an application asks
for, and there is no route, scope, or handler that could reach it.
*/
type service interface {
	Register(ctx context.Context, applicationID string, registration Registration) (Endpoint, Secret, error)
	Get(ctx context.Context, applicationID, id string) (Endpoint, error)
	List(ctx context.Context, applicationID string, options ListOptions) (Page, error)
	Update(ctx context.Context, applicationID, id string, registration Registration) (Endpoint, error)
	Rotate(ctx context.Context, applicationID, id string) (Endpoint, Secret, error)
	Enable(ctx context.Context, applicationID, id string) (Endpoint, error)
	Disable(ctx context.Context, applicationID, id string) (Endpoint, error)
	Delete(ctx context.Context, applicationID, id string) error
	GetDelivery(ctx context.Context, applicationID, id string) (Delivery, error)
	ListDeliveries(ctx context.Context, applicationID string, options DeliveryListOptions) (DeliveryPage, error)
}

/*
Authorized is the webhooks service acting with the authority of one verified
application.

The application is taken from the verified principal and never from the
request, so an endpoint belongs to whoever registered it and no request field
could name another tenant's.
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

/*
Register records a destination for the caller's own events.

The webhook scopes are separate from `events:read`, which grants a live stream.
Holding a connection open and asking Convia to reach out to an address are
different powers with different risks — the second is the one that makes Convia
send requests on a tenant's behalf — so neither implies the other.
*/
func (authorized *Authorized) Register(ctx context.Context, registration Registration) (Endpoint, Secret, error) {
	if err := authorized.permit(credentials.ScopeWebhooksWrite); err != nil {
		return Endpoint{}, "", err
	}
	return authorized.service.Register(ctx, authorized.principal.ApplicationID, registration)
}

// Get returns one of the caller's own endpoints.
func (authorized *Authorized) Get(ctx context.Context, id string) (Endpoint, error) {
	if err := authorized.permit(credentials.ScopeWebhooksRead); err != nil {
		return Endpoint{}, err
	}
	return authorized.service.Get(ctx, authorized.principal.ApplicationID, id)
}

// List returns one page of the caller's own endpoints.
func (authorized *Authorized) List(ctx context.Context, options ListOptions) (Page, error) {
	if err := authorized.permit(credentials.ScopeWebhooksRead); err != nil {
		return Page{}, err
	}
	return authorized.service.List(ctx, authorized.principal.ApplicationID, options)
}

// Update changes one of the caller's own endpoints.
func (authorized *Authorized) Update(ctx context.Context, id string, registration Registration) (Endpoint, error) {
	if err := authorized.permit(credentials.ScopeWebhooksWrite); err != nil {
		return Endpoint{}, err
	}
	return authorized.service.Update(ctx, authorized.principal.ApplicationID, id, registration)
}

/*
Rotate issues a new signing key for one of the caller's own endpoints.

It requires the write scope for the same reason registering does: this is the
only other operation that produces a secret, and a key granted to read a list of
destinations must not be able to mint one.
*/
func (authorized *Authorized) Rotate(ctx context.Context, id string) (Endpoint, Secret, error) {
	if err := authorized.permit(credentials.ScopeWebhooksWrite); err != nil {
		return Endpoint{}, "", err
	}
	return authorized.service.Rotate(ctx, authorized.principal.ApplicationID, id)
}

// Enable resumes delivery to one of the caller's own endpoints.
func (authorized *Authorized) Enable(ctx context.Context, id string) (Endpoint, error) {
	if err := authorized.permit(credentials.ScopeWebhooksWrite); err != nil {
		return Endpoint{}, err
	}
	return authorized.service.Enable(ctx, authorized.principal.ApplicationID, id)
}

// Disable stops delivery to one of the caller's own endpoints.
func (authorized *Authorized) Disable(ctx context.Context, id string) (Endpoint, error) {
	if err := authorized.permit(credentials.ScopeWebhooksWrite); err != nil {
		return Endpoint{}, err
	}
	return authorized.service.Disable(ctx, authorized.principal.ApplicationID, id)
}

// Delete removes one of the caller's own endpoints.
func (authorized *Authorized) Delete(ctx context.Context, id string) error {
	if err := authorized.permit(credentials.ScopeWebhooksWrite); err != nil {
		return err
	}
	return authorized.service.Delete(ctx, authorized.principal.ApplicationID, id)
}

// GetDelivery returns one of the caller's own deliveries.
func (authorized *Authorized) GetDelivery(ctx context.Context, id string) (Delivery, error) {
	if err := authorized.permit(credentials.ScopeWebhooksRead); err != nil {
		return Delivery{}, err
	}
	return authorized.service.GetDelivery(ctx, authorized.principal.ApplicationID, id)
}

// ListDeliveries returns one page of what Convia tried to tell the caller.
func (authorized *Authorized) ListDeliveries(ctx context.Context, options DeliveryListOptions) (DeliveryPage, error) {
	if err := authorized.permit(credentials.ScopeWebhooksRead); err != nil {
		return DeliveryPage{}, err
	}
	return authorized.service.ListDeliveries(ctx, authorized.principal.ApplicationID, options)
}
