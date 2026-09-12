package rooms

import (
	"context"
	"errors"
	"strings"
	"testing"

	"convia/internal/users"
)

// newPerson resolves one of an application's people into a Convia user.
func (setup fixture) newPerson(t *testing.T, applicationID, subject string) string {
	t.Helper()

	person, _, err := setup.users.Resolve(context.Background(), applicationID,
		users.Identity{ExternalSubject: subject})
	if err != nil {
		t.Fatalf("resolve %q: %v", subject, err)
	}
	return person.ID
}

// newRoom makes a room to belong to.
func (setup fixture) newRoom(t *testing.T, applicationID, name string) Room {
	t.Helper()

	room, err := setup.service.Create(context.Background(), applicationID, Definition{Name: name})
	if err != nil {
		t.Fatalf("create the %q room: %v", name, err)
	}
	return room
}

/*
TestAddingSomebodyTwiceIsAddingThemOnce is what lets an application reconcile
its own list against Convia's without telling the two cases apart.
*/
func TestAddingSomebodyTwiceIsAddingThemOnce(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	room := setup.newRoom(t, setup.first, "Standup")
	ana := setup.newPerson(t, setup.first, "ana")

	first, added, err := setup.service.AddMember(ctx, setup.first, room.ID, ana)
	if err != nil {
		t.Fatalf("AddMember() error = %v", err)
	}
	if !added {
		t.Error("the first call reported that nothing changed")
	}
	if first.RoomID != room.ID || first.UserID != ana {
		t.Errorf("AddMember() returned %+v", first)
	}

	again, added, err := setup.service.AddMember(ctx, setup.first, room.ID, ana)
	if err != nil {
		t.Fatalf("AddMember() a second time error = %v", err)
	}
	if added {
		t.Error("the second call reported that it added them again")
	}
	if !again.CreatedAt.Equal(first.CreatedAt) {
		t.Error("adding again moved the timestamp, so the place was replaced rather than kept")
	}
}

/*
TestRemovingSomebodyLeavesWhatTheySaid is the line between membership and
erasure.

Removal is about the future. The messages are the room's record of a
conversation that did happen, and withdrawing them would rewrite it for
everybody still there.
*/
func TestRemovingSomebodyLeavesWhatTheySaid(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	room := setup.newRoom(t, setup.first, "Standup")
	ana := setup.newPerson(t, setup.first, "ana")

	if _, _, err := setup.service.AddMember(ctx, setup.first, room.ID, ana); err != nil {
		t.Fatalf("AddMember() error = %v", err)
	}

	removed, err := setup.service.RemoveMember(ctx, setup.first, room.ID, ana)
	if err != nil {
		t.Fatalf("RemoveMember() error = %v", err)
	}
	if !removed {
		t.Error("RemoveMember() reported that nothing changed")
	}

	member, err := setup.service.IsMember(ctx, setup.first, room.ID, ana)
	if err != nil {
		t.Fatalf("IsMember() error = %v", err)
	}
	if member {
		t.Error("the person is still a member after being removed")
	}

	// Removing again is the state the caller asked for, not a failure.
	removed, err = setup.service.RemoveMember(ctx, setup.first, room.ID, ana)
	if err != nil {
		t.Errorf("removing a second time error = %v, want none", err)
	}
	if removed {
		t.Error("removing a second time reported that it removed something")
	}
}

/*
TestAPlaceIsOnlyGivenToSomebodyReal keeps a room from holding an identifier that
names nobody, which nothing downstream could resolve.
*/
func TestAPlaceIsOnlyGivenToSomebodyReal(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	room := setup.newRoom(t, setup.first, "Standup")
	ana := setup.newPerson(t, setup.first, "ana")

	if _, _, err := setup.service.AddMember(ctx, setup.first, room.ID,
		"usr_AAAAAAAAAAAAAAAAAAAAAAAAAA"); !errors.Is(err, ErrUserNotFound) {
		t.Errorf("adding a stranger error = %v, want %v", err, ErrUserNotFound)
	}

	if _, err := setup.users.Suspend(ctx, setup.first, ana); err != nil {
		t.Fatalf("Suspend() error = %v", err)
	}
	if _, _, err := setup.service.AddMember(ctx, setup.first, room.ID, ana); !errors.Is(err, ErrUserUnavailable) {
		t.Errorf("adding a suspended person error = %v, want %v", err, ErrUserUnavailable)
	}
}

