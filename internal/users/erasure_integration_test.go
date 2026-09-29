package users

import (
	"context"
	"testing"
	"time"
)

// deleted resolves somebody, deletes them, and dates the deletion so that a
// test can put the retention window behind them without waiting a month.
func deleted(t *testing.T, setup fixture, subject string, ago time.Duration) User {
	t.Helper()
	ctx := context.Background()

	user, _, err := setup.service.Resolve(ctx, setup.first, Identity{
		ExternalSubject: subject,
		DisplayName:     "Ada Lovelace",
		Metadata:        map[string]string{"plan": "pro"},
	})
	if err != nil {
		t.Fatalf("resolve %q: %v", subject, err)
	}

	if err := setup.service.Delete(ctx, setup.first, user.ID); err != nil {
		t.Fatalf("delete %q: %v", subject, err)
	}

	/*
		Creation moves back with the deletion, because the schema refuses a user
		deleted before it existed. That constraint is the reason this helper
		cannot simply back-date one column, and it is worth keeping: a deletion
		date earlier than the row is a clock or a migration going wrong, and
		either would put somebody past a window they never entered.
	*/
	when := time.Now().UTC().Add(-ago)
	if _, err := setup.pool.Exec(ctx,
		`UPDATE users SET deleted_at = $1, created_at = $1, updated_at = $1 WHERE id = $2`,
		when, user.ID); err != nil {
		t.Fatalf("date the deletion of %q: %v", subject, err)
	}
	return user
}

/*
TestOnlyPeopleWhoseWindowHasClosedAreListed.

The window is the recoverable half of a deletion: an application that deleted
the wrong person has until then to say so. A sweep that ignored it would make
that promise false, and nobody would find out until somebody asked for a person
back and was told they were gone.
*/
func TestOnlyPeopleWhoseWindowHasClosedAreListed(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()
	store := NewStore(setup.pool)

	old := deleted(t, setup, "long-gone", 40*24*time.Hour)
	recent := deleted(t, setup, "just-left", 2*24*time.Hour)

	due, err := store.Expired(ctx, time.Now().UTC().Add(-30*24*time.Hour), 100)
	if err != nil {
		t.Fatalf("Expired() error = %v", err)
	}

	if len(due) != 1 {
		t.Fatalf("listed %d people, want 1: %v", len(due), due)
	}
	if due[0].ID != old.ID {
		t.Errorf("listed %q, want %q", due[0].ID, old.ID)
	}
	if due[0].ID == recent.ID {
		t.Error("somebody still inside their window was listed")
	}
}

// TestSomebodyStillHereIsNeverListed: only deleted users are ever candidates.
func TestSomebodyStillHereIsNeverListed(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()
	store := NewStore(setup.pool)

	if _, _, err := setup.service.Resolve(ctx, setup.first,
		Identity{ExternalSubject: "still-here", DisplayName: "Grace"}); err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}

	// Far in the future, so age is not what is keeping them off the list.
	due, err := store.Expired(ctx, time.Now().UTC().Add(365*24*time.Hour), 100)
	if err != nil {
		t.Fatalf("Expired() error = %v", err)
	}
	if len(due) != 0 {
		t.Errorf("listed %d people who were never deleted: %v", len(due), due)
	}
}

/*
TestErasingFreesTheSubjectAndKeepsTheRow.

Both halves matter and they pull in opposite directions. **The subject has to
go**, because an application that deletes somebody and later sees them again
must be able to resolve them into a new user — while the old row holds the
subject, the unique index refuses. **The row has to stay**, because messages
point at it and rooms were owned by it, and taking it away would take a
conversation apart to remove one name from it.
*/
func TestErasingFreesTheSubjectAndKeepsTheRow(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()
	store := NewStore(setup.pool)

	gone := deleted(t, setup, "customer-42", 40*24*time.Hour)

	erased, err := store.Erase(ctx, setup.first, gone.ID, time.Now().UTC())
	if err != nil {
		t.Fatalf("Erase() error = %v", err)
	}
	if !erased {
		t.Fatal("Erase() reported that it changed nothing")
	}

	var subject, name *string
	var metadata map[string]string
	var status Status
	if err := setup.pool.QueryRow(ctx,
		`SELECT external_subject, display_name, metadata, status FROM users WHERE id = $1`,
		gone.ID).Scan(&subject, &name, &metadata, &status); err != nil {
		t.Fatalf("read the erased row: %v", err)
	}

	if subject != nil {
		t.Errorf("the subject survived erasure: %q", *subject)
	}
	if name != nil {
		t.Errorf("the display name survived erasure: %q", *name)
	}
	if len(metadata) != 0 {
		t.Errorf("the metadata survived erasure: %v", metadata)
	}
	if status != StatusDeleted {
		t.Errorf("status = %q, want %q: the row must stay deleted, not vanish", status, StatusDeleted)
	}

	// And the freed subject resolves into somebody new.
	fresh, isNew, err := setup.service.Resolve(ctx, setup.first,
		Identity{ExternalSubject: "customer-42", DisplayName: "Somebody Else"})
	if err != nil {
		t.Fatalf("Resolve() after erasure error = %v", err)
	}
	if !isNew {
		t.Error("the freed subject resolved to the erased user rather than a new one")
	}
	if fresh.ID == gone.ID {
		t.Error("the erased user came back")
	}
}

