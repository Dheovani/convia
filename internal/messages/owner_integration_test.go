package messages

import (
	"context"
	"errors"
	"testing"

	"convia/internal/rooms"
)

/*
TestTheOwnerOfARoomTakesDownWhatOthersSaid is the owner's power over a
conversation, and what everybody else can tell about it afterwards.

A member cannot take down somebody else's message, as before. The owner can, and
the tombstone says the owner did, so the room does not read it as its author
taking the words back. Taking down something its author already withdrew does
not rewrite who withdrew it.
*/
func TestTheOwnerOfARoomTakesDownWhatOthersSaid(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	ana := setup.newAuthor(t, setup.first, "ana").UserID
	bruno := setup.newAuthor(t, setup.first, "bruno").UserID
	carla := setup.newAuthor(t, setup.first, "carla").UserID

	room, err := setup.rooms.CreateFor(ctx, setup.first, ana, rooms.Definition{Name: "Standup"})
	if err != nil {
		t.Fatalf("CreateFor() error = %v", err)
	}
	setup.join(t, setup.first, room.ID, bruno)
	setup.join(t, setup.first, room.ID, carla)

	said, err := setup.asPerson(setup.first, bruno).Post(ctx, room.ID, "Something the owner will take down.")
	if err != nil {
		t.Fatalf("Post() error = %v", err)
	}

	if _, err := setup.asPerson(setup.first, carla).Delete(ctx, said.ID); !errors.Is(err, ErrNotAuthor) {
		t.Errorf("a member taking down somebody else's message error = %v, want %v", err, ErrNotAuthor)
	}

	removed, err := setup.asPerson(setup.first, ana).Delete(ctx, said.ID)
	if err != nil {
		t.Fatalf("the owner taking down a message error = %v", err)
	}
	if !removed.Deleted() || removed.DeletedBy != RemovedByOwner || removed.Body != "" {
		t.Errorf("the owner's removal = deleted %v by %q with body %q, want a tombstone the owner made",
			removed.Deleted(), removed.DeletedBy, removed.Body)
	}

	own, err := setup.asPerson(setup.first, bruno).Post(ctx, room.ID, "Something I take back.")
	if err != nil {
		t.Fatalf("Post() error = %v", err)
	}
	withdrawn, err := setup.asPerson(setup.first, bruno).Delete(ctx, own.ID)
	if err != nil || withdrawn.DeletedBy != RemovedByAuthor {
		t.Fatalf("withdrawing my own message = by %q, %v, want by its author", withdrawn.DeletedBy, err)
	}

	again, err := setup.asPerson(setup.first, ana).Delete(ctx, own.ID)
	if err != nil || again.DeletedBy != RemovedByAuthor {
		t.Errorf("the owner taking down something already withdrawn = by %q, %v, want it still by its author",
			again.DeletedBy, err)
	}
}

// TestNobodyOwnsAnApplicationsConversation keeps taking down somebody else's
// message out of reach in a room an application made, where nobody is owner.
func TestNobodyOwnsAnApplicationsConversation(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	room := setup.newRoom(t, setup.first)
	ana := setup.newAuthor(t, setup.first, "ana").UserID
	bruno := setup.newAuthor(t, setup.first, "bruno").UserID
	setup.join(t, setup.first, room.ID, ana)
	setup.join(t, setup.first, room.ID, bruno)

	said, err := setup.asPerson(setup.first, bruno).Post(ctx, room.ID, "Nobody here can take this down but me.")
	if err != nil {
		t.Fatalf("Post() error = %v", err)
	}
	if _, err := setup.asPerson(setup.first, ana).Delete(ctx, said.ID); !errors.Is(err, ErrNotAuthor) {
		t.Errorf("taking down a message in an application's room error = %v, want %v", err, ErrNotAuthor)
	}
}
