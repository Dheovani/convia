package webhooks

import (
	"context"
	"fmt"
	"time"

	"convia/internal/audit"
	"convia/internal/events"
)

const (
	// defaultPageSize and maxPageSize implement the pagination bounds defined
	// in docs/api-conventions.md.
	defaultPageSize = 25
	maxPageSize     = 100
)

/*
tenants is the behavior this package needs from the tenancy domain.

It asks whether an application is being served rather than whether it exists,
so that a suspended tenant's endpoints stop being reachable through the API at
the same moment everything else of theirs does.
*/
type tenants interface {
	Active(ctx context.Context, applicationID string) (bool, error)
}

// Service applies Convia's rules for webhook endpoints.
type Service struct {
	store   *Store
	tenants tenants
	guard   Destinations
	trail   trail
}

// trail is the durable audit record this service writes to, in place of the
// logger that only ever wrote audit lines.
type trail interface {
	Record(ctx context.Context, written audit.Written) (audit.Entry, error)
}

func NewService(store *Store, owner tenants, guard Destinations, entries trail) *Service {
	return &Service{store: store, tenants: owner, guard: guard, trail: entries}
}

/*
Registration is what an application asks for when it wants to be told things.

There is no secret in it. The signing key is Convia's to generate, because a
key an application chose is one it may have chosen badly, reused from
somewhere else, or typed into a chat window.
*/
type Registration struct {
	Name       string
	URL        string
	EventTypes []events.Type
}

// ListOptions selects one page of an application's endpoints.
type ListOptions struct {
	Limit  int
	Cursor string
}

// Page is one page of endpoints and the token that continues it.
type Page struct {
	Endpoints  []Endpoint
	NextCursor string
}

// DeliveryListOptions selects one page of what Convia tried to send.
type DeliveryListOptions struct {
	Limit      int
	Cursor     string
	EndpointID string
}

// DeliveryPage is one page of deliveries and the token that continues it.
type DeliveryPage struct {
	Deliveries []Delivery
	NextCursor string
}

/*
Register records a destination and returns its signing key exactly once.

The key is shown here and never again, which is the same promise Convia makes
about an application key — and it is kept for a different reason. Convia does
hold this one, because it signs with it, so what "never again" means here is
that no read returns it and no representation carries it. Rotation exists for
when that is not enough.
*/
func (service *Service) Register(ctx context.Context, applicationID string,
	registration Registration) (Endpoint, Secret, error) {

	if err := service.requireApplication(ctx, applicationID); err != nil {
		return Endpoint{}, "", err
	}

	name, address, types, err := service.check(ctx, registration)
	if err != nil {
		return Endpoint{}, "", err
	}

	created := now()
	endpoint := Endpoint{
		ID:            NewID(),
		ApplicationID: applicationID,
		Name:          name,
		URL:           address,
		EventTypes:    types,
		Status:        StatusEnabled,
		CreatedAt:     created,
		UpdatedAt:     created,
	}

	signing := NewSecret()
	err = service.store.Atomically(ctx, func(ctx context.Context) error {
		if err := service.store.Create(ctx, endpoint, signing); err != nil {
			return err
		}
		return service.audit(ctx, "webhook_endpoint.registered", endpoint)
	})
	if err != nil {
		return Endpoint{}, "", err
	}

	return endpoint, signing, nil
}

// Get returns one of an application's endpoints.
func (service *Service) Get(ctx context.Context, applicationID, id string) (Endpoint, error) {
	if err := service.requireApplication(ctx, applicationID); err != nil {
		return Endpoint{}, err
	}
	if !ValidID(id) {
		return Endpoint{}, ErrNotFound
	}
	return service.store.Get(ctx, applicationID, id)
}

// List returns one page of an application's endpoints, newest first.
func (service *Service) List(ctx context.Context, applicationID string,
	options ListOptions) (Page, error) {

	if err := service.requireApplication(ctx, applicationID); err != nil {
		return Page{}, err
	}

	limit, err := pageSize(options.Limit)
	if err != nil {
		return Page{}, err
	}

	var cursor *Cursor
	if options.Cursor != "" {
		decoded, err := DecodeCursor(options.Cursor, ValidID)
		if err != nil {
			return Page{}, err
		}
		cursor = &decoded
	}

	page, hasMore, err := service.store.List(ctx, applicationID, cursor, limit)
	if err != nil {
		return Page{}, err
	}

	result := Page{Endpoints: page}
	if hasMore {
		last := page[len(page)-1]
		result.NextCursor = Cursor{CreatedAt: last.CreatedAt, ID: last.ID}.Encode()
	}
	return result, nil
}

