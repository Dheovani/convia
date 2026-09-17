package rooms

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"

	"convia/internal/events"
)

// name has an owner make somebody a moderator.
func (setup fixture) name(t *testing.T, roomID, ownerID, userID string) {
	t.Helper()

	if err := setup.asPerson(setup.first, ownerID).NameModerator(context.Background(), roomID, userID, true); err != nil {
		t.Fatalf("NameModerator() error = %v", err)
	}
}

// roleEvents is what the fixture announced about who may do what, as user and role.
func (setup fixture) roleEvents() [][2]string {
	var announced [][2]string
	for _, event := range setup.published.all() {
		if event.Type == events.MemberRoleChanged {
			user, _ := event.Data["user_id"].(string)
			role, _ := event.Data["role"].(string)
			announced = append(announced, [2]string{user, role})
		}
	}
	return announced
}

/*
TestAModeratorModeratesPeopleButNotTheRoom is docs/adr/0015: a moderator removes
and bans members and lifts bans, and is refused everything about the room itself
and everything aimed at the owner or another moderator.
*/
func TestAModeratorModeratesPeopleButNotTheRoom(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	ana := setup.local(t, setup.first, "ana")
	bruno := setup.local(t, setup.first, "bruno")
	carla := setup.local(t, setup.first, "carla")
	dora := setup.local(t, setup.first, "dora")
	edu := setup.local(t, setup.first, "edu")

	room := setup.opens(t, setup.first, ana, "Standup")
	for _, person := range []string{bruno, carla, dora, edu} {
		setup.join(t, setup.first, room.ID, person)
	}
	setup.name(t, room.ID, ana, bruno)
	setup.name(t, room.ID, ana, carla)

	moderator := setup.asPerson(setup.first, bruno)

	page, err := moderator.Members(ctx, room.ID, MembershipOptions{Limit: 10})
	if err != nil {
		t.Fatalf("Members() error = %v", err)
	}
	roles := map[string]Role{}
	for _, person := range page.People {
		roles[person.UserID] = person.Role
	}
	if roles[ana] != RoleOwner || roles[bruno] != RoleModerator || roles[carla] != RoleModerator ||
		roles[dora] != RoleMember {
		t.Errorf("roles = %v, want ana the owner, bruno and carla moderators, dora a member", roles)
	}

	for name, err := range map[string]error{
		"removing the owner":           moderator.Remove(ctx, room.ID, ana),
		"banning the owner":            moderator.Ban(ctx, room.ID, ana),
		"removing another moderator":   moderator.Remove(ctx, room.ID, carla),
		"banning another moderator":    moderator.Ban(ctx, room.ID, carla),
		"naming a moderator":           moderator.NameModerator(ctx, room.ID, dora, true),
		"deleting the room":            moderator.Delete(ctx, room.ID),
		"handing the room over":        func() error { _, err := moderator.Transfer(ctx, room.ID, bruno); return err }(),
		"renaming the room":            func() error { _, err := moderator.Rename(ctx, room.ID, "Mine"); return err }(),
		"closing the room":             func() error { _, err := moderator.Close(ctx, room.ID); return err }(),
		"unnaming the other moderator": moderator.NameModerator(ctx, room.ID, carla, false),
	} {
		if !errors.Is(err, ErrNotOwner) {
			t.Errorf("%s error = %v, want %v", name, err, ErrNotOwner)
		}
	}
	var validation ValidationError
	if err := moderator.Remove(ctx, room.ID, bruno); !errors.As(err, &validation) {
		t.Errorf("a moderator removing themselves error = %v, want a validation error", err)
	}

	if err := moderator.Remove(ctx, room.ID, dora); err != nil {
		t.Errorf("a moderator removing a member error = %v", err)
	}
	if err := moderator.Ban(ctx, room.ID, edu); err != nil {
		t.Errorf("a moderator banning a member error = %v", err)
	}
	banned, err := moderator.Bans(ctx, room.ID, MembershipOptions{Limit: 10})
	if err != nil || len(banned.People) != 1 || banned.People[0].UserID != edu {
		t.Errorf("Bans() = %+v, %v, want edu", banned.People, err)
	}
	if err := moderator.Unban(ctx, room.ID, edu); err != nil {
		t.Errorf("a moderator lifting a ban error = %v", err)
	}

	if err := setup.asPerson(setup.first, ana).NameModerator(ctx, room.ID, carla, false); err != nil {
		t.Fatalf("unnaming carla error = %v", err)
	}
	if err := setup.asPerson(setup.first, carla).Remove(ctx, room.ID, bruno); !errors.Is(err, ErrNotModerator) {
		t.Errorf("somebody who stopped moderating removing a moderator error = %v, want %v", err, ErrNotModerator)
	}
	if err := moderator.Remove(ctx, room.ID, carla); err != nil {
		t.Errorf("removing somebody who stopped moderating error = %v", err)
	}
}

