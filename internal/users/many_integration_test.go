package users

import (
	"context"
	"testing"
)

/*
TestManyReadsOnlyLivePeopleOfOneApplication covers the batch read the
person-facing lists are built on.

What it leaves out matters as much as what it returns: somebody deleted, somebody
belonging to another application, and an identifier that names nobody are all
absent rather than errors, because a caller building a list has no use for an
error about one row of it — and a cross-tenant identifier must produce nothing
at all.
*/
func TestManyReadsOnlyLivePeopleOfOneApplication(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	ana, _, err := setup.service.Resolve(ctx, setup.first, Identity{ExternalSubject: "ana"})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	bruno, _, err := setup.service.Resolve(ctx, setup.first, Identity{ExternalSubject: "bruno"})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if err := setup.service.Delete(ctx, setup.first, bruno.ID); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	theirs, _, err := setup.service.Resolve(ctx, setup.second, Identity{ExternalSubject: "ana"})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}

	found, err := setup.service.Many(ctx, setup.first,
		[]string{ana.ID, bruno.ID, theirs.ID, "usr_AAAAAAAAAAAAAAAAAAAAAAAAAA"})
	if err != nil {
		t.Fatalf("Many() error = %v", err)
	}

	if len(found) != 1 || found[ana.ID].ID != ana.ID {
		t.Errorf("Many() = %v, want only %s", found, ana.ID)
	}
}

// TestManyOfNothingAsksNothing keeps an empty list from becoming a query.
func TestManyOfNothingAsksNothing(t *testing.T) {
	setup := newFixture(t)

	found, err := setup.service.Many(context.Background(), setup.first, nil)
	if err != nil {
		t.Fatalf("Many() error = %v", err)
	}
	if len(found) != 0 {
		t.Errorf("Many(nil) = %v, want nothing", found)
	}
}