/*
TestAClosedRoomStillTakesMembers keeps closing from meaning more than it says.

Closing stops new calls and new messages. It does not evict the people who were
there, and adding somebody to a finished room so that they can read its history
is a reasonable thing to want. A deleted room is gone from the API and is
refused.
*/
func TestAClosedRoomStillTakesMembers(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	room := setup.newRoom(t, setup.first, "Standup")
	ana := setup.newPerson(t, setup.first, "ana")

	if _, err := setup.service.Close(ctx, setup.first, room.ID); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if _, _, err := setup.service.AddMember(ctx, setup.first, room.ID, ana); err != nil {
		t.Errorf("adding to a closed room error = %v, want none", err)
	}

	gone := setup.newRoom(t, setup.first, "Retired")
	if err := setup.service.Delete(ctx, setup.first, gone.ID); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if _, _, err := setup.service.AddMember(ctx, setup.first, gone.ID, ana); !errors.Is(err, ErrNotFound) {
		t.Errorf("adding to a deleted room error = %v, want %v", err, ErrNotFound)
	}
}

/*
TestMembershipDoesNotCrossTenants is the boundary every store method in Convia
is scoped by, applied to the newest table.
*/
func TestMembershipDoesNotCrossTenants(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	room := setup.newRoom(t, setup.first, "Standup")
	ours := setup.newPerson(t, setup.first, "ana")
	theirs := setup.newPerson(t, setup.second, "ana")

	if _, _, err := setup.service.AddMember(ctx, setup.first, room.ID, ours); err != nil {
		t.Fatalf("AddMember() error = %v", err)
	}

	if _, _, err := setup.service.AddMember(ctx, setup.second, room.ID, theirs); !errors.Is(err, ErrNotFound) {
		t.Errorf("another tenant added somebody to our room: %v", err)
	}

	/*
		The person is one of theirs and the room is one of ours, so the honest
		answer is that the room is not there. Saying "that user is not in that
		room" would confirm the room exists to somebody who may not know.
	*/
	member, err := setup.service.IsMember(ctx, setup.second, room.ID, ours)
	if err != nil {
		t.Fatalf("IsMember() error = %v", err)
	}
	if member {
		t.Error("a membership was visible across a tenant boundary")
	}
}

/*
TestBothDirectionsOfMembershipPage covers the two questions the table answers:
who is in this room, and what rooms is this person in.
*/
func TestBothDirectionsOfMembershipPage(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	room := setup.newRoom(t, setup.first, "Standup")
	other := setup.newRoom(t, setup.first, "Support")

	var people []string
	for _, subject := range []string{"ana", "bruno", "carla"} {
		person := setup.newPerson(t, setup.first, subject)
		people = append(people, person)
		if _, _, err := setup.service.AddMember(ctx, setup.first, room.ID, person); err != nil {
			t.Fatalf("AddMember() error = %v", err)
		}
	}
	if _, _, err := setup.service.AddMember(ctx, setup.first, other.ID, people[0]); err != nil {
		t.Fatalf("AddMember() error = %v", err)
	}

	roster, err := setup.service.Members(ctx, setup.first, room.ID, MembershipOptions{Limit: 10})
	if err != nil {
		t.Fatalf("Members() error = %v", err)
	}
	if len(roster.Members) != 3 {
		t.Errorf("the room holds %d members, want 3", len(roster.Members))
	}
	if roster.NextCursor != "" {
		t.Errorf("NextCursor = %q at the end of a listing", roster.NextCursor)
	}

	// Paging is by the person, which is the order the primary key holds.
	firstPage, err := setup.service.Members(ctx, setup.first, room.ID, MembershipOptions{Limit: 2})
	if err != nil {
		t.Fatalf("Members() error = %v", err)
	}
	if len(firstPage.Members) != 2 || firstPage.NextCursor == "" {
		t.Fatalf("the first page is %+v", firstPage)
	}
	secondPage, err := setup.service.Members(ctx, setup.first, room.ID,
		MembershipOptions{Limit: 2, Cursor: firstPage.NextCursor})
	if err != nil {
		t.Fatalf("Members() error = %v", err)
	}
	if len(secondPage.Members) != 1 {
		t.Errorf("the second page holds %d, want 1", len(secondPage.Members))
	}
	for _, seen := range firstPage.Members {
		if seen.UserID == secondPage.Members[0].UserID {
			t.Error("the second page repeated somebody from the first")
		}
	}

	hers, err := setup.service.RoomsOf(ctx, setup.first, people[0], MembershipOptions{Limit: 10})
	if err != nil {
		t.Fatalf("RoomsOf() error = %v", err)
	}
	if len(hers.Members) != 2 {
		t.Errorf("she is in %d rooms, want 2", len(hers.Members))
	}

	his, err := setup.service.RoomsOf(ctx, setup.first, people[1], MembershipOptions{Limit: 10})
	if err != nil {
		t.Fatalf("RoomsOf() error = %v", err)
	}
	if len(his.Members) != 1 {
		t.Errorf("he is in %d rooms, want 1", len(his.Members))
	}
}

