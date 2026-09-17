package rooms

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"convia/internal/events"
)

// refusing is an announcer that cannot record anything.
type refusing struct{ err error }

func (announcer refusing) Publish(context.Context, events.Event) error { return announcer.err }

/*
TestAChangeThatCannotBeAnnouncedDoesNotHappen is M15-016: the event is recorded
in the transaction that makes the change, so when it cannot be, the change is
undone rather than left for nobody to hear about.
*/
func TestAChangeThatCannotBeAnnouncedDoesNotHappen(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	ana := setup.local(t, setup.first, "ana")
	bruno := setup.local(t, setup.first, "bruno")
	room := setup.opens(t, setup.first, ana, "Standup")

	refusal := errors.New("the journal is unreachable")
	failing := NewService(NewStore(setup.pool), setup.applications, setup.users, refusing{err: refusal},
		slog.New(slog.NewTextHandler(io.Discard, nil)))

	if _, _, err := failing.AddMember(ctx, setup.first, room.ID, bruno); !errors.Is(err, refusal) {
		t.Fatalf("AddMember() error = %v, want %v", err, refusal)
	}
	if in, _ := setup.service.IsMember(ctx, setup.first, room.ID, bruno); in {
		t.Error("bruno was added although nobody could be told")
	}

	if _, err := failing.CreateFor(ctx, setup.first, ana, Definition{Name: "Unheard"}); !errors.Is(err, refusal) {
		t.Fatalf("CreateFor() error = %v, want %v", err, refusal)
	}
	if rooms, _ := setup.service.RoomIDsOf(ctx, setup.first, ana); len(rooms) != 1 {
		t.Errorf("ana is in %v, want only the room that was announced", rooms)
	}

	if _, err := failing.Close(ctx, setup.first, room.ID); !errors.Is(err, refusal) {
		t.Fatalf("Close() error = %v, want %v", err, refusal)
	}
	if stored, _ := setup.service.Get(ctx, setup.first, room.ID); stored.Status != StatusOpen {
		t.Errorf("the room is %s, want it still open", stored.Status)
	}

	if err := failing.Delete(ctx, setup.first, room.ID); !errors.Is(err, refusal) {
		t.Fatalf("Delete() error = %v, want %v", err, refusal)
	}
	if stored, _ := setup.service.Get(ctx, setup.first, room.ID); stored.Status != StatusOpen {
		t.Errorf("the room is %s, want it not deleted", stored.Status)
	}
}
