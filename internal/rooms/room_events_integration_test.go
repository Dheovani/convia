package rooms

import (
	"context"
	"testing"

	"convia/internal/events"
)

/*
TestARoomsOwnChangesAreAnnouncedOnce is M18-029.

The owner renaming, closing, reopening and deleting a room is news to everybody
else in it. A transition that repeats the room's state is not a change and says
nothing, and none of the events carries the room's name.
*/
func TestARoomsOwnChangesAreAnnouncedOnce(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	ana := setup.newPerson(t, setup.first, "ana")
	opened, err := setup.service.CreateFor(ctx, setup.first, ana, Definition{Name: "Standup"})
	if err != nil {
		t.Fatalf("CreateFor() error = %v", err)
	}

	name := "Weekly standup"
	if _, err := setup.service.Update(ctx, setup.first, opened.ID, Change{Name: &name}, ""); err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	for range 2 {
		if _, err := setup.service.Close(ctx, setup.first, opened.ID); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	}
	for range 2 {
		if _, err := setup.service.Reopen(ctx, setup.first, opened.ID); err != nil {
			t.Fatalf("Reopen() error = %v", err)
		}
	}
	for range 2 {
		if err := setup.service.Delete(ctx, setup.first, opened.ID); err != nil {
			t.Fatalf("Delete() error = %v", err)
		}
	}

	var announced []events.Event
	for _, event := range setup.published.all() {
		switch event.Type {
		case events.RoomUpdated, events.RoomClosed, events.RoomReopened, events.RoomDeleted:
			announced = append(announced, event)
		}
	}

	want := []events.Type{events.RoomUpdated, events.RoomClosed, events.RoomReopened, events.RoomDeleted}
	if len(announced) != len(want) {
		t.Fatalf("%d room events were announced, want %d: %+v", len(announced), len(want), announced)
	}
	for index, kind := range want {
		event := announced[index]
		if event.Type != kind {
			t.Errorf("event %d is %q, want %q", index, event.Type, kind)
		}
		if event.Subject.Type != events.SubjectRoom || event.Subject.ID != opened.ID {
			t.Errorf("event %d is about %+v, want the room", index, event.Subject)
		}
		if event.ApplicationID != setup.first {
			t.Errorf("event %d belongs to %q", index, event.ApplicationID)
		}
		if len(event.Data) != 0 {
			t.Errorf("event %d carries %v, want nothing but the room", index, event.Data)
		}
	}
}
