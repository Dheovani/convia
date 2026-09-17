package rooms

import (
	"context"
	"errors"
	"fmt"

	"convia/internal/sessions"
	"convia/internal/users"
)

/*
personalService is what acting as one person needs from this package.

It is declared by its consumer, like every interface here, so that the session
handler can be tested without a database and the tenant surface's larger
interface is not dragged along with it.
*/
type personalService interface {
	Get(ctx context.Context, applicationID, id string) (Room, error)
	CreateFor(ctx context.Context, applicationID, userID string, definition Definition) (Room, error)
	Update(ctx context.Context, applicationID, id string, change Change, expectedVersion string) (Room, error)
	Close(ctx context.Context, applicationID, id string) (Room, error)
	Reopen(ctx context.Context, applicationID, id string) (Room, error)
	Delete(ctx context.Context, applicationID, id string) error
	IsMember(ctx context.Context, applicationID, roomID, userID string) (bool, error)
	Members(ctx context.Context, applicationID, roomID string, options MembershipOptions) (Membership, error)
	AddUnlessBanned(ctx context.Context, applicationID, roomID, userID string) (Member, bool, error)
	RemoveMember(ctx context.Context, applicationID, roomID, userID string) (bool, error)
	Ban(ctx context.Context, applicationID, roomID, userID string) (bool, error)
	Unban(ctx context.Context, applicationID, roomID, userID string) (bool, error)
	IsBanned(ctx context.Context, applicationID, roomID, userID string) (bool, error)
	Bans(ctx context.Context, applicationID, roomID string, options MembershipOptions) (Bans, error)
	SharesRoom(ctx context.Context, applicationID, userID, otherID string) (bool, error)
	Acquaintances(ctx context.Context, applicationID, userID string, options MembershipOptions) (Acquaintances, error)
	Moderating(ctx context.Context, applicationID, roomID, userID string) (bool, error)
	SetModerator(ctx context.Context, applicationID, roomID, userID string, moderator bool) (bool, error)
	TransferOwner(ctx context.Context, applicationID, roomID, from, to string) error
}

/*
directory is how a person learns what to call the people they can see.

Only reading, and only in bulk: every list on this surface names several people
at once, and resolving them one at a time is the query-per-row shape this
repository has already paid for once.
*/
type directory interface {
	Many(ctx context.Context, applicationID string, ids []string) (map[string]users.User, error)
}

/*
Personal is the rooms service acting as one signed-in person.

**Any person may open a room**, and they own the room they open. **Any member
may** add somebody they already share a room with, see who is in the room and
who they could add, and leave. **A moderator may also** remove somebody, ban
somebody so that nobody can bring them back until the ban is lifted, and lift a
ban — but not act on the owner or another moderator. **The owner may** do all of
that to anybody, rename, close, reopen or delete the room, name and unname
moderators, and hand the room to somebody else. docs/adr/0013 records why a room
has an owner, and docs/adr/0015 why it may have moderators.

What is not here is deliberate.

**No authority over an application's rooms.** A room an application created has
no owner, so removing people from it, and renaming or closing it, stay with the
application, where scopes exist.

**No lookup by address.** One person names another only through a room they
already share. A lookup that confirmed whether an address has an account would
be an enumeration oracle, which the sign-in surface already refuses to be
(M32-002). Sharing a room is the consent: somebody already decided those two
people belong in one place.

**No moderating from outside.** Everything the owner may do is refused to a
member as ErrNotOwner, what a moderator may do as ErrNotModerator, and both to
somebody outside the room as a room that is not there.
*/
type Personal struct {
	service   personalService
	directory directory
	principal sessions.Principal
}

/*
AsPerson binds the service to the authority of one verified session.

It does not produce a credentials.Principal, here or anywhere. See
docs/adr/0007: a session is a person, not a tenant's authority.
*/
func AsPerson(service personalService, directory directory, principal sessions.Principal) *Personal {
	return &Personal{service: service, directory: directory, principal: principal}
}

// Person is somebody a signed-in person can see, by the name they go by. Role
// is set only when they are listed as a member of a room.
type Person struct {
	UserID      string
	DisplayName string
	Role        Role
}

// errOwnerActsOnSelf refuses an owner or a moderator removing or banning
// themselves, which is leaving by another name.
var errOwnerActsOnSelf = ValidationError{
	Field:   "user_id",
	Message: "Leave the room rather than removing or banning yourself.",
}

// errOwnerNamesSelf refuses an owner making themselves a moderator or handing the
// room to themselves, which would change nothing.
var errOwnerNamesSelf = ValidationError{
	Field:   "user_id",
	Message: "The owner already does everything a moderator does.",
}

