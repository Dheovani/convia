package webhooks

import (
	"context"
	"errors"
	"testing"

	"convia/internal/operator"
)

// counting is an operator service that only remembers whether it was reached.
type counting struct{ reached int }

func (service *counting) Get(context.Context, string, string) (Endpoint, error) {
	service.reached++
	return Endpoint{}, nil
}

func (service *counting) List(context.Context, string, ListOptions) (Page, error) {
	service.reached++
	return Page{}, nil
}

func (service *counting) GetDelivery(context.Context, string, string) (Delivery, error) {
	service.reached++
	return Delivery{}, nil
}

func (service *counting) ListDeliveries(context.Context, string, DeliveryListOptions) (DeliveryPage, error) {
	service.reached++
	return DeliveryPage{}, nil
}

func (service *counting) Redeliver(context.Context, string, string) (Delivery, error) {
	service.reached++
	return Delivery{}, nil
}

/*
TestReadingIsNotSendingAgain keeps redelivery a write: an operator who may look
at a tenant's deliveries may not make Convia send anything on the tenant's
behalf, and is refused before the service is asked.
*/
func TestReadingIsNotSendingAgain(t *testing.T) {
	service := &counting{}
	reader := AuthorizeOperator(service, operator.Principal{CredentialID: "oper_X",
		Scopes: []operator.Scope{operator.ScopeTenantsRead}})
	ctx := context.Background()

	if _, err := reader.ListDeliveries(ctx, "app_X", DeliveryListOptions{}); err != nil {
		t.Fatalf("ListDeliveries() with tenants:read error = %v", err)
	}
	if _, err := reader.Redeliver(ctx, "app_X", "whd_X"); !errors.Is(err, ErrForbidden) {
		t.Errorf("Redeliver() with tenants:read error = %v, want ErrForbidden", err)
	}
	if service.reached != 1 {
		t.Errorf("the service was reached %d times, want only the read", service.reached)
	}

	nobody := AuthorizeOperator(service, operator.Principal{CredentialID: "oper_Y",
		Scopes: []operator.Scope{operator.ScopeAuditRead}})
	for name, attempt := range map[string]func() error{
		"Get":            func() error { _, err := nobody.Get(ctx, "app_X", "whk_X"); return err },
		"List":           func() error { _, err := nobody.List(ctx, "app_X", ListOptions{}); return err },
		"GetDelivery":    func() error { _, err := nobody.GetDelivery(ctx, "app_X", "whd_X"); return err },
		"ListDeliveries": func() error { _, err := nobody.ListDeliveries(ctx, "app_X", DeliveryListOptions{}); return err },
	} {
		if err := attempt(); !errors.Is(err, ErrForbidden) {
			t.Errorf("%s without tenants:read error = %v, want ErrForbidden", name, err)
		}
	}
}
