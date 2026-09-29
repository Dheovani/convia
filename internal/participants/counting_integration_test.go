package participants

import (
	"context"
	"testing"
)

/*
TestOnlyPeopleStillInACallAreCounted.

Joined counts; left and removed do not. A count over every row would climb for
ever and never come down, which is a row count rather than a saturation signal —
and the two are indistinguishable on a graph until somebody acts on the wrong
one.
*/
func TestOnlyPeopleStillInACallAreCounted(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()
	store := NewStore(setup.pool)

	if counted, err := store.CountPresent(ctx); err != nil || counted != 0 {
		t.Fatalf("CountPresent() on an empty installation = %d, %v, want 0", counted, err)
	}

	call := setup.newCall(t, setup.first, nil)
	ana := setup.join(t, setup.first, call.ID, setup.newUser(t, setup.first, "ana"), RoleMember)
	setup.join(t, setup.first, call.ID, setup.newUser(t, setup.first, "bruno"), RoleMember)

	counted, err := store.CountPresent(ctx)
	if err != nil {
		t.Fatalf("CountPresent() error = %v", err)
	}
	if counted != 2 {
		t.Fatalf("CountPresent() = %d with two people in a call, want 2", counted)
	}

	if _, _, err := store.Leave(ctx, setup.first, ana.ID, ana.CreatedAt); err != nil {
		t.Fatalf("Leave() error = %v", err)
	}

	if counted, err = store.CountPresent(ctx); err != nil || counted != 1 {
		t.Errorf("CountPresent() = %d, %v after one left, want 1", counted, err)
	}
}

/*
TestTheCountCrossesCallsAndApplications.

What this measures is load, and load does not belong to a call or to a tenant.
It is also where `M22-007`'s rule shows: there is nothing in the answer that
says which call anybody was in.
*/
func TestTheCountCrossesCallsAndApplications(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	here := setup.newCall(t, setup.first, nil)
	elsewhere := setup.newCall(t, setup.second, nil)

	setup.join(t, setup.first, here.ID, setup.newUser(t, setup.first, "ana"), RoleMember)
	setup.join(t, setup.second, elsewhere.ID, setup.newUser(t, setup.second, "bruno"), RoleMember)

	counted, err := NewStore(setup.pool).CountPresent(ctx)
	if err != nil {
		t.Fatalf("CountPresent() error = %v", err)
	}
	if counted != 2 {
		t.Errorf("CountPresent() = %d across two calls in two applications, want 2", counted)
	}
}