// People is one page of people.
type People struct {
	People     []Person
	NextCursor string
}

/*
Create opens a room with this person in it.

It takes a name and nothing else, and that is the rule rather than a
simplification. An alias is the application's own namespace, metadata is the
application's own data, and a capacity is a policy: a person setting any of them
would be deciding something on the application's behalf, and an alias in
particular would let one person squat a name the application meant to use.
*/
func (personal *Personal) Create(ctx context.Context, name string) (Room, error) {
	return personal.service.CreateFor(ctx, personal.principal.ApplicationID, personal.principal.UserID,
		Definition{Name: name})
}

// Members returns one page of who is in a room this person is in, each with
// their role in it.
func (personal *Personal) Members(ctx context.Context, roomID string, options MembershipOptions) (People, error) {
	if err := personal.requireMembership(ctx, roomID); err != nil {
		return People{}, err
	}

	room, err := personal.service.Get(ctx, personal.principal.ApplicationID, roomID)
	if err != nil {
		return People{}, err
	}

	membership, err := personal.service.Members(ctx, personal.principal.ApplicationID, roomID, options)
	if err != nil {
		return People{}, err
	}

	identifiers := make([]string, 0, len(membership.Members))
	for _, member := range membership.Members {
		identifiers = append(identifiers, member.UserID)
	}

	page, err := personal.name(ctx, identifiers, membership.NextCursor)
	if err != nil {
		return People{}, err
	}
	moderators := make(map[string]bool, len(membership.Members))
	for _, member := range membership.Members {
		moderators[member.UserID] = member.Moderator
	}
	for index := range page.People {
		switch person := page.People[index]; {
		case person.UserID == room.OwnerUserID:
			page.People[index].Role = RoleOwner
		case moderators[person.UserID]:
			page.People[index].Role = RoleModerator
		default:
			page.People[index].Role = RoleMember
		}
	}
	return page, nil
}

/*
Add gives somebody a place in a room this person is in.

**Somebody who cannot be added is not there**, and every reason is the same
answer: an identifier that names nobody, a stranger this person shares no room
with, somebody who shares a room but is suspended, and somebody the room's owner
has banned. Distinguishing the first two would make this an oracle for which
identifiers exist; distinguishing the others would tell one person about
another's suspension, or about a decision that is the owner's.

Adding yourself is not special. You are in the room or you would have been told
it is not there, so it answers as any repeated addition does.
*/
func (personal *Personal) Add(ctx context.Context, roomID, userID string) (Member, bool, error) {
	if err := personal.requireMembership(ctx, roomID); err != nil {
		return Member{}, false, err
	}

	shares, err := personal.service.SharesRoom(ctx, personal.principal.ApplicationID,
		personal.principal.UserID, userID)
	if err != nil {
		return Member{}, false, fmt.Errorf("check whether somebody can be named: %w", err)
	}
	if !shares {
		return Member{}, false, ErrUserNotFound
	}

	member, added, err := personal.service.AddUnlessBanned(ctx, personal.principal.ApplicationID, roomID, userID)
	if errors.Is(err, ErrUserUnavailable) || errors.Is(err, ErrBanned) {
		return Member{}, false, ErrUserNotFound
	}
	return member, added, err
}

/*
Leave takes this person's own place away.

It is always their own. There is no parameter naming whose place, so leaving on
somebody else's behalf is not refused here — it cannot be asked for.
*/
func (personal *Personal) Leave(ctx context.Context, roomID string) error {
	if err := personal.requireMembership(ctx, roomID); err != nil {
		return err
	}

	_, err := personal.service.RemoveMember(ctx, personal.principal.ApplicationID, roomID,
		personal.principal.UserID)
	return err
}

/*
Remove takes somebody else's place in a room this person owns.

It is not a ban: anybody in the room may add them back. What they said stays.
*/
func (personal *Personal) Remove(ctx context.Context, roomID, userID string) error {
	room, err := personal.requireActingOn(ctx, roomID, userID)
	if err != nil {
		return err
	}

	_, err = personal.service.RemoveMember(ctx, personal.principal.ApplicationID, room.ID, userID)
	return err
}