/*
TestForgettingMembershipsLeavesNothingBehind is the erasure half.

A place in a room names a person and a room they were in, which is a fact about
them, so it goes when they do.
*/
func TestForgettingMembershipsLeavesNothingBehind(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	ana := setup.newPerson(t, setup.first, "ana")
	for _, name := range []string{"Standup", "Support", "Design"} {
		room := setup.newRoom(t, setup.first, name)
		if _, _, err := setup.service.AddMember(ctx, setup.first, room.ID, ana); err != nil {
			t.Fatalf("AddMember() error = %v", err)
		}
	}

	forgotten, err := setup.service.ForgetMemberships(ctx, setup.first, ana)
	if err != nil {
		t.Fatalf("ForgetMemberships() error = %v", err)
	}
	if forgotten != 3 {
		t.Errorf("ForgetMemberships() removed %d, want 3", forgotten)
	}

	remaining, err := setup.service.RoomsOf(ctx, setup.first, ana, MembershipOptions{Limit: 10})
	if err != nil {
		t.Fatalf("RoomsOf() error = %v", err)
	}
	if len(remaining.Members) != 0 {
		t.Errorf("%d places survived erasure", len(remaining.Members))
	}
}

/*
TestOnlyAChangeIsAudited keeps an application's reconciliation from filling the
audit trail with events where nothing happened, which is how a trail stops being
read.
*/
func TestOnlyAChangeIsAudited(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	room := setup.newRoom(t, setup.first, "Standup")
	ana := setup.newPerson(t, setup.first, "ana")

	if _, _, err := setup.service.AddMember(ctx, setup.first, room.ID, ana); err != nil {
		t.Fatalf("AddMember() error = %v", err)
	}

	setup.logs.Reset()
	for index := 0; index < 3; index++ {
		if _, _, err := setup.service.AddMember(ctx, setup.first, room.ID, ana); err != nil {
			t.Fatalf("AddMember() error = %v", err)
		}
	}
	if count := strings.Count(setup.logs.String(), "room.member_added"); count != 0 {
		t.Errorf("re-adding an existing member recorded %d events, want 0", count)
	}

	setup.logs.Reset()
	if _, err := setup.service.RemoveMember(ctx, setup.first, room.ID, ana); err != nil {
		t.Fatalf("RemoveMember() error = %v", err)
	}
	if _, err := setup.service.RemoveMember(ctx, setup.first, room.ID, ana); err != nil {
		t.Fatalf("RemoveMember() error = %v", err)
	}
	if count := strings.Count(setup.logs.String(), "room.member_removed"); count != 1 {
		t.Errorf("two removals recorded %d events, want 1", count)
	}
}
