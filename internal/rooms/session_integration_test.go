package rooms

import (
	"context"
	"errors"
	"slices"
	"testing"

	"convia/internal/sessions"
)

// asPerson binds the fixture's services to one signed-in person.
func (setup fixture) asPerson(applicationID, userID string) *Personal {
	return AsPerson(setup.service, setup.users, sessions.Principal{
		SessionID:     "ses_4XZQP7KN2VJH6TBWMDR3YAFC5E",
		AccountID:     "acc_7QK4XMZP2VJH6TBWNDR3YAFC5E",
		UserID:        userID,
		ApplicationID: applicationID,
	})
}

// join puts somebody in a room the way an application would.
func (setup fixture) join(t *testing.T, applicationID, roomID, userID string) {
	t.Helper()

	if _, _, err := setup.service.AddMember(context.Background(), applicationID, roomID, userID); err != nil {
		t.Fatalf("AddMember() error = %v", err)
	}
}

func identifiers(page People) []string {
	found := make([]string, 0, len(page.People))
	for _, person := range page.People {
		found = append(found, person.UserID)
	}
	return found
}

/*
TestOpeningARoomPutsYouInIt is the reason creation is a person's act at all.

A room a person opens and cannot then reach would be useless, so being in it is
part of opening it.
*/
func TestOpeningARoomPutsYouInIt(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	ana := setup.newPerson(t, setup.first, "ana")

	room, err := setup.asPerson(setup.first, ana).Create(ctx, "Weekend plans")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if room.Name != "Weekend plans" || room.Status != StatusOpen {
		t.Errorf("Create() returned %+v", room)
	}

	member, err := setup.service.IsMember(ctx, setup.first, room.ID, ana)
	if err != nil {
		t.Fatalf("IsMember() error = %v", err)
	}
	if !member {
		t.Error("the person who opened the room is not in it")
	}
}

/*
TestARoomAndItsFirstMemberAreOneWrite is the atomicity the session surface
depends on.

The member is made to fail — an identifier with the right shape that names
nobody, which the foreign key refuses — and the room must not survive it. A room
left behind with nobody in it is a room no person could ever reach again.
*/
func TestARoomAndItsFirstMemberAreOneWrite(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	room, err := define(setup.first, Definition{Name: "Orphan"})
	if err != nil {
		t.Fatalf("define() error = %v", err)
	}

	nobody := Member{
		ApplicationID: setup.first,
		RoomID:        room.ID,
		UserID:        "usr_AAAAAAAAAAAAAAAAAAAAAAAAAA",
		CreatedAt:     room.CreatedAt,
	}
	if err := setup.service.store.CreateWithMember(ctx, room, nobody); err == nil {
		t.Fatal("CreateWithMember() accepted a member who does not exist")
	}

	if _, err := setup.service.store.Get(ctx, setup.first, room.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("the room outlived its failed first member: Get() error = %v, want %v", err, ErrNotFound)
	}
}

/*
TestYouCanAddOnlySomebodyYouAlreadyShareARoomWith is discovery, and its privacy
rule.

Every reason somebody cannot be added is one error: an identifier naming nobody,
a stranger, and somebody who shares a room but is suspended. A person must not
be able to tell which identifiers exist, or who is suspended.
*/
func TestYouCanAddOnlySomebodyYouAlreadyShareARoomWith(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	ana := setup.newPerson(t, setup.first, "ana")
	bruno := setup.newPerson(t, setup.first, "bruno")
	carla := setup.newPerson(t, setup.first, "carla")
	dora := setup.newPerson(t, setup.first, "dora")

	standup := setup.newRoom(t, setup.first, "Standup")
	for _, person := range []string{ana, bruno, dora} {
		setup.join(t, setup.first, standup.ID, person)
	}
	if _, err := setup.users.Suspend(ctx, setup.first, dora); err != nil {
		t.Fatalf("Suspend() error = %v", err)
	}

	person := setup.asPerson(setup.first, ana)
	weekend, err := person.Create(ctx, "Weekend plans")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	if _, added, err := person.Add(ctx, weekend.ID, bruno); err != nil || !added {
		t.Errorf("adding somebody she shares a room with: added = %v, error = %v", added, err)
	}

	refused := map[string]string{
		"a stranger":                            carla,
		"an identifier that names nobody":       "usr_AAAAAAAAAAAAAAAAAAAAAAAAAA",
		"something that is not an identifier":   "not-a-user",
		"somebody who shares a room, suspended": dora,
	}
	for name, userID := range refused {
		t.Run(name, func(t *testing.T) {
			if _, _, err := person.Add(ctx, weekend.ID, userID); !errors.Is(err, ErrUserNotFound) {
				t.Errorf("Add() error = %v, want %v", err, ErrUserNotFound)
			}
		})
	}
}

// TestAddingToARoomYouAreNotInIsNotThere keeps a person from reaching into a
// room by guessing its identifier.
func TestAddingToARoomYouAreNotInIsNotThere(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	ana := setup.newPerson(t, setup.first, "ana")
	bruno := setup.newPerson(t, setup.first, "bruno")

	shared := setup.newRoom(t, setup.first, "Shared")
	setup.join(t, setup.first, shared.ID, ana)
	setup.join(t, setup.first, shared.ID, bruno)

	hers, err := setup.asPerson(setup.first, ana).Create(ctx, "Hers")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	if _, _, err := setup.asPerson(setup.first, bruno).Add(ctx, hers.ID, bruno); !errors.Is(err, ErrNotFound) {
		t.Errorf("adding himself to her room: error = %v, want %v", err, ErrNotFound)
	}
}

