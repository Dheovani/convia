package webhooks

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"convia/internal/credentials"
)

// scriptedService answers every operation, recording which tenant it was asked
// about so that a test can prove the caller never chooses.
type scriptedService struct {
	askedAbout []string
	endpoint   Endpoint
	delivery   Delivery
}

func (stub *scriptedService) record(applicationID string) {
	stub.askedAbout = append(stub.askedAbout, applicationID)
}

func (stub *scriptedService) Register(_ context.Context, applicationID string, _ Registration) (Endpoint, Secret, error) {
	stub.record(applicationID)
	return stub.endpoint, Secret("whsec_scripted"), nil
}

func (stub *scriptedService) Get(_ context.Context, applicationID, _ string) (Endpoint, error) {
	stub.record(applicationID)
	return stub.endpoint, nil
}

func (stub *scriptedService) List(_ context.Context, applicationID string, _ ListOptions) (Page, error) {
	stub.record(applicationID)
	return Page{}, nil
}

func (stub *scriptedService) Update(_ context.Context, applicationID, _ string, _ Registration) (Endpoint, error) {
	stub.record(applicationID)
	return stub.endpoint, nil
}

func (stub *scriptedService) Rotate(_ context.Context, applicationID, _ string) (Endpoint, Secret, error) {
	stub.record(applicationID)
	return stub.endpoint, Secret("whsec_scripted"), nil
}

func (stub *scriptedService) Enable(_ context.Context, applicationID, _ string) (Endpoint, error) {
	stub.record(applicationID)
	return stub.endpoint, nil
}

func (stub *scriptedService) Disable(_ context.Context, applicationID, _ string) (Endpoint, error) {
	stub.record(applicationID)
	return stub.endpoint, nil
}

func (stub *scriptedService) Delete(_ context.Context, applicationID, _ string) error {
	stub.record(applicationID)
	return nil
}

func (stub *scriptedService) GetDelivery(_ context.Context, applicationID, _ string) (Delivery, error) {
	stub.record(applicationID)
	return stub.delivery, nil
}

func (stub *scriptedService) ListDeliveries(_ context.Context, applicationID string, _ DeliveryListOptions) (DeliveryPage, error) {
	stub.record(applicationID)
	return DeliveryPage{}, nil
}

// holding builds a principal carrying exactly the named scopes.
func holding(scopes ...credentials.Scope) credentials.Principal {
	return credentials.Principal{
		ApplicationID: "app_1",
		CredentialID:  "cred_1",
		Scopes:        scopes,
	}
}

/*
TestEveryOperationRequiresItsScope is the authorization boundary, checked one
operation at a time.

The split matters most for the two operations that produce a secret. A key
granted to read a list of destinations must not be able to register a new one
or rotate an existing key, because either would turn a read-only integration
into one that can redirect an application's events or invalidate the signature
its receiver checks.
*/
func TestEveryOperationRequiresItsScope(t *testing.T) {
	operations := map[string]struct {
		required credentials.Scope
		call     func(*Authorized) error
	}{
		"register": {credentials.ScopeWebhooksWrite, func(authorized *Authorized) error {
			_, _, err := authorized.Register(context.Background(), Registration{})
			return err
		}},
		"rotate": {credentials.ScopeWebhooksWrite, func(authorized *Authorized) error {
			_, _, err := authorized.Rotate(context.Background(), "whk_1")
			return err
		}},
		"update": {credentials.ScopeWebhooksWrite, func(authorized *Authorized) error {
			_, err := authorized.Update(context.Background(), "whk_1", Registration{})
			return err
		}},
		"enable": {credentials.ScopeWebhooksWrite, func(authorized *Authorized) error {
			_, err := authorized.Enable(context.Background(), "whk_1")
			return err
		}},
		"disable": {credentials.ScopeWebhooksWrite, func(authorized *Authorized) error {
			_, err := authorized.Disable(context.Background(), "whk_1")
			return err
		}},
		"delete": {credentials.ScopeWebhooksWrite, func(authorized *Authorized) error {
			return authorized.Delete(context.Background(), "whk_1")
		}},
		"get": {credentials.ScopeWebhooksRead, func(authorized *Authorized) error {
			_, err := authorized.Get(context.Background(), "whk_1")
			return err
		}},
		"list": {credentials.ScopeWebhooksRead, func(authorized *Authorized) error {
			_, err := authorized.List(context.Background(), ListOptions{})
			return err
		}},
		"get a delivery": {credentials.ScopeWebhooksRead, func(authorized *Authorized) error {
			_, err := authorized.GetDelivery(context.Background(), "whd_1")
			return err
		}},
		"list deliveries": {credentials.ScopeWebhooksRead, func(authorized *Authorized) error {
			_, err := authorized.ListDeliveries(context.Background(), DeliveryListOptions{})
			return err
		}},
	}

	other := map[credentials.Scope]credentials.Scope{
		credentials.ScopeWebhooksWrite: credentials.ScopeWebhooksRead,
		credentials.ScopeWebhooksRead:  credentials.ScopeWebhooksWrite,
	}

	for name, operation := range operations {
		t.Run(name+" with its scope", func(t *testing.T) {
			if err := operation.call(Authorize(&scriptedService{}, holding(operation.required))); err != nil {
				t.Errorf("a permitted call failed: %v", err)
			}
		})

		t.Run(name+" with the other webhook scope", func(t *testing.T) {
			err := operation.call(Authorize(&scriptedService{}, holding(other[operation.required])))
			if !errors.Is(err, ErrForbidden) {
				t.Errorf("error = %v, want %v", err, ErrForbidden)
			}
		})

		t.Run(name+" with no scopes", func(t *testing.T) {
			if err := operation.call(Authorize(&scriptedService{}, holding())); !errors.Is(err, ErrForbidden) {
				t.Errorf("error = %v, want %v", err, ErrForbidden)
			}
		})
	}
}