/*
TestErasingTwiceIsRecognisableAsHavingNothingToDo.

The absence of the subject is what records that the work was done, rather than a
column saying so — there is nothing left to erase on a row with no subject, no
name and no metadata. This is what keeps a second pass from reporting success
for work it did not do, and what keeps the same person off the list for ever
after.
*/
func TestErasingTwiceIsRecognisableAsHavingNothingToDo(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()
	store := NewStore(setup.pool)

	gone := deleted(t, setup, "customer-42", 40*24*time.Hour)
	window := time.Now().UTC().Add(-30 * 24 * time.Hour)

	if _, err := store.Erase(ctx, setup.first, gone.ID, time.Now().UTC()); err != nil {
		t.Fatalf("the first Erase() error = %v", err)
	}

	again, err := store.Erase(ctx, setup.first, gone.ID, time.Now().UTC())
	if err != nil {
		t.Fatalf("the second Erase() error = %v", err)
	}
	if again {
		t.Error("the second erasure reported that it changed something")
	}

	due, err := store.Expired(ctx, window, 100)
	if err != nil {
		t.Fatalf("Expired() error = %v", err)
	}
	if len(due) != 0 {
		t.Errorf("an erased person is still listed as due: %v", due)
	}
}

// TestErasureCannotCrossATenant: the application is part of the WHERE clause
// here as it is everywhere else, so a valid identifier from another tenant
// reaches nothing.
func TestErasureCannotCrossATenant(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()
	store := NewStore(setup.pool)

	gone := deleted(t, setup, "customer-42", 40*24*time.Hour)

	erased, err := store.Erase(ctx, setup.second, gone.ID, time.Now().UTC())
	if err != nil {
		t.Fatalf("Erase() error = %v", err)
	}
	if erased {
		t.Error("another tenant erased this application's user")
	}
}

/*
TestDeletingDatesTheDeletion is what the whole window rests on.

A row that is deleted and undated would sit in the window for ever, which is the
failure that looks exactly like the janitor working: nothing is erased and
nothing complains. The database refuses it, and this is the test that says the
service does not try.
*/
func TestDeletingDatesTheDeletion(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	for what, remove := range map[string]func(string) error{
		"deleted by its application": func(id string) error {
			return setup.service.Delete(ctx, setup.first, id)
		},
		"retired with its person": func(id string) error {
			return setup.service.Retire(ctx, setup.first, id)
		},
	} {
		user, _, err := setup.service.Resolve(ctx, setup.first,
			Identity{ExternalSubject: what, DisplayName: "Ada"})
		if err != nil {
			t.Fatalf("resolve for %q: %v", what, err)
		}

		before := time.Now().UTC().Add(-time.Minute)
		if err := remove(user.ID); err != nil {
			t.Fatalf("%s: %v", what, err)
		}

		var when *time.Time
		if err := setup.pool.QueryRow(ctx,
			`SELECT deleted_at FROM users WHERE id = $1`, user.ID).Scan(&when); err != nil {
			t.Fatalf("read deleted_at for %q: %v", what, err)
		}
		if when == nil {
			t.Errorf("a user %s carries no deletion date, so its window never closes", what)
			continue
		}
		if when.Before(before) {
			t.Errorf("a user %s was dated %s, which is before it was deleted", what, when)
		}
	}
}