/*
TestOnlySomebodyWhoCouldHoldTheRoomIsNamed applies the owner's rule to
moderators, announces only a change, and ends a moderator's role with their
place.
*/
func TestOnlySomebodyWhoCouldHoldTheRoomIsNamed(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	ana := setup.local(t, setup.first, "ana")
	bruno := setup.local(t, setup.first, "bruno")
	outside := setup.local(t, setup.first, "carla")
	visitor := setup.newPerson(t, setup.first, "somebody from elsewhere")

	room := setup.opens(t, setup.first, ana, "Standup")
	setup.join(t, setup.first, room.ID, bruno)
	setup.join(t, setup.first, room.ID, visitor)
	owner := setup.asPerson(setup.first, ana)

	for name, userID := range map[string]string{"a visitor": visitor, "somebody outside the room": outside} {
		if err := owner.NameModerator(ctx, room.ID, userID, true); !errors.Is(err, ErrUserNotFound) {
			t.Errorf("naming %s error = %v, want %v", name, err, ErrUserNotFound)
		}
	}
	var validation ValidationError
	if err := owner.NameModerator(ctx, room.ID, ana, true); !errors.As(err, &validation) {
		t.Errorf("an owner naming themselves error = %v, want a validation error", err)
	}

	for range 2 {
		setup.name(t, room.ID, ana, bruno)
	}
	for range 2 {
		if err := owner.NameModerator(ctx, room.ID, bruno, false); err != nil {
			t.Fatalf("unnaming bruno error = %v", err)
		}
	}
	setup.name(t, room.ID, ana, bruno)

	want := [][2]string{{bruno, "moderator"}, {bruno, "member"}, {bruno, "moderator"}}
	if got := setup.roleEvents(); !slices.Equal(got, want) {
		t.Errorf("announced %v, want %v", got, want)
	}
	for _, event := range setup.published.all() {
		if event.Type == events.MemberRoleChanged && (event.Subject.Type != events.SubjectRoom || event.Subject.ID != room.ID) {
			t.Errorf("a role change is about %s %q, want room %q", event.Subject.Type, event.Subject.ID, room.ID)
		}
	}

	if err := setup.asPerson(setup.first, bruno).Leave(ctx, room.ID); err != nil {
		t.Fatalf("Leave() error = %v", err)
	}
	setup.join(t, setup.first, room.ID, bruno)
	if moderating, err := setup.service.Moderating(ctx, setup.first, room.ID, bruno); err != nil || moderating {
		t.Errorf("after leaving and coming back, Moderating() = %v, %v, want a member", moderating, err)
	}
}

