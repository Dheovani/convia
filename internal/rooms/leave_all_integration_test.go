package rooms

import (
	"context"
	"testing"

	"convia/internal/events"
)

/*
TestLeavingEveryRoomTellsEachAndDeletesWhatNobodyIsLeftIn is a person deleting
their account, as far as rooms go.
*/
func TestLeavingEveryRoomTellsEachAndDeletesWhatNobodyIsLeftIn(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	ana := setup.local(t, setup.first, "ana")
	bruno := setup.local(t, setup.first, "bruno")

	shared := setup.opens(t, setup.first, ana, "Shared")
	setup.join(t, setup.first, shared.ID, bruno)
	alone := setup.opens(t, setup.first, ana, "Alone")
	applications := setup.newRoom(t, setup.first, "The application's")
	setup.join(t, setup.first, applications.ID, ana)
	banning := setup.opens(t, setup.first, bruno, "Banning")
	setup.join(t, setup.first, banning.ID, ana)
	if err := setup.asPerson(setup.first, bruno).Ban(ctx, banning.ID, ana); err != nil {
		t.Fatalf("Ban() error = %v", err)
	}

	before := len(setup.published.all())
	departure, err := setup.service.LeaveAll(ctx, setup.first, ana)
	if err != nil {
		t.Fatalf("LeaveAll() error = %v", err)
	}
	if departure.Left != 3 || departure.Deleted != 1 {
		t.Errorf("LeaveAll() = %+v, want three rooms left and one deleted", departure)
	}

	if rooms, _ := setup.service.RoomIDsOf(ctx, setup.first, ana); len(rooms) != 0 {
		t.Errorf("ana is still in %v", rooms)
	}
	if owner := setup.ownerOf(t, setup.first, shared.ID); owner != bruno {
		t.Errorf("the shared room's owner is %q, want bruno %q", owner, bruno)
	}
	for name, want := range map[string]struct {
		room   Room
		status Status
	}{
		"the room she was alone in": {alone, StatusDeleted},
		"the shared room":           {shared, StatusOpen},
		"the application's room":    {applications, StatusOpen},
	} {
		stored, err := setup.service.Get(ctx, setup.first, want.room.ID)
		if err != nil || stored.Status != want.status {
			t.Errorf("%s = %s, %v, want %s", name, stored.Status, err, want.status)
		}
	}
	banned, err := setup.asPerson(setup.first, bruno).Bans(ctx, banning.ID, MembershipOptions{Limit: 10})
	if err != nil || len(banned.People) != 0 {
		t.Errorf("Bans() = %+v, %v, want the ban naming ana gone", banned.People, err)
	}

	removed, deleted := 0, 0
	for _, event := range setup.published.all()[before:] {
		switch event.Type {
		case events.MemberRemoved:
			removed++
		case events.RoomDeleted:
			deleted++
		}
	}
	if removed != 3 || deleted != 1 {
		t.Errorf("announced %d departures and %d deletions, want 3 and 1", removed, deleted)
	}
}
