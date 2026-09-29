package calls

import (
	"context"
	"testing"
)

/*
TestOnlyLiveCallsAreCounted.

The gauge this feeds is read as "what is this installation carrying". A count
that included ended calls would climb for ever and never come down, which is not
a saturation signal — it is a row count wearing one's clothes, and it would look
healthy right up until it looked alarming for no reason.
*/
func TestOnlyLiveCallsAreCounted(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()
	store := NewStore(setup.pool)

	if counted, err := store.CountActive(ctx); err != nil || counted != 0 {
		t.Fatalf("CountActive() on an empty installation = %d, %v, want 0", counted, err)
	}

	first := setup.start(t, setup.first, setup.firstRoom)
	setup.start(t, setup.second, setup.secondRoom)

	counted, err := store.CountActive(ctx)
	if err != nil {
		t.Fatalf("CountActive() error = %v", err)
	}
	if counted != 2 {
		t.Fatalf("CountActive() = %d with two calls running, want 2", counted)
	}

	if _, err := setup.service.End(ctx, setup.first, first.ID, ActorApplication, ""); err != nil {
		t.Fatalf("End() error = %v", err)
	}

	if counted, err = store.CountActive(ctx); err != nil || counted != 1 {
		t.Errorf("CountActive() = %d, %v after one ended, want 1", counted, err)
	}
}

/*
TestTheCountCrossesApplications, deliberately.

The number means "what is this installation carrying", which is a property of
the installation rather than of any tenant on it. Cutting it by application
would make a series per tenant that is almost always zero and whose sum is the
only figure anybody reads — and `M22-007` is about keeping labels off these.
*/
func TestTheCountCrossesApplications(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	setup.start(t, setup.first, setup.firstRoom)
	setup.start(t, setup.second, setup.secondRoom)

	counted, err := NewStore(setup.pool).CountActive(ctx)
	if err != nil {
		t.Fatalf("CountActive() error = %v", err)
	}
	if counted != 2 {
		t.Errorf("CountActive() = %d across two applications, want 2 — it is scoped to a tenant", counted)
	}
}
