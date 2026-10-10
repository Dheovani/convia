package webhooks

import (
	"context"

	"convia/internal/operator"
)

// operatorService is what an operator reaches: reading a tenant's endpoints and
// deliveries, and sending a delivery again.
type operatorService interface {
	Get(ctx context.Context, applicationID, id string) (Endpoint, error)
	List(ctx context.Context, applicationID string, options ListOptions) (Page, error)
	GetDelivery(ctx context.Context, applicationID, id string) (Delivery, error)
	ListDeliveries(ctx context.Context, applicationID string, options DeliveryListOptions) (DeliveryPage, error)
	Redeliver(ctx context.Context, applicationID, id string) (Delivery, error)
}

/*
OperatorAuthorized is the webhooks service acting with the authority of one
verified operator, on a tenant named in the request.

This is `M21-007`, and what it deliberately leaves out is as much the point as
what it offers. An operator can see where a tenant asked to be told, what was
sent, and how each attempt went, and can send something again; an operator
cannot register, change, rotate, enable or disable a tenant's endpoint. Those
decide where a tenant's events go and who can verify them, and an operator
changing them would be redirecting somebody else's data.

The scopes are the tenant ones every other operator read and write already
uses, because an endpoint is a tenant's resource like its users and keys are.
Reading the signing key is not possible here or anywhere.
*/
type OperatorAuthorized struct {
	service   operatorService
	principal operator.Principal
}

// AuthorizeOperator binds the service to the authority of a verified operator.
func AuthorizeOperator(service operatorService, principal operator.Principal) *OperatorAuthorized {
	return &OperatorAuthorized{service: service, principal: principal}
}

// permit refuses an operation the caller was not granted.
func (authorized *OperatorAuthorized) permit(scope operator.Scope) error {
	if !authorized.principal.Allows(scope) {
		return ErrForbidden
	}
	return nil
}

// Get returns one of a tenant's endpoints, never its signing key.
func (authorized *OperatorAuthorized) Get(ctx context.Context, applicationID, id string) (Endpoint, error) {
	if err := authorized.permit(operator.ScopeTenantsRead); err != nil {
		return Endpoint{}, err
	}
	return authorized.service.Get(ctx, applicationID, id)
}

// List returns one page of a tenant's endpoints.
func (authorized *OperatorAuthorized) List(ctx context.Context, applicationID string, options ListOptions) (Page, error) {
	if err := authorized.permit(operator.ScopeTenantsRead); err != nil {
		return Page{}, err
	}
	return authorized.service.List(ctx, applicationID, options)
}

// GetDelivery returns one of a tenant's deliveries.
func (authorized *OperatorAuthorized) GetDelivery(ctx context.Context, applicationID, id string) (Delivery, error) {
	if err := authorized.permit(operator.ScopeTenantsRead); err != nil {
		return Delivery{}, err
	}
	return authorized.service.GetDelivery(ctx, applicationID, id)
}

// ListDeliveries returns one page of what Convia tried to tell a tenant.
func (authorized *OperatorAuthorized) ListDeliveries(ctx context.Context, applicationID string,
	options DeliveryListOptions) (DeliveryPage, error) {

	if err := authorized.permit(operator.ScopeTenantsRead); err != nil {
		return DeliveryPage{}, err
	}
	return authorized.service.ListDeliveries(ctx, applicationID, options)
}

/*
Redeliver sends one of a tenant's deliveries again.

It is a write because it makes Convia send a request to somewhere a tenant
chose, on that tenant's behalf: the same authority as managing their users and
keys, and the same scope.
*/
func (authorized *OperatorAuthorized) Redeliver(ctx context.Context, applicationID, id string) (Delivery, error) {
	if err := authorized.permit(operator.ScopeTenantsWrite); err != nil {
		return Delivery{}, err
	}
	return authorized.service.Redeliver(ctx, applicationID, id)
}