/*
TestAnOwnerHandsTheRoomOver covers a handover: the new owner stops being a
moderator, the old one stays as a member, and each is announced.
*/
func TestAnOwnerHandsTheRoomOver(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	ana := setup.local(t, setup.first, "ana")
	bruno := setup.local(t, setup.first, "bruno")
	visitor := setup.newPerson(t, setup.first, "somebody from elsewhere")

	room := setup.opens(t, setup.first, ana, "Standup")
	setup.join(t, setup.first, room.ID, bruno)
	setup.join(t, setup.first, room.ID, visitor)
	setup.name(t, room.ID, ana, bruno)

	owner := setup.asPerson(setup.first, ana)
	if _, err := owner.Transfer(ctx, room.ID, visitor); !errors.Is(err, ErrUserNotFound) {
		t.Errorf("handing the room to a visitor error = %v, want %v", err, ErrUserNotFound)
	}
	var validation ValidationError
	if _, err := owner.Transfer(ctx, room.ID, ana); !errors.As(err, &validation) {
		t.Errorf("handing the room to oneself error = %v, want a validation error", err)
	}

	handed, err := owner.Transfer(ctx, room.ID, bruno)
	if err != nil {
		t.Fatalf("Transfer() error = %v", err)
	}
	if handed.OwnerUserID != bruno {
		t.Errorf("Transfer() owner = %q, want bruno %q", handed.OwnerUserID, bruno)
	}
	if moderating, _ := setup.service.Moderating(ctx, setup.first, room.ID, bruno); moderating {
		t.Error("the new owner is still flagged a moderator")
	}
	if in, _ := setup.service.IsMember(ctx, setup.first, room.ID, ana); !in {
		t.Error("the old owner lost their place")
	}
	if _, err := owner.Rename(ctx, room.ID, "Still mine"); !errors.Is(err, ErrNotOwner) {
		t.Errorf("the old owner renaming error = %v, want %v", err, ErrNotOwner)
	}

	want := [][2]string{{bruno, "moderator"}, {bruno, "owner"}, {ana, "member"}}
	if got := setup.roleEvents(); !slices.Equal(got, want) {
		t.Errorf("announced %v, want %v", got, want)
	}
}

// TestTwoHandoversAtOnceCannotBothWin is why the owner is checked under the lock.
func TestTwoHandoversAtOnceCannotBothWin(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	ana := setup.local(t, setup.first, "ana")
	bruno := setup.local(t, setup.first, "bruno")
	carla := setup.local(t, setup.first, "carla")

	for attempt := range 10 {
		room := setup.opens(t, setup.first, ana, "Standup")
		setup.join(t, setup.first, room.ID, bruno)
		setup.join(t, setup.first, room.ID, carla)

		var group sync.WaitGroup
		var mutex sync.Mutex
		won := 0
		for _, to := range []string{bruno, carla} {
			group.Go(func() {
				err := setup.service.TransferOwner(ctx, setup.first, room.ID, ana, to)
				if err != nil && !errors.Is(err, ErrNotOwner) {
					t.Errorf("TransferOwner() error = %v", err)
				}
				if err == nil {
					mutex.Lock()
					won++
					mutex.Unlock()
				}
			})
		}
		group.Wait()

		if won != 1 {
			t.Fatalf("attempt %d: %d handovers succeeded, want 1", attempt, won)
		}
	}
}

// TestAModeratorIsTheFirstToTakeARoomOver extends succession: somebody the owner
// trusted comes before somebody who has only been there longer.
func TestAModeratorIsTheFirstToTakeARoomOver(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	ana := setup.local(t, setup.first, "ana")
	bruno := setup.local(t, setup.first, "bruno")
	carla := setup.local(t, setup.first, "carla")

	room := setup.opens(t, setup.first, ana, "Standup")
	setup.join(t, setup.first, room.ID, bruno)
	setup.join(t, setup.first, room.ID, carla)
	setup.name(t, room.ID, ana, carla)

	if err := setup.asPerson(setup.first, ana).Leave(ctx, room.ID); err != nil {
		t.Fatalf("Leave() error = %v", err)
	}
	if owner := setup.ownerOf(t, setup.first, room.ID); owner != carla {
		t.Errorf("after the owner left, the owner is %q, want the moderator carla %q", owner, carla)
	}
	if moderating, _ := setup.service.Moderating(ctx, setup.first, room.ID, carla); moderating {
		t.Error("the moderator who took the room over is still flagged a moderator")
	}
}