/*
TestTheStreamScopeDoesNotReachWebhooks keeps two different powers apart.

Holding a connection open and asking Convia to make requests to an address of
your choosing are not the same grant, and only the second turns Convia into a
client of somewhere else. `M15-011` is about what that address may be; this is
about who may choose one at all.
*/
func TestTheStreamScopeDoesNotReachWebhooks(t *testing.T) {
	authorized := Authorize(&scriptedService{}, holding(credentials.ScopeEventsRead))

	if _, _, err := authorized.Register(context.Background(), Registration{}); !errors.Is(err, ErrForbidden) {
		t.Errorf("a stream scope registered a destination: %v", err)
	}
	if _, err := authorized.List(context.Background(), ListOptions{}); !errors.Is(err, ErrForbidden) {
		t.Errorf("a stream scope read the destinations: %v", err)
	}
}

/*
TestTheTenantComesFromTheCredential pins the thing no request field may
influence.

Every method below takes an identifier from the caller and none takes an
application, so the only tenant that can be acted on is the one the key proved.
*/
func TestTheTenantComesFromTheCredential(t *testing.T) {
	stub := &scriptedService{}
	authorized := Authorize(stub, holding(credentials.ScopeWebhooksRead, credentials.ScopeWebhooksWrite))

	if _, _, err := authorized.Register(context.Background(), Registration{}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if _, err := authorized.Get(context.Background(), "whk_1"); err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if _, err := authorized.ListDeliveries(context.Background(), DeliveryListOptions{}); err != nil {
		t.Fatalf("ListDeliveries() error = %v", err)
	}

	for _, asked := range stub.askedAbout {
		if asked != "app_1" {
			t.Errorf("the service was asked about %q rather than the caller's own tenant", asked)
		}
	}
	if len(stub.askedAbout) != 3 {
		t.Errorf("the service was reached %d times, want 3", len(stub.askedAbout))
	}
}

/*
TestQueuingIsUnreachableFromTheTenantSurface is the same guarantee guest
admission has, for the same reason.

Queuing a delivery is Convia's own act on the way out of a domain operation,
never something an application asks for. It is absent from the interface the
authorization wrapper and the handler consume, so there is no route and no
scope that reaches it — and an application therefore cannot make Convia send a
body it composed to an address it chose.
*/
func TestQueuingIsUnreachableFromTheTenantSurface(t *testing.T) {
	surface := reflect.TypeOf((*service)(nil)).Elem()

	for index := range surface.NumMethod() {
		if name := surface.Method(index).Name; name == "Enqueue" {
			t.Error("queuing a delivery is reachable from the application-facing surface")
		}
	}

	authorized := reflect.TypeOf(&Authorized{})
	for index := range authorized.NumMethod() {
		if name := authorized.Method(index).Name; name == "Enqueue" {
			t.Error("queuing a delivery is reachable through the authorization wrapper")
		}
	}
}
