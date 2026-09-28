package messages

import (
	"context"
	"testing"
)

/*
TestWhatOnePersonWroteIsReadAcrossRoomsAndOnlyTheirs.

`ByAuthor` exists for export, and export is the one place where returning one
message too many is not a bug in a listing — it is handing somebody another
person's words. The two halves are tested together because they fail together:
a query that forgets the author returns the room, and a query that forgets the
room returns only part of the person.
*/
func TestWhatOnePersonWroteIsReadAcrossRoomsAndOnlyTheirs(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	kitchen := setup.newRoom(t, setup.first)
	standup := setup.newRoom(t, setup.first)

	ana := setup.newAuthor(t, setup.first, "ana")
	bruno := setup.newAuthor(t, setup.first, "bruno")

	mine := map[string]bool{}
	for _, said := range []struct {
		room   string
		author Author
		body   string
	}{
		{kitchen.ID, ana, "morning"},
		{standup.ID, ana, "blocked on the migration"},
		{kitchen.ID, bruno, "morning to you too"},
		{standup.ID, bruno, "i can look at it"},
		{kitchen.ID, ana, "thanks"},
	} {
		message, err := setup.service.Post(ctx, setup.first, said.room, said.author, said.body)
		if err != nil {
			t.Fatalf("post %q: %v", said.body, err)
		}
		if said.author.UserID == ana.UserID {
			mine[message.ID] = true
		}
	}

	page, more, err := setup.store.ByAuthor(ctx, setup.first, ana.UserID, nil, 100)
	if err != nil {
		t.Fatalf("ByAuthor() error = %v", err)
	}
	if more {
		t.Error("ByAuthor() reported more than a hundred messages for five")
	}

	if len(page) != len(mine) {
		t.Fatalf("read %d messages, want %d", len(page), len(mine))
	}

	rooms := map[string]bool{}
	for _, message := range page {
		if !mine[message.ID] {
			t.Errorf("read %q, which this person did not write", message.ID)
		}
		if message.Author.UserID != ana.UserID {
			t.Errorf("read a message by %q", message.Author.UserID)
		}
		rooms[message.RoomID] = true
	}

	if len(rooms) != 2 {
		t.Errorf("read messages from %d rooms, want both — this cut is supposed to cross them", len(rooms))
	}
}

/*
TestAWalkThroughSomebodysMessagesReachesTheEnd.

The cursor is `(created_at, id)` rather than the sequence a room page uses,
because a sequence orders one room and says nothing across rooms. This walks in
pages of one, which is the size that catches a cursor comparison that is subtly
wrong: an off-by-one either repeats a message for ever or skips every second
one, and both are invisible at a page size that fits everything.
*/
func TestAWalkThroughSomebodysMessagesReachesTheEnd(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	room := setup.newRoom(t, setup.first)
	ana := setup.newAuthor(t, setup.first, "ana")

	const said = 6
	for range said {
		if _, err := setup.service.Post(ctx, setup.first, room.ID, ana, "one more"); err != nil {
			t.Fatalf("post: %v", err)
		}
	}

	seen := map[string]bool{}
	var after *Written

	for round := 0; round <= said+2; round++ {
		page, more, err := setup.store.ByAuthor(ctx, setup.first, ana.UserID, after, 1)
		if err != nil {
			t.Fatalf("ByAuthor() error = %v", err)
		}

		for _, message := range page {
			if seen[message.ID] {
				t.Fatalf("%q was read twice, so the cursor does not move", message.ID)
			}
			seen[message.ID] = true
			after = &Written{CreatedAt: message.CreatedAt, ID: message.ID}
		}

		if !more {
			break
		}
	}

	if len(seen) != said {
		t.Errorf("walked %d messages one page at a time, want %d", len(seen), said)
	}
}

// TestSomebodyElsesTenantIsNotReadable: the application is part of the lookup
// here as it is in every other statement, so a valid identifier from another
// tenant reaches nothing.
func TestSomebodyElsesTenantIsNotReadable(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	room := setup.newRoom(t, setup.first)
	ana := setup.newAuthor(t, setup.first, "ana")

	if _, err := setup.service.Post(ctx, setup.first, room.ID, ana, "morning"); err != nil {
		t.Fatalf("post: %v", err)
	}

	page, _, err := setup.store.ByAuthor(ctx, setup.second, ana.UserID, nil, 100)
	if err != nil {
		t.Fatalf("ByAuthor() error = %v", err)
	}
	if len(page) != 0 {
		t.Errorf("another tenant read %d of this application's messages", len(page))
	}
}
