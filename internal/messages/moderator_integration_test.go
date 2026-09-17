package messages

import (
	"context"
	"errors"
	"testing"

	"convia/internal/rooms"
)

// TestARoomsModeratorTakesDownWhatOthersSaid gives a moderator the owner's power
// over a conversation, and the tombstone says it was not its author.
func TestARoomsModeratorTakesDownWhatOthersSaid(t *testing.T) {
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

	said, err := setup.asPerson(setup.first, carla).Post(ctx, room.ID, "Something a moderator will take down.")
	if err != nil {
		t.Fatalf("Post() error = %v", err)
	}
	if _, err := setup.asPerson(setup.first, bruno).Delete(ctx, said.ID); !errors.Is(err, ErrNotAuthor) {
		t.Errorf("a member taking down somebody else's message error = %v, want %v", err, ErrNotAuthor)
	}

	// Naming a moderator asks for an account here, which this fixture does not
	// make; the flag is what Delete reads.
	if _, err := setup.pool.Exec(ctx, `UPDATE room_members SET moderator = true WHERE room_id = $1 AND user_id = $2`,
		room.ID, bruno); err != nil {
		t.Fatalf("make bruno a moderator: %v", err)
	}

	removed, err := setup.asPerson(setup.first, bruno).Delete(ctx, said.ID)
	if err != nil {
		t.Fatalf("a moderator taking down a message error = %v", err)
	}
	if !removed.Deleted() || removed.DeletedBy != RemovedByOwner {
		t.Errorf("the moderator's removal = deleted %v by %q, want a tombstone that is not its author's",
			removed.Deleted(), removed.DeletedBy)
	}

	sidebar, err := setup.asPerson(setup.first, bruno).Rooms(ctx, rooms.MembershipOptions{Limit: 10})
	if err != nil || len(sidebar.Rooms) != 1 || !sidebar.Rooms[0].Moderator {
		t.Errorf("Rooms() = %+v, %v, want the room marked as one bruno moderates", sidebar.Rooms, err)
	}
}