// Update changes the destination, the name, or what an endpoint is told about.
func (service *Service) Update(ctx context.Context, applicationID, id string,
	registration Registration) (Endpoint, error) {

	if err := service.requireApplication(ctx, applicationID); err != nil {
		return Endpoint{}, err
	}
	if !ValidID(id) {
		return Endpoint{}, ErrNotFound
	}

	name, address, types, err := service.check(ctx, registration)
	if err != nil {
		return Endpoint{}, err
	}

	var updated Endpoint
	err = service.store.Atomically(ctx, func(ctx context.Context) error {
		updated, err = service.store.Update(ctx, applicationID, id, name, address, types, now())
		if err != nil {
			return err
		}
		return service.audit(ctx, "webhook_endpoint.updated", updated)
	})
	if err != nil {
		return Endpoint{}, err
	}

	return updated, nil
}

/*
Rotate issues a new signing key and returns it exactly once.

The old key stops working immediately, which is the point: rotation exists
because a key may have been exposed, and one that kept working for a grace
period would keep working for whoever exposed it. A consumer therefore accepts
both keys while it deploys the new one, and Convia's part is to make the change
the moment it is asked for.
*/
func (service *Service) Rotate(ctx context.Context, applicationID, id string) (Endpoint, Secret, error) {
	if err := service.requireApplication(ctx, applicationID); err != nil {
		return Endpoint{}, "", err
	}
	if !ValidID(id) {
		return Endpoint{}, "", ErrNotFound
	}

	signing := NewSecret()
	var endpoint Endpoint
	err := service.store.Atomically(ctx, func(ctx context.Context) error {
		var err error
		endpoint, err = service.store.Rotate(ctx, applicationID, id, signing, now())
		if err != nil {
			return err
		}
		return service.audit(ctx, "webhook_endpoint.rotated", endpoint)
	})
	if err != nil {
		return Endpoint{}, "", err
	}

	return endpoint, signing, nil
}

/*
Enable resumes delivery to an endpoint.

It is how an application recovers from Convia having disabled a destination
that stopped working: fix the receiver, then say so. The failure count resets,
so a fixed endpoint starts from zero rather than one failure away from being
disabled again.
*/
func (service *Service) Enable(ctx context.Context, applicationID, id string) (Endpoint, error) {
	return service.setStatus(ctx, applicationID, id, StatusEnabled, "",
		"webhook_endpoint.enabled")
}

/*
Disable stops delivery without deleting the endpoint or its history.

An application does this when a receiver is being replaced or is known to be
down, and it is kinder than deleting: the deliveries stay readable, and the
destination comes back with the same identifier.
*/
func (service *Service) Disable(ctx context.Context, applicationID, id string) (Endpoint, error) {
	return service.setStatus(ctx, applicationID, id, StatusDisabled,
		"The application disabled this endpoint.", "webhook_endpoint.disabled")
}

func (service *Service) setStatus(ctx context.Context, applicationID, id string,
	status Status, reason, event string) (Endpoint, error) {

	if err := service.requireApplication(ctx, applicationID); err != nil {
		return Endpoint{}, err
	}
	if !ValidID(id) {
		return Endpoint{}, ErrNotFound
	}

	var endpoint Endpoint
	err := service.store.Atomically(ctx, func(ctx context.Context) error {
		var err error
		endpoint, err = service.store.SetStatus(ctx, applicationID, id, status, reason, now())
		if err != nil {
			return err
		}
		return service.audit(ctx, event, endpoint)
	})
	if err != nil {
		return Endpoint{}, err
	}

	return endpoint, nil
}

/*
Delete removes an endpoint and everything Convia queued for it.

The deliveries go too, by the schema's cascade, and that is the deliberate
reading of what deleting means: an application that wants the history keeps the
endpoint and disables it instead.
*/
func (service *Service) Delete(ctx context.Context, applicationID, id string) error {
	if err := service.requireApplication(ctx, applicationID); err != nil {
		return err
	}
	if !ValidID(id) {
		return ErrNotFound
	}

	return service.store.Atomically(ctx, func(ctx context.Context) error {
		if err := service.store.Delete(ctx, applicationID, id); err != nil {
			return err
		}
		return service.audit(ctx, "webhook_endpoint.deleted", Endpoint{ID: id, ApplicationID: applicationID})
	})
}

