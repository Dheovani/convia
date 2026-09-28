package participants

import (
	"context"
	"testing"
)

/*
TestTheCallsOnePersonWasInAreReadableAcrossCalls.

[Store.List] answers "who was in this call", which is a roster. This is the same
table read the other way — "which calls was this person in" — and it exists for
export, where returning one participation too many means telling somebody which
calls a different person joined.
*/
func TestTheCallsOnePersonWasInAreReadableAcrossCalls(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	standup := setup.newCall(t, setup.first, nil)
	retro := setup.newCall(t, setup.first, nil)

	ana := setup.newUser(t, setup.first, "ana")
	bruno := setup.newUser(t, setup.first, "bruno")

	mine := map[string]bool{}
	mine[setup.join(t, setup.first, standup.ID, ana, RoleMember).ID] = true
	mine[setup.join(t, setup.first, retro.ID, ana, RoleMember).ID] = true
	setup.join(t, setup.first, standup.ID, bruno, RoleMember)

	store := NewStore(setup.pool)

	page, more, err := store.OfUser(ctx, setup.first, ana, nil, 100)
	if err != nil {
		t.Fatalf("OfUser() error = %v", err)
	}
	if more {
		t.Error("OfUser() reported more than a hundred participations for two")
	}
	if len(page) != len(mine) {
		t.Fatalf("read %d participations, want %d", len(page), len(mine))
	}

	calls := map[string]bool{}
	for _, participant := range page {
		if !mine[participant.ID] {
			t.Errorf("read %q, which is not this person's participation", participant.ID)
		}
		if participant.UserID != ana {
			t.Errorf("read a participation belonging to %q", participant.UserID)
		}
		calls[participant.CallID] = true
	}

	if len(calls) != 2 {
		t.Errorf("read participations from %d calls, want both", len(calls))
	}
}

/*
TestAWalkThroughSomebodysCallsReachesTheEnd, one page at a time.

A page size of one is what catches a cursor comparison that is subtly wrong: an
off-by-one either repeats a row for ever or skips every second one, and neither
is visible at a page size that fits everything.
*/
func TestAWalkThroughSomebodysCallsReachesTheEnd(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	ana := setup.newUser(t, setup.first, "ana")

	const joined = 4
	for range joined {
		call := setup.newCall(t, setup.first, nil)
		setup.join(t, setup.first, call.ID, ana, RoleMember)
	}

	store := NewStore(setup.pool)
	seen := map[string]bool{}
	var cursor *Cursor

	for round := 0; round <= joined+2; round++ {
		page, more, err := store.OfUser(ctx, setup.first, ana, cursor, 1)
		if err != nil {
			t.Fatalf("OfUser() error = %v", err)
		}

		for _, participant := range page {
			if seen[participant.ID] {
				t.Fatalf("%q was read twice, so the cursor does not move", participant.ID)
			}
			seen[participant.ID] = true
			cursor = &Cursor{CreatedAt: participant.CreatedAt, ID: participant.ID}
		}

		if !more {
			break
		}
	}

	if len(seen) != joined {
		t.Errorf("walked %d participations one page at a time, want %d", len(seen), joined)
	}
}

// TestAnotherTenantReadsNoParticipations: the application is part of the lookup
// here as it is everywhere else.
func TestAnotherTenantReadsNoParticipations(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	call := setup.newCall(t, setup.first, nil)
	ana := setup.newUser(t, setup.first, "ana")
	setup.join(t, setup.first, call.ID, ana, RoleMember)

	page, _, err := NewStore(setup.pool).OfUser(ctx, setup.second, ana, nil, 100)
	if err != nil {
		t.Fatalf("OfUser() error = %v", err)
	}
	if len(page) != 0 {
		t.Errorf("another tenant read %d of this application's participations", len(page))
	}
}
