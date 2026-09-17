package webhooks

import (
	"context"
	"errors"
	"testing"

	"convia/internal/events"
	"convia/internal/transaction"
)

/*
TestADeliveryIsOwedOnlyWhenItsChangeCommits is M15-016: the delivery is queued
in the transaction that made the change, so a change that is undone owes nobody
anything, and one that commits cannot lose its delivery on the way out.
*/
func TestADeliveryIsOwedOnlyWhenItsChangeCommits(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()
	setup.register(t, setup.first, "http://127.0.0.1:9/hooks")

	undone := errors.New("the change failed")
	err := transaction.Run(ctx, setup.pool, func(ctx context.Context) error {
		event := events.New(events.CallStarted, setup.first, "call_1", "", events.Data{"room_id": "room_1"})
		if err := setup.dispatcher.Enqueue(ctx, event); err != nil {
			return err
		}
		return undone
	})
	if !errors.Is(err, undone) {
		t.Fatalf("Run() error = %v", err)
	}
	if queued := setup.deliveries(t, setup.first); len(queued) != 0 {
		t.Fatalf("a change that was undone left %d deliveries", len(queued))
	}

	committed := events.New(events.CallStarted, setup.first, "call_2", "", events.Data{"room_id": "room_1"})
	if err := transaction.Run(ctx, setup.pool, func(ctx context.Context) error {
		return setup.dispatcher.Enqueue(ctx, committed)
	}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	queued := setup.deliveries(t, setup.first)
	if len(queued) != 1 || queued[0].EventID != committed.ID {
		t.Errorf("deliveries = %+v, want one for %s", queued, committed.ID)
	}
}