/*
TestPeopleAreOnlyThoseYouShareALiveRoomWith is the list discovery is built on,
and each exclusion is somebody who must not appear in it.
*/
func TestPeopleAreOnlyThoseYouShareALiveRoomWith(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	ana := setup.newPerson(t, setup.first, "ana")
	bruno := setup.newPerson(t, setup.first, "bruno")
	stranger := setup.newPerson(t, setup.first, "stranger")
	suspended := setup.newPerson(t, setup.first, "suspended")
	formerly := setup.newPerson(t, setup.first, "formerly")
	elsewhere := setup.newPerson(t, setup.second, "ana")

	standup := setup.newRoom(t, setup.first, "Standup")
	for _, person := range []string{ana, bruno, suspended} {
		setup.join(t, setup.first, standup.ID, person)
	}
	if _, err := setup.users.Suspend(ctx, setup.first, suspended); err != nil {
		t.Fatalf("Suspend() error = %v", err)
	}

	gone := setup.newRoom(t, setup.first, "Gone")
	setup.join(t, setup.first, gone.ID, ana)
	setup.join(t, setup.first, gone.ID, formerly)
	if err := setup.service.Delete(ctx, setup.first, gone.ID); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}

	page, err := setup.asPerson(setup.first, ana).People(ctx, MembershipOptions{Limit: 10})
	if err != nil {
		t.Fatalf("People() error = %v", err)
	}

	listed := identifiers(page)
	if !slices.Equal(listed, []string{bruno}) {
		t.Errorf("People() = %v, want only %v", listed, bruno)
	}

	for name, userID := range map[string]string{
		"herself":                      ana,
		"a stranger":                   stranger,
		"somebody suspended":           suspended,
		"somebody from a deleted room": formerly,
		"a namesake in another tenant": elsewhere,
	} {
		if slices.Contains(listed, userID) {
			t.Errorf("People() lists %s", name)
		}
	}
}

// TestPeoplePagesByIdentifier keeps the listing complete across pages, which a
// DISTINCT over a join is easy to get wrong.
func TestPeoplePagesByIdentifier(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	ana := setup.newPerson(t, setup.first, "ana")
	first := setup.newRoom(t, setup.first, "First")
	second := setup.newRoom(t, setup.first, "Second")
	setup.join(t, setup.first, first.ID, ana)
	setup.join(t, setup.first, second.ID, ana)

	var everybody []string
	for _, subject := range []string{"b", "c", "d", "e", "f"} {
		person := setup.newPerson(t, setup.first, subject)
		everybody = append(everybody, person)
		// In both rooms, so a join that forgot DISTINCT would list them twice.
		setup.join(t, setup.first, first.ID, person)
		setup.join(t, setup.first, second.ID, person)
	}
	slices.Sort(everybody)

	var collected []string
	options := MembershipOptions{Limit: 2}
	for range 10 {
		page, err := setup.asPerson(setup.first, ana).People(ctx, options)
		if err != nil {
			t.Fatalf("People() error = %v", err)
		}
		collected = append(collected, identifiers(page)...)
		if page.NextCursor == "" {
			break
		}
		options.Cursor = page.NextCursor
	}

	if !slices.Equal(collected, everybody) {
		t.Errorf("paging collected %v, want %v", collected, everybody)
	}
}

// TestLeavingIsYoursAndOnlyOnce covers the one way out of a room, and that a
// second attempt is told the room is not there rather than succeeding again.
func TestLeavingIsYoursAndOnlyOnce(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	ana := setup.newPerson(t, setup.first, "ana")
	bruno := setup.newPerson(t, setup.first, "bruno")

	room := setup.newRoom(t, setup.first, "Standup")
	setup.join(t, setup.first, room.ID, ana)
	setup.join(t, setup.first, room.ID, bruno)

	person := setup.asPerson(setup.first, ana)
	if err := person.Leave(ctx, room.ID); err != nil {
		t.Fatalf("Leave() error = %v", err)
	}
	if err := person.Leave(ctx, room.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("leaving again: error = %v, want %v", err, ErrNotFound)
	}

	still, err := setup.service.IsMember(ctx, setup.first, room.ID, bruno)
	if err != nil || !still {
		t.Errorf("leaving took somebody else with her: member = %v, error = %v", still, err)
	}
}

// TestMembersAreOnlyVisibleFromInside is the privacy rule every route on this
// surface keeps, applied to who is in a room.
func TestMembersAreOnlyVisibleFromInside(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	ana := setup.newPerson(t, setup.first, "ana")
	bruno := setup.newPerson(t, setup.first, "bruno")

	hers, err := setup.asPerson(setup.first, ana).Create(ctx, "Hers")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	inside, err := setup.asPerson(setup.first, ana).Members(ctx, hers.ID, MembershipOptions{})
	if err != nil {
		t.Fatalf("Members() error = %v", err)
	}
	if !slices.Equal(identifiers(inside), []string{ana}) {
		t.Errorf("Members() = %v, want only %v", identifiers(inside), ana)
	}

	if _, err := setup.asPerson(setup.first, bruno).Members(ctx, hers.ID,
		MembershipOptions{}); !errors.Is(err, ErrNotFound) {
		t.Errorf("a stranger reading who is in her room: error = %v, want %v", err, ErrNotFound)
	}
}