/*
Ban keeps somebody out of a room this person owns, taking their place if they
have one. Nobody can add them back, and no invitation admits them, until the
owner lifts it.

Who can be banned is who could be named at all: somebody in the room, somebody
already banned from it, or somebody the owner shares another room with. Anybody
else is the one answer Add gives, for the reason it gives it.
*/
func (personal *Personal) Ban(ctx context.Context, roomID, userID string) error {
	room, err := personal.requireActingOn(ctx, roomID, userID)
	if err != nil {
		return err
	}

	nameable, err := personal.nameable(ctx, room.ID, userID)
	if err != nil {
		return err
	}
	if !nameable {
		return ErrUserNotFound
	}

	_, err = personal.service.Ban(ctx, personal.principal.ApplicationID, room.ID, userID)
	return err
}

// Unban lifts a ban on a room this person moderates. It gives no place back.
func (personal *Personal) Unban(ctx context.Context, roomID, userID string) error {
	room, _, err := personal.requireModerator(ctx, roomID)
	if err != nil {
		return err
	}

	_, err = personal.service.Unban(ctx, personal.principal.ApplicationID, room.ID, userID)
	return err
}

// Bans returns one page of the people kept out of a room this person moderates.
func (personal *Personal) Bans(ctx context.Context, roomID string, options MembershipOptions) (People, error) {
	room, _, err := personal.requireModerator(ctx, roomID)
	if err != nil {
		return People{}, err
	}

	page, err := personal.service.Bans(ctx, personal.principal.ApplicationID, room.ID, options)
	if err != nil {
		return People{}, err
	}

	identifiers := make([]string, 0, len(page.Bans))
	for _, ban := range page.Bans {
		identifiers = append(identifiers, ban.UserID)
	}
	return personal.name(ctx, identifiers, page.NextCursor)
}

/*
Rename gives a room this person owns a new name, and changes nothing else: an
alias, metadata and a capacity stay the application's, as they are when a room
is opened.
*/
func (personal *Personal) Rename(ctx context.Context, roomID, name string) (Room, error) {
	room, err := personal.requireOwner(ctx, roomID)
	if err != nil {
		return Room{}, err
	}
	return personal.service.Update(ctx, personal.principal.ApplicationID, room.ID, Change{Name: &name}, "")
}

// Close stops a room this person owns taking anything new. What was said stays
// readable, and Reopen undoes it.
func (personal *Personal) Close(ctx context.Context, roomID string) (Room, error) {
	room, err := personal.requireOwner(ctx, roomID)
	if err != nil {
		return Room{}, err
	}
	return personal.service.Close(ctx, personal.principal.ApplicationID, room.ID)
}

// Reopen returns a room this person owns to use.
func (personal *Personal) Reopen(ctx context.Context, roomID string) (Room, error) {
	room, err := personal.requireOwner(ctx, roomID)
	if err != nil {
		return Room{}, err
	}
	return personal.service.Reopen(ctx, personal.principal.ApplicationID, room.ID)
}

/*
Delete removes a room this person owns, for everybody in it.

It is the same deletion an application's is: the room leaves every sidebar, and
the row is kept until erasure.
*/
func (personal *Personal) Delete(ctx context.Context, roomID string) error {
	room, err := personal.requireOwner(ctx, roomID)
	if err != nil {
		return err
	}
	return personal.service.Delete(ctx, personal.principal.ApplicationID, room.ID)
}

/*
NameModerator makes a member of a room this person owns one of its moderators,
or, with moderator false, stops them being one. Somebody who could not hold the
room — a visitor, or somebody not in it — is ErrUserNotFound.
*/
func (personal *Personal) NameModerator(ctx context.Context, roomID, userID string, moderator bool) error {
	room, err := personal.requireOwner(ctx, roomID)
	if err != nil {
		return err
	}
	if userID == personal.principal.UserID {
		return errOwnerNamesSelf
	}
	_, err = personal.service.SetModerator(ctx, personal.principal.ApplicationID, room.ID, userID, moderator)
	return err
}

/*
Transfer hands a room this person owns to another member who could hold it. They
stay in the room, as a member.
*/
func (personal *Personal) Transfer(ctx context.Context, roomID, userID string) (Room, error) {
	room, err := personal.requireOwner(ctx, roomID)
	if err != nil {
		return Room{}, err
	}
	if userID == personal.principal.UserID {
		return Room{}, errOwnerNamesSelf
	}
	if err := personal.service.TransferOwner(ctx, personal.principal.ApplicationID, room.ID,
		personal.principal.UserID, userID); err != nil {
		return Room{}, err
	}
	return personal.service.Get(ctx, personal.principal.ApplicationID, room.ID)
}

