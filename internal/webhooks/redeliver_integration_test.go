package webhooks

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"convia/internal/audit"
	"convia/internal/events"
)

// operating is a request made by one operator, saying why.
func operating(reason string) context.Context {
	ctx := audit.ContextWithActor(context.Background(), audit.Actor{Kind: audit.KindOperator, ID: "oper_7KQZP4XN2VJH6TBWMDR3YAFC5E"})
	return audit.ContextWithReason(ctx, reason)
}

// refused sends one event to a destination that refuses it, so the delivery
// ends failed after its single attempt.
func (setup fixture) refused(t *testing.T, consumer *receiver, address string) (Endpoint, Delivery) {
	t.Helper()

	endpoint, signing := setup.register(t, setup.first, address)
	consumer.secret = signing
	consumer.status = http.StatusBadRequest

	setup.announce(t, setup.first, events.CallStarted)
	setup.dispatcher.pass(context.Background())

	recorded := setup.deliveries(t, setup.first)
	if len(recorded) != 1 || recorded[0].Status != DeliveryFailed {
		t.Fatalf("deliveries = %+v, want one that failed", recorded)
	}
	return endpoint, recorded[0]
}

/*
TestAMissedEventCanBeSentAgain is `M15-010` end to end: a receiver that was
broken for a while is sent what it missed, by an operator who said why, and it
can tell the event is the one it already knows about.
*/
func TestAMissedEventCanBeSentAgain(t *testing.T) {
	setup := newFixture(t)
	consumer, server := listening(t, http.StatusOK)
	_, original := setup.refused(t, consumer, server.URL)

	consumer.mutex.Lock()
	consumer.status = http.StatusOK
	consumer.mutex.Unlock()

	redelivered, err := setup.service.Redeliver(operating("receiver was down 14:00-15:00, ticket 812"),
		setup.first, original.ID)
	if err != nil {
		t.Fatalf("Redeliver() error = %v", err)
	}
	if redelivered.ID == original.ID || redelivered.RedeliveryOf != original.ID || redelivered.Status != DeliveryPending {
		t.Fatalf("redelivered %+v, want a new pending delivery naming %s", redelivered, original.ID)
	}

	setup.dispatcher.pass(context.Background())

	received := consumer.deliveries()
	if len(received) != 2 {
		t.Fatalf("the consumer received %d deliveries, want the refused one and the one sent again", len(received))
	}
	again := received[1]
	if !again.Verified || again.DeliveryID != redelivered.ID {
		t.Errorf("the second delivery was %+v, want a verified one named %s", again, redelivered.ID)
	}
	if string(again.Body) != string(received[0].Body) {
		t.Errorf("the event was sent with different bytes:\n%s\n%s", received[0].Body, again.Body)
	}
	if !strings.Contains(string(again.Body), original.EventID) {
		t.Error("the redelivery does not carry the event's identifier, so a consumer cannot tell it is the same event")
	}

	stillThere, err := setup.service.GetDelivery(context.Background(), setup.first, original.ID)
	if err != nil || stillThere.Status != DeliveryFailed {
		t.Errorf("the original became %+v (%v); it is the record of what happened and must stay failed", stillThere, err)
	}

	var actor, reason, details string
	if err := setup.pool.QueryRow(context.Background(), `
		SELECT actor_kind || ':' || actor_id, reason, details::text FROM audit_entries
		WHERE action = 'webhook_delivery.redelivered' AND subject_id = $1`, original.ID).Scan(&actor, &reason, &details); err != nil {
		t.Fatalf("read the trail: %v", err)
	}
	if actor != "operator:oper_7KQZP4XN2VJH6TBWMDR3YAFC5E" || reason != "receiver was down 14:00-15:00, ticket 812" {
		t.Errorf("the trail says %s did it because %q", actor, reason)
	}
	if !strings.Contains(details, redelivered.ID) {
		t.Errorf("the trail does not lead to the new delivery: %s", details)
	}
}

/*
TestAnEventIsNotSentAgainWhileItIsOnItsWay keeps a redelivery from turning
into two: neither the one still pending nor the original it repeats can be
redelivered until the attempt finishes.
*/
func TestAnEventIsNotSentAgainWhileItIsOnItsWay(t *testing.T) {
	setup := newFixture(t)
	consumer, server := listening(t, http.StatusOK)
	_, original := setup.refused(t, consumer, server.URL)

	redelivered, err := setup.service.Redeliver(operating("first try"), setup.first, original.ID)
	if err != nil {
		t.Fatalf("Redeliver() error = %v", err)
	}

	for name, id := range map[string]string{"the original": original.ID, "the pending one": redelivered.ID} {
		if _, err := setup.service.Redeliver(operating("again"), setup.first, id); !errors.Is(err, ErrDeliveryOutstanding) {
			t.Errorf("redelivering %s while one is on its way: error = %v, want ErrDeliveryOutstanding", name, err)
		}
	}
	if recorded := setup.deliveries(t, setup.first); len(recorded) != 2 {
		t.Errorf("%d deliveries recorded, want the original and one redelivery", len(recorded))
	}
}

/*
TestADisabledDestinationIsNotSentAgain refuses what would otherwise sit in the
queue for an endpoint the worker does not attempt.
*/
func TestADisabledDestinationIsNotSentAgain(t *testing.T) {
	setup := newFixture(t)
	consumer, server := listening(t, http.StatusOK)
	endpoint, original := setup.refused(t, consumer, server.URL)

	if _, err := setup.service.Disable(context.Background(), setup.first, endpoint.ID); err != nil {
		t.Fatalf("Disable() error = %v", err)
	}

	if _, err := setup.service.Redeliver(operating("try anyway"), setup.first, original.ID); !errors.Is(err, ErrEndpointDisabled) {
		t.Errorf("error = %v, want ErrEndpointDisabled", err)
	}

	var entries int
	if err := setup.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM audit_entries WHERE action = 'webhook_delivery.redelivered'`).Scan(&entries); err != nil {
		t.Fatalf("read the trail: %v", err)
	}
	if entries != 0 {
		t.Errorf("a refused redelivery left %d entries in the trail", entries)
	}
}

// TestAnotherTenantsDeliveryIsNotThere holds redelivery to the tenant in the path.
func TestAnotherTenantsDeliveryIsNotThere(t *testing.T) {
	setup := newFixture(t)
	consumer, server := listening(t, http.StatusOK)
	_, original := setup.refused(t, consumer, server.URL)

	if _, err := setup.service.Redeliver(operating("wrong tenant"), setup.second, original.ID); !errors.Is(err, ErrDeliveryNotFound) {
		t.Errorf("error = %v, want ErrDeliveryNotFound", err)
	}
}

/*
TestTheStoreHoldsRedeliveryToItsTenant is the same rule one layer down. The
service reads the delivery for the tenant first, so this is what keeps the
insert honest if that read is ever skipped: every query here names its tenant.
*/
func TestTheStoreHoldsRedeliveryToItsTenant(t *testing.T) {
	setup := newFixture(t)
	consumer, server := listening(t, http.StatusOK)
	_, original := setup.refused(t, consumer, server.URL)

	if _, err := setup.store.Redeliver(context.Background(), setup.second, original.ID, NewDeliveryID(), now()); err == nil {
		t.Error("the store queued another tenant's delivery again")
	}
	if recorded := setup.deliveries(t, setup.first); len(recorded) != 1 {
		t.Errorf("%d deliveries recorded, want only the original", len(recorded))
	}
}
