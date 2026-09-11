package messages

import (
	"context"
	"testing"
)

/*
TestAReaderStartsHavingReadNothing is the answer a sidebar needs for a room
somebody has never opened.

It is not an error and it is not an empty response: they have read nothing, and
everything said there is waiting for them.
*/
func TestAReaderStartsHavingReadNothing(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	room := setup.newRoom(t, setup.first)
	ana := setup.newAuthor(t, setup.first, "ana")
	bruno := setup.newAuthor(t, setup.first, "bruno")

	for index := 0; index < 3; index++ {
		if _, err := setup.service.Post(ctx, setup.first, room.ID, ana, "hello"); err != nil {
			t.Fatalf("Post() error = %v", err)
		}
	}

	state, err := setup.service.ReadState(ctx, setup.first, room.ID, bruno.UserID)
	if err != nil {
		t.Fatalf("ReadState() error = %v", err)
	}
	if state.Sequence != Unseen {
		t.Errorf("a new reader starts at %d, want %d", state.Sequence, Unseen)
	}
	if state.Unread != 3 {
		t.Errorf("unread = %d, want 3", state.Unread)
	}
	if !state.UpdatedAt.IsZero() {
		t.Errorf("somebody who read nothing carries a timestamp: %v", state.UpdatedAt)
	}
}

/*
TestNobodyHasUnreadMessagesFromThemselves is a product decision worth a test,
because it is invisible until somebody notices their own badge lighting up as
they type.
*/
func TestNobodyHasUnreadMessagesFromThemselves(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	room := setup.newRoom(t, setup.first)
	ana := setup.newAuthor(t, setup.first, "ana")
	bruno := setup.newAuthor(t, setup.first, "bruno")

	for index := 0; index < 4; index++ {
		if _, err := setup.service.Post(ctx, setup.first, room.ID, ana, "mine"); err != nil {
			t.Fatalf("Post() error = %v", err)
		}
	}

	hers, err := setup.service.ReadState(ctx, setup.first, room.ID, ana.UserID)
	if err != nil {
		t.Fatalf("ReadState() error = %v", err)
	}
	if hers.Unread != 0 {
		t.Errorf("the author has %d unread messages of her own, want 0", hers.Unread)
	}

	his, err := setup.service.ReadState(ctx, setup.first, room.ID, bruno.UserID)
	if err != nil {
		t.Fatalf("ReadState() error = %v", err)
	}
	if his.Unread != 4 {
		t.Errorf("somebody else has %d unread, want 4", his.Unread)
	}
}

/*
TestAWithdrawnMessageIsNotSomethingToRead keeps a tombstone from holding a badge
open over text that no longer exists.
*/
func TestAWithdrawnMessageIsNotSomethingToRead(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	room := setup.newRoom(t, setup.first)
	ana := setup.newAuthor(t, setup.first, "ana")
	bruno := setup.newAuthor(t, setup.first, "bruno")

	var written []Message
	for index := 0; index < 3; index++ {
		message, err := setup.service.Post(ctx, setup.first, room.ID, ana, "hello")
		if err != nil {
			t.Fatalf("Post() error = %v", err)
		}
		written = append(written, message)
	}

	if _, err := setup.service.Delete(ctx, setup.first, written[1].ID, ana); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}

	state, err := setup.service.ReadState(ctx, setup.first, room.ID, bruno.UserID)
	if err != nil {
		t.Fatalf("ReadState() error = %v", err)
	}
	if state.Unread != 2 {
		t.Errorf("unread = %d, want 2 with the tombstone not counted", state.Unread)
	}
}