/*
requireModerator refuses anybody who neither owns nor moderates a room, and says
which of the two this person is.
*/
func (personal *Personal) requireModerator(ctx context.Context, roomID string) (Room, bool, error) {
	room, err := personal.requireOwner(ctx, roomID)
	if err == nil {
		return room, true, nil
	}
	if !errors.Is(err, ErrNotOwner) {
		return Room{}, false, err
	}

	moderating, err := personal.service.Moderating(ctx, personal.principal.ApplicationID, roomID,
		personal.principal.UserID)
	if err != nil {
		return Room{}, false, err
	}
	if !moderating {
		return Room{}, false, ErrNotModerator
	}
	room, err = personal.service.Get(ctx, personal.principal.ApplicationID, roomID)
	return room, false, err
}

/*
requireActingOn refuses anybody who may not remove or ban somebody in a room: an
owner may act on anybody but themselves, and a moderator only on a member who is
neither the owner nor another moderator.
*/
func (personal *Personal) requireActingOn(ctx context.Context, roomID, userID string) (Room, error) {
	room, owner, err := personal.requireModerator(ctx, roomID)
	if err != nil {
		return Room{}, err
	}
	if userID == personal.principal.UserID {
		return Room{}, errOwnerActsOnSelf
	}
	if owner {
		return room, nil
	}

	if userID == room.OwnerUserID {
		return Room{}, ErrNotOwner
	}
	moderating, err := personal.service.Moderating(ctx, personal.principal.ApplicationID, roomID, userID)
	if err != nil {
		return Room{}, err
	}
	if moderating {
		return Room{}, ErrNotOwner
	}
	return room, nil
}

// nameable reports whether an owner could name somebody in a ban.
func (personal *Personal) nameable(ctx context.Context, roomID, userID string) (bool, error) {
	if !users.ValidID(userID) {
		return false, nil
	}
	applicationID := personal.principal.ApplicationID

	member, err := personal.service.IsMember(ctx, applicationID, roomID, userID)
	if err != nil || member {
		return member, err
	}

	banned, err := personal.service.IsBanned(ctx, applicationID, roomID, userID)
	if err != nil || banned {
		return banned, err
	}

	shares, err := personal.service.SharesRoom(ctx, applicationID, personal.principal.UserID, userID)
	if err != nil {
		return false, fmt.Errorf("check whether somebody can be named: %w", err)
	}
	return shares, nil
}

/*
requireOwner refuses anybody but the owner of a room: somebody outside it is told
it is not there, and a member that the act is the owner's.
*/
func (personal *Personal) requireOwner(ctx context.Context, roomID string) (Room, error) {
	if err := personal.requireMembership(ctx, roomID); err != nil {
		return Room{}, err
	}

	room, err := personal.service.Get(ctx, personal.principal.ApplicationID, roomID)
	if err != nil {
		return Room{}, err
	}

	if room.Status == StatusDeleted {
		return Room{}, ErrNotFound
	}

	if room.OwnerUserID != personal.principal.UserID {
		return Room{}, ErrNotOwner
	}

	return room, nil
}

// People returns one page of the people this person could add to a room.
func (personal *Personal) People(ctx context.Context, options MembershipOptions) (People, error) {
	acquaintances, err := personal.service.Acquaintances(ctx, personal.principal.ApplicationID,
		personal.principal.UserID, options)
	if err != nil {
		return People{}, err
	}
	return personal.name(ctx, acquaintances.UserIDs, acquaintances.NextCursor)
}

/*
name resolves identifiers into the names a person reads, in one read.

Somebody whose user is gone is skipped rather than shown nameless. Users are
deleted softly and a membership can outlive one until erasure, so it is an
ordinary state, and a blank row is not something anybody can act on.
*/
func (personal *Personal) name(ctx context.Context, identifiers []string, cursor string) (People, error) {
	found, err := personal.directory.Many(ctx, personal.principal.ApplicationID, identifiers)
	if err != nil {
		return People{}, fmt.Errorf("read the people on the page: %w", err)
	}

	page := People{People: make([]Person, 0, len(identifiers)), NextCursor: cursor}
	for _, identifier := range identifiers {
		user, present := found[identifier]
		if !present {
			continue
		}
		page.People = append(page.People, Person{UserID: user.ID, DisplayName: user.DisplayName})
	}
	return page, nil
}

/*
requireMembership refuses a room this person is not in, as though it were not
there — ErrNotFound, never a forbidden, for the reason every route on this
surface gives.
*/
func (personal *Personal) requireMembership(ctx context.Context, roomID string) error {
	member, err := personal.service.IsMember(ctx, personal.principal.ApplicationID, roomID,
		personal.principal.UserID)
	if err != nil {
		return fmt.Errorf("check membership: %w", err)
	}
	if !member {
		return ErrNotFound
	}
	return nil
}
