package rooms

import (
	"context"
	"slices"
	"testing"

	"convia/internal/events"
)

// membershipEvents is what the fixture announced about who belongs where.
func (setup fixture) membershipEvents() []events.Event {
	var announced []events.Event
	for _, event := range setup.published.all() {
		if event.Type == events.MemberAdded || event.Type == events.MemberRemoved {
			announced = append(announced, event)
		}
	}
	return announced
}

/*
TestEveryChangeOfPlaceIsAnnouncedOnce is M18-020.

A person added to a room by somebody else is the first membership change in
Convia that the request's own response cannot tell the person it happened to.
Their sidebar has no way to learn it except an event, and neither does an
application's other instance.

Only a change is announced, exactly as only a change is audited: an application
reconciling its list would otherwise announce every room it already agreed with.
*/
func TestEveryChangeOfPlaceIsAnnouncedOnce(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	ana := setup.newPerson(t, setup.first, "ana")
	bea := setup.newPerson(t, setup.first, "bea")

	opened, err := setup.service.CreateFor(ctx, setup.first, ana, Definition{Name: "Standup"})
	if err != nil {
		t.Fatalf("CreateFor() error = %v", err)
	}
	for range 2 {
		if _, _, err := setup.service.AddMember(ctx, setup.first, opened.ID, bea); err != nil {
			t.Fatalf("AddMember() error = %v", err)
		}
	}
	for range 2 {
		if _, err := setup.service.RemoveMember(ctx, setup.first, opened.ID, bea); err != nil {
			t.Fatalf("RemoveMember() error = %v", err)
		}
	}

	want := []struct {
		kind   events.Type
		person string
	}{
		{events.MemberAdded, ana},
		{events.MemberAdded, bea},
		{events.MemberRemoved, bea},
	}

	announced := setup.membershipEvents()
	if len(announced) != len(want) {
		t.Fatalf("%d membership events were announced, want %d: %+v", len(announced), len(want), announced)
	}

	for index, expected := range want {
		event := announced[index]
		if event.Type != expected.kind {
			t.Errorf("event %d is %q, want %q", index, event.Type, expected.kind)
		}
		if event.Subject.Type != events.SubjectRoom || event.Subject.ID != opened.ID {
			t.Errorf("event %d is about %+v, want the room", index, event.Subject)
		}
		if event.ApplicationID != setup.first {
			t.Errorf("event %d belongs to %q", index, event.ApplicationID)
		}
		if event.Data["user_id"] != expected.person {
			t.Errorf("event %d names %v, want %q", index, event.Data["user_id"], expected.person)
		}
	}
}

/*
TestAMembershipEventCarriesNoLabel keeps the room's name out of what streams.

A name is the application's label and may describe the people in the room, and
membership events go to every registered webhook destination as well as every
member's stream. Serialized rather than inspected, so that a field added later
under another name is caught too.
*/
func TestAMembershipEventCarriesNoLabel(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	const label = "Tuesday layoffs planning"
	ana := setup.newPerson(t, setup.first, "ana")
	if _, err := setup.service.CreateFor(ctx, setup.first, ana, Definition{Name: label}); err != nil {
		t.Fatalf("CreateFor() error = %v", err)
	}

	for _, event := range setup.membershipEvents() {
		for key, value := range event.Data {
			if text, isText := value.(string); isText && text == label {
				t.Errorf("a %s event carries the room's name under %q", event.Type, key)
			}
		}
	}
}

// TestErasureAnnouncesNothing records the decision announceMembership explains.
func TestErasureAnnouncesNothing(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	ana := setup.newPerson(t, setup.first, "ana")
	for _, name := range []string{"Standup", "Retro"} {
		if _, err := setup.service.CreateFor(ctx, setup.first, ana, Definition{Name: name}); err != nil {
			t.Fatalf("CreateFor() error = %v", err)
		}
	}
	before := len(setup.membershipEvents())

	if _, err := setup.service.ForgetMemberships(ctx, setup.first, ana); err != nil {
		t.Fatalf("ForgetMemberships() error = %v", err)
	}
	if after := len(setup.membershipEvents()); after != before {
		t.Errorf("erasure announced %d membership events", after-before)
	}
}

// TestRoomIDsOfIsEveryRoomAndOnlyThose covers what a person's stream reads:
// every room, unpaged, and nothing from another tenant or another person.
func TestRoomIDsOfIsEveryRoomAndOnlyThose(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	ana := setup.newPerson(t, setup.first, "ana")
	bea := setup.newPerson(t, setup.first, "bea")

	var hers []string
	for range maxPageSize + 1 {
		room, err := setup.service.CreateFor(ctx, setup.first, ana, Definition{Name: "Room"})
		if err != nil {
			t.Fatalf("CreateFor() error = %v", err)
		}
		hers = append(hers, room.ID)
	}
	if _, err := setup.service.CreateFor(ctx, setup.first, bea, Definition{Name: "Not hers"}); err != nil {
		t.Fatalf("CreateFor() error = %v", err)
	}

	got, err := setup.service.RoomIDsOf(ctx, setup.first, ana)
	if err != nil {
		t.Fatalf("RoomIDsOf() error = %v", err)
	}
	slices.Sort(got)
	slices.Sort(hers)
	if !slices.Equal(got, hers) {
		t.Errorf("RoomIDsOf() returned %d rooms, want her %d", len(got), len(hers))
	}

	elsewhere, err := setup.service.RoomIDsOf(ctx, setup.second, ana)
	if err != nil {
		t.Fatalf("RoomIDsOf() in another tenant error = %v", err)
	}
	if len(elsewhere) != 0 {
		t.Errorf("another tenant sees %d of her rooms", len(elsewhere))
	}
}