// GetDelivery returns one of an application's deliveries.
func (service *Service) GetDelivery(ctx context.Context, applicationID, id string) (Delivery, error) {
	if err := service.requireApplication(ctx, applicationID); err != nil {
		return Delivery{}, err
	}
	if !ValidDeliveryID(id) {
		return Delivery{}, ErrDeliveryNotFound
	}
	return service.store.GetDelivery(ctx, applicationID, id)
}

// ListDeliveries returns one page of what Convia tried to tell an application.
func (service *Service) ListDeliveries(ctx context.Context, applicationID string,
	options DeliveryListOptions) (DeliveryPage, error) {

	if err := service.requireApplication(ctx, applicationID); err != nil {
		return DeliveryPage{}, err
	}

	limit, err := pageSize(options.Limit)
	if err != nil {
		return DeliveryPage{}, err
	}

	if options.EndpointID != "" {
		if !ValidID(options.EndpointID) {
			return DeliveryPage{}, ErrNotFound
		}
		if _, err := service.store.Get(ctx, applicationID, options.EndpointID); err != nil {
			return DeliveryPage{}, err
		}
	}

	var cursor *Cursor
	if options.Cursor != "" {
		decoded, err := DecodeCursor(options.Cursor, ValidDeliveryID)
		if err != nil {
			return DeliveryPage{}, err
		}
		cursor = &decoded
	}

	page, hasMore, err := service.store.ListDeliveries(ctx, applicationID,
		options.EndpointID, cursor, limit)
	if err != nil {
		return DeliveryPage{}, err
	}

	result := DeliveryPage{Deliveries: page}
	if hasMore {
		last := page[len(page)-1]
		result.NextCursor = Cursor{CreatedAt: last.CreatedAt, ID: last.ID}.Encode()
	}
	return result, nil
}

/*
check validates a registration, shape first and destination second.

The order matters for what a caller is told: a malformed URL is a mistake in
the request, and a well-formed one pointing somewhere Convia will not go is a
different answer that should not be reached by anything that failed the first
test.
*/
func (service *Service) check(ctx context.Context, registration Registration) (
	name, address string, types []events.Type, err error) {

	if name, err = NormalizeName(registration.Name); err != nil {
		return "", "", nil, err
	}
	if address, err = NormalizeURL(registration.URL); err != nil {
		return "", "", nil, err
	}
	if types, err = NormalizeEventTypes(registration.EventTypes); err != nil {
		return "", "", nil, err
	}

	if err := service.guard.Permits(address); err != nil {
		return "", "", nil, err
	}
	if err := service.guard.Resolves(ctx, address); err != nil {
		return "", "", nil, err
	}
	return name, address, types, nil
}

/*
requireApplication refuses work for an application Convia does not serve.

A suspended tenant's endpoints are unreachable through the API for the same
reason its rooms are. Delivery stops too, because the events that would feed it
stop being produced.
*/
func (service *Service) requireApplication(ctx context.Context, applicationID string) error {
	active, err := service.tenants.Active(ctx, applicationID)
	if err != nil {
		return fmt.Errorf("check application: %w", err)
	}
	if !active {
		return ErrApplicationNotFound
	}
	return nil
}

/*
pageSize validates a requested page size.

An oversized limit is rejected rather than silently clamped, so that a client
never believes it received every result it asked for.
*/
func pageSize(requested int) (int, error) {
	switch {
	case requested == 0:
		return defaultPageSize, nil
	case requested < 0 || requested > maxPageSize:
		return 0, ValidationError{
			Field:   "limit",
			Message: fmt.Sprintf("The limit must be between 1 and %d.", maxPageSize),
		}
	default:
		return requested, nil
	}
}

/*
audit records a change to where Convia sends things.

**The destination is not recorded**, which the log line this replaced did. A
webhook address commonly carries a token in its query string, because that is
the easiest way for a receiver to know a request came from Convia, and the
trail is durable and readable by an operator over every tenant. The endpoint's
identifier leads to the address for as long as the endpoint exists, through
routes that already decide who may read it. The signing key is never here.
*/
func (service *Service) audit(ctx context.Context, action string, endpoint Endpoint) error {
	_, err := service.trail.Record(ctx, audit.Written{
		Action:        action,
		Subject:       audit.Subject{Kind: "webhook_endpoint", ID: endpoint.ID},
		ApplicationID: endpoint.ApplicationID,
	})
	return err
}

// now returns the timestamp Convia stores for a change.
func now() time.Time {
	return time.Now().UTC().Truncate(time.Microsecond)
}
