package rooms

import (
	"context"
	"crypto/rand"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"

	"convia/internal/applications"
	"convia/internal/database"
)

// signsInHere gives somebody an account on this installation, which is what lets
// them hold a room.
func (setup fixture) signsInHere(t *testing.T, userID, username string) {
	t.Helper()

	const statement = `INSERT INTO accounts
	                   (id, username, password_digest, public_key, sealed_private_key, user_id, status,
	                    created_at, updated_at)
	                   VALUES ($1, $2, 'not a digest', $3, 'not a sealed key', $4, 'active', now(), now())`
	if _, err := setup.pool.Exec(context.Background(), statement,
		"acc_"+rand.Text(), username, make([]byte, 32), userID); err != nil {
		t.Fatalf("give %s an account: %v", username, err)
	}
}

// local makes a person who signs in here.
func (setup fixture) local(t *testing.T, applicationID, name string) string {
	t.Helper()

	person := setup.newPerson(t, applicationID, name)
	setup.signsInHere(t, person, name)
	return person
}

// opens has somebody open a room, which makes them its owner.
func (setup fixture) opens(t *testing.T, applicationID, userID, name string) Room {
	t.Helper()

	room, err := setup.asPerson(applicationID, userID).Create(context.Background(), name)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	return room
}

func (setup fixture) ownerOf(t *testing.T, applicationID, roomID string) string {
	t.Helper()

	room, err := setup.service.Get(context.Background(), applicationID, roomID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	return room.OwnerUserID
}

func TestWhoeverOpensARoomOwnsIt(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	ana := setup.local(t, setup.first, "ana")
	bruno := setup.local(t, setup.first, "bruno")

	room := setup.opens(t, setup.first, ana, "Weekend plans")
	if !room.Personal || room.OwnerUserID != ana {
		t.Fatalf("Create() = personal %v owned by %q, want a room ana owns", room.Personal, room.OwnerUserID)
	}
	setup.join(t, setup.first, room.ID, bruno)

	page, err := setup.asPerson(setup.first, bruno).Members(ctx, room.ID, MembershipOptions{Limit: 10})
	if err != nil {
		t.Fatalf("Members() error = %v", err)
	}

	roles := map[string]Role{}
	for _, person := range page.People {
		roles[person.UserID] = person.Role
	}
	if roles[ana] != RoleOwner || roles[bruno] != RoleMember {
		t.Errorf("roles = %v, want ana the owner and bruno a member", roles)
	}
}

/*
TestAnOwnerWhoGoesPassesTheRoomOn covers every way an owner can go, and who
takes the room each time.

The visitor joined before everybody but the owner and is still passed over:
somebody from another installation never holds a room here. Once only the
visitor is left the room has no owner, and the next person who signs in here and
is added takes it.
*/
func TestAnOwnerWhoGoesPassesTheRoomOn(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	ana := setup.local(t, setup.first, "ana")
	visitor := setup.newPerson(t, setup.first, "somebody from elsewhere")
	bruno := setup.local(t, setup.first, "bruno")
	carla := setup.local(t, setup.first, "carla")

	room := setup.opens(t, setup.first, ana, "Standup")
	for _, person := range []string{visitor, bruno, carla} {
		setup.join(t, setup.first, room.ID, person)
	}

	if err := setup.asPerson(setup.first, ana).Leave(ctx, room.ID); err != nil {
		t.Fatalf("Leave() error = %v", err)
	}
	if owner := setup.ownerOf(t, setup.first, room.ID); owner != bruno {
		t.Errorf("after the owner left, the owner is %q, want bruno %q", owner, bruno)
	}

	if _, err := setup.service.RemoveMember(ctx, setup.first, room.ID, bruno); err != nil {
		t.Fatalf("RemoveMember() error = %v", err)
	}
	if owner := setup.ownerOf(t, setup.first, room.ID); owner != carla {
		t.Errorf("after the application removed the owner, the owner is %q, want carla %q", owner, carla)
	}

	if _, err := setup.service.ForgetMemberships(ctx, setup.first, carla); err != nil {
		t.Fatalf("ForgetMemberships() error = %v", err)
	}
	if owner := setup.ownerOf(t, setup.first, room.ID); owner != "" {
		t.Errorf("with only a visitor left, the owner is %q, want nobody", owner)
	}

	dora := setup.local(t, setup.first, "dora")
	setup.join(t, setup.first, room.ID, dora)
	if owner := setup.ownerOf(t, setup.first, room.ID); owner != dora {
		t.Errorf("after somebody who signs in here was added, the owner is %q, want dora %q", owner, dora)
	}
}

/*
TestOnlyTheOwnerModeratesARoom tries every act of an owner as a member, who is
told it is the owner's, and as somebody outside the room, who is told the room
is not there. Then the owner does them.
*/
func TestOnlyTheOwnerModeratesARoom(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	ana := setup.local(t, setup.first, "ana")
	bruno := setup.local(t, setup.first, "bruno")
	carla := setup.local(t, setup.first, "carla")

	room := setup.opens(t, setup.first, ana, "Standup")
	setup.join(t, setup.first, room.ID, bruno)

	acts := map[string]func(*Personal) error{
		"remove": func(person *Personal) error { return person.Remove(ctx, room.ID, ana) },
		"ban":    func(person *Personal) error { return person.Ban(ctx, room.ID, ana) },
		"unban":  func(person *Personal) error { return person.Unban(ctx, room.ID, ana) },
		"bans": func(person *Personal) error {
			_, err := person.Bans(ctx, room.ID, MembershipOptions{Limit: 10})
			return err
		},
		"rename": func(person *Personal) error {
			_, err := person.Rename(ctx, room.ID, "Taken over")
			return err
		},
		"close": func(person *Personal) error {
			_, err := person.Close(ctx, room.ID)
			return err
		},
		"reopen": func(person *Personal) error {
			_, err := person.Reopen(ctx, room.ID)
			return err
		},
		"delete": func(person *Personal) error { return person.Delete(ctx, room.ID) },
	}
	for name, act := range acts {
		if err := act(setup.asPerson(setup.first, bruno)); !errors.Is(err, ErrNotOwner) {
			t.Errorf("%s by a member error = %v, want %v", name, err, ErrNotOwner)
		}
		if err := act(setup.asPerson(setup.first, carla)); !errors.Is(err, ErrNotFound) {
			t.Errorf("%s by somebody outside the room error = %v, want %v", name, err, ErrNotFound)
		}
	}

	owner := setup.asPerson(setup.first, ana)
	var validation ValidationError
	if err := owner.Remove(ctx, room.ID, ana); !errors.As(err, &validation) {
		t.Errorf("an owner removing themselves error = %v, want a validation error", err)
	}
	if err := owner.Ban(ctx, room.ID, ana); !errors.As(err, &validation) {
		t.Errorf("an owner banning themselves error = %v, want a validation error", err)
	}

	if renamed, err := owner.Rename(ctx, room.ID, "Weekly standup"); err != nil || renamed.Name != "Weekly standup" {
		t.Errorf("Rename() = %q, %v, want the new name", renamed.Name, err)
	}
	if closed, err := owner.Close(ctx, room.ID); err != nil || closed.Status != StatusClosed {
		t.Errorf("Close() = %s, %v, want closed", closed.Status, err)
	}
	if reopened, err := owner.Reopen(ctx, room.ID); err != nil || reopened.Status != StatusOpen {
		t.Errorf("Reopen() = %s, %v, want open", reopened.Status, err)
	}
	if err := owner.Remove(ctx, room.ID, bruno); err != nil {
		t.Errorf("Remove() error = %v", err)
	}
	if in, _ := setup.service.IsMember(ctx, setup.first, room.ID, bruno); in {
		t.Error("the owner removed bruno and he is still in the room")
	}
	if err := owner.Delete(ctx, room.ID); err != nil {
		t.Errorf("Delete() error = %v", err)
	}
	if deleted, _ := setup.service.Get(ctx, setup.first, room.ID); deleted.Status != StatusDeleted {
		t.Errorf("after Delete() the room is %s, want deleted", deleted.Status)
	}
}

/*
TestRemovalCanBeUndoneByAnybodyAndABanOnlyByTheOwner is the difference between
the two that the product owner asked for.

Carla shares another room with bruno, so she can name him: the only thing
standing between him and the first room is the ban.
*/
func TestRemovalCanBeUndoneByAnybodyAndABanOnlyByTheOwner(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	ana := setup.local(t, setup.first, "ana")
	bruno := setup.local(t, setup.first, "bruno")
	carla := setup.local(t, setup.first, "carla")

	room := setup.opens(t, setup.first, ana, "Standup")
	elsewhere := setup.opens(t, setup.first, carla, "Lunch")
	setup.join(t, setup.first, room.ID, bruno)
	setup.join(t, setup.first, room.ID, carla)
	setup.join(t, setup.first, elsewhere.ID, bruno)
	setup.join(t, setup.first, elsewhere.ID, ana)

	owner := setup.asPerson(setup.first, ana)
	member := setup.asPerson(setup.first, carla)

	if err := owner.Remove(ctx, room.ID, bruno); err != nil {
		t.Fatalf("Remove() error = %v", err)
	}
	if _, added, err := member.Add(ctx, room.ID, bruno); err != nil || !added {
		t.Fatalf("a member bringing back somebody removed = added %v, %v, want them back", added, err)
	}

	if err := owner.Ban(ctx, room.ID, bruno); err != nil {
		t.Fatalf("Ban() error = %v", err)
	}
	if in, _ := setup.service.IsMember(ctx, setup.first, room.ID, bruno); in {
		t.Fatal("the ban left bruno in the room")
	}
	if _, _, err := member.Add(ctx, room.ID, bruno); !errors.Is(err, ErrUserNotFound) {
		t.Errorf("a member adding somebody banned error = %v, want %v", err, ErrUserNotFound)
	}
	if _, _, err := owner.Add(ctx, room.ID, bruno); !errors.Is(err, ErrUserNotFound) {
		t.Errorf("the owner adding somebody still banned error = %v, want %v", err, ErrUserNotFound)
	}

	banned, err := owner.Bans(ctx, room.ID, MembershipOptions{Limit: 10})
	if err != nil || len(banned.People) != 1 || banned.People[0].UserID != bruno {
		t.Errorf("Bans() = %+v, %v, want bruno", banned.People, err)
	}

	if err := owner.Unban(ctx, room.ID, bruno); err != nil {
		t.Fatalf("Unban() error = %v", err)
	}
	if in, _ := setup.service.IsMember(ctx, setup.first, room.ID, bruno); in {
		t.Error("lifting the ban gave bruno his place back by itself")
	}
	if _, added, err := member.Add(ctx, room.ID, bruno); err != nil || !added {
		t.Errorf("a member adding somebody whose ban was lifted = added %v, %v, want them back", added, err)
	}
}

/*
TestTheApplicationIsNotBoundByABan keeps an application's authority over its own
rooms what it was: a ban is a room owner's decision about people, not a limit on
the application.
*/
func TestTheApplicationIsNotBoundByABan(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	ana := setup.local(t, setup.first, "ana")
	bruno := setup.local(t, setup.first, "bruno")
	room := setup.opens(t, setup.first, ana, "Standup")
	setup.join(t, setup.first, room.ID, bruno)

	if _, err := setup.service.Ban(ctx, setup.first, room.ID, bruno); err != nil {
		t.Fatalf("Ban() error = %v", err)
	}
	if _, added, err := setup.service.AddMember(ctx, setup.first, room.ID, bruno); err != nil || !added {
		t.Errorf("the application adding somebody banned = added %v, %v, want them added", added, err)
	}
}

/*
TestABanAndAnAdditionRacingEachOtherCannotBothWin is why both take the room's
lock. Without it an addition can check for a ban before the ban commits and
insert the place after the ban removed it, leaving somebody banned and in the
room at once.
*/
func TestABanAndAnAdditionRacingEachOtherCannotBothWin(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	ana := setup.local(t, setup.first, "ana")
	bruno := setup.local(t, setup.first, "bruno")

	for attempt := range 20 {
		room := setup.opens(t, setup.first, ana, "Standup")

		var group sync.WaitGroup
		group.Add(2)
		go func() {
			defer group.Done()
			if _, err := setup.service.Ban(ctx, setup.first, room.ID, bruno); err != nil {
				t.Errorf("Ban() error = %v", err)
			}
		}()
		go func() {
			defer group.Done()
			_, _, err := setup.service.AddUnlessBanned(ctx, setup.first, room.ID, bruno)
			if err != nil && !errors.Is(err, ErrBanned) {
				t.Errorf("AddUnlessBanned() error = %v", err)
			}
		}()
		group.Wait()

		if in, _ := setup.service.IsMember(ctx, setup.first, room.ID, bruno); in {
			t.Fatalf("attempt %d: bruno is banned and in the room", attempt)
		}
	}
}

// TestAnApplicationsRoomHasNoOwner leaves moderating a room an application made
// to the application, whoever is in it.
func TestAnApplicationsRoomHasNoOwner(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	room := setup.newRoom(t, setup.first, "Standup")
	ana := setup.local(t, setup.first, "ana")
	setup.join(t, setup.first, room.ID, ana)

	stored, err := setup.service.Get(ctx, setup.first, room.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if stored.Personal || stored.OwnerUserID != "" {
		t.Errorf("an application's room = personal %v owned by %q, want no owner", stored.Personal, stored.OwnerUserID)
	}
	if _, err := setup.asPerson(setup.first, ana).Rename(ctx, room.ID, "Mine now"); !errors.Is(err, ErrNotOwner) {
		t.Errorf("renaming an application's room error = %v, want %v", err, ErrNotOwner)
	}
}

/*
TestExistingRoomsAreGivenTheOwnerTheRuleWouldHaveGiven runs `00021` over rooms
that existed before it, by reverting it and applying it again, and expects the
owners the rule for an owner who goes would have produced.
*/
func TestExistingRoomsAreGivenTheOwnerTheRuleWouldHaveGiven(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	if err := setup.applications.EnsureFirstParty(ctx); err != nil {
		t.Fatalf("EnsureFirstParty() error = %v", err)
	}
	product := applications.FirstPartyID

	ana := setup.local(t, product, "ana")
	bruno := setup.local(t, product, "bruno")
	carla := setup.local(t, product, "carla")
	visitor := setup.newPerson(t, product, "somebody from elsewhere")

	kept := setup.opens(t, product, ana, "Kept")

	passedOn := setup.opens(t, product, bruno, "Passed on")
	setup.join(t, product, passedOn.ID, visitor)
	setup.join(t, product, passedOn.ID, carla)
	if err := setup.asPerson(product, bruno).Leave(ctx, passedOn.ID); err != nil {
		t.Fatalf("Leave() error = %v", err)
	}

	abandoned := setup.opens(t, product, ana, "Abandoned")
	setup.join(t, product, abandoned.ID, visitor)
	if err := setup.asPerson(product, ana).Leave(ctx, abandoned.ID); err != nil {
		t.Fatalf("Leave() error = %v", err)
	}

	applications, err := setup.service.Create(ctx, product, Definition{Alias: "standup", Name: "Standup"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	setup.join(t, product, applications.ID, ana)

	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := database.Rollback(ctx, setup.databaseURL, quiet); err != nil {
		t.Fatalf("Rollback() error = %v", err)
	}
	if err := database.Migrate(ctx, setup.databaseURL, quiet); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	// The pool's cached statements describe the columns before the migration.
	setup.pool.Reset()

	for name, want := range map[string]struct {
		room     Room
		personal bool
		owner    string
	}{
		"a room whose opener is still in it":         {kept, true, ana},
		"a room whose opener left":                   {passedOn, true, carla},
		"a room with only a visitor left":            {abandoned, true, ""},
		"a room the application made, with an alias": {applications, false, ""},
	} {
		stored, err := setup.service.Get(ctx, product, want.room.ID)
		if err != nil {
			t.Fatalf("%s: Get() error = %v", name, err)
		}
		if stored.Personal != want.personal || stored.OwnerUserID != want.owner {
			t.Errorf("%s = personal %v owned by %q, want personal %v owned by %q",
				name, stored.Personal, stored.OwnerUserID, want.personal, want.owner)
		}
	}
}