/*
TestAReadPositionOnlyMovesForward is what keeps two devices from fighting.

A phone that finishes reporting a second after the laptop reports a position
behind it. Taking the newer report literally would pull the badge back, and
refusing it would make an ordinary race look like a client error.
*/
func TestAReadPositionOnlyMovesForward(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	room := setup.newRoom(t, setup.first)
	ana := setup.newAuthor(t, setup.first, "ana")
	bruno := setup.newAuthor(t, setup.first, "bruno")

	for index := 0; index < 5; index++ {
		if _, err := setup.service.Post(ctx, setup.first, room.ID, ana, "hello"); err != nil {
			t.Fatalf("Post() error = %v", err)
		}
	}

	ahead, err := setup.service.MarkRead(ctx, setup.first, room.ID, bruno.UserID, 4)
	if err != nil {
		t.Fatalf("MarkRead() error = %v", err)
	}
	if ahead.Sequence != 4 || ahead.Unread != 1 {
		t.Errorf("after marking 4: sequence %d, unread %d; want 4 and 1", ahead.Sequence, ahead.Unread)
	}

	behind, err := setup.service.MarkRead(ctx, setup.first, room.ID, bruno.UserID, 2)
	if err != nil {
		t.Fatalf("MarkRead() behind the stored position error = %v, want none", err)
	}
	if behind.Sequence != 4 {
		t.Errorf("a stale device pulled the position back to %d", behind.Sequence)
	}
	if !behind.UpdatedAt.Equal(ahead.UpdatedAt) {
		t.Error("a mark that changed nothing still moved the timestamp")
	}

	/*
		Marking the same position again is the state the caller asked for. It
		must not be an error, because a client that retries a request it did
		not see the answer to would otherwise be told it did something wrong.
	*/
	if _, err := setup.service.MarkRead(ctx, setup.first, room.ID, bruno.UserID, 4); err != nil {
		t.Errorf("re-marking the current position error = %v, want none", err)
	}
}

/*
TestAPositionBeyondTheHistoryIsRefused keeps a guessed sequence from silently
suppressing messages the client never received.
*/
func TestAPositionBeyondTheHistoryIsRefused(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	room := setup.newRoom(t, setup.first)
	ana := setup.newAuthor(t, setup.first, "ana")
	bruno := setup.newAuthor(t, setup.first, "bruno")

	if _, err := setup.service.Post(ctx, setup.first, room.ID, ana, "hello"); err != nil {
		t.Fatalf("Post() error = %v", err)
	}

	if _, err := setup.service.MarkRead(ctx, setup.first, room.ID, bruno.UserID, 99); err == nil {
		t.Error("a position past the end of the history was accepted")
	}
	if _, err := setup.service.MarkRead(ctx, setup.first, room.ID, bruno.UserID, 0); err == nil {
		t.Error("a position of zero was accepted, which is the absence of one")
	}
}

/*
TestReadStateIsPerRoomAndPerPerson keeps one badge from answering for another.
*/
func TestReadStateIsPerRoomAndPerPerson(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	first := setup.newRoom(t, setup.first)
	second := setup.newRoom(t, setup.first)
	ana := setup.newAuthor(t, setup.first, "ana")
	bruno := setup.newAuthor(t, setup.first, "bruno")

	for _, room := range []string{first.ID, second.ID} {
		for index := 0; index < 2; index++ {
			if _, err := setup.service.Post(ctx, setup.first, room, ana, "hello"); err != nil {
				t.Fatalf("Post() error = %v", err)
			}
		}
	}

	if _, err := setup.service.MarkRead(ctx, setup.first, first.ID, bruno.UserID, 2); err != nil {
		t.Fatalf("MarkRead() error = %v", err)
	}

	caughtUp, err := setup.service.ReadState(ctx, setup.first, first.ID, bruno.UserID)
	if err != nil {
		t.Fatalf("ReadState() error = %v", err)
	}
	if caughtUp.Unread != 0 {
		t.Errorf("the room he read has %d unread", caughtUp.Unread)
	}

	untouched, err := setup.service.ReadState(ctx, setup.first, second.ID, bruno.UserID)
	if err != nil {
		t.Fatalf("ReadState() error = %v", err)
	}
	if untouched.Unread != 2 {
		t.Errorf("the other room has %d unread, want 2", untouched.Unread)
	}
}

/*
TestReadStateDoesNotCrossTenants is the boundary every store method in Convia is
scoped by, applied to the newest table.
*/
func TestReadStateDoesNotCrossTenants(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	room := setup.newRoom(t, setup.first)
	theirs := setup.newAuthor(t, setup.second, "ana")

	if _, err := setup.service.ReadState(ctx, setup.second, room.ID, theirs.UserID); err == nil {
		t.Error("another tenant read a read state in a room that is not theirs")
	}
	if _, err := setup.service.MarkRead(ctx, setup.second, room.ID, theirs.UserID, 1); err == nil {
		t.Error("another tenant marked a room read that is not theirs")
	}
}
