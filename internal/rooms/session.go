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
	CreateFor(ctx context.Context, applicationID, userID string, definition Definition) (Room, error)
	IsMember(ctx context.Context, applicationID, roomID, userID string) (bool, error)
	Members(ctx context.Context, applicationID, roomID string, options MembershipOptions) (Membership, error)
	AddMember(ctx context.Context, applicationID, roomID, userID string) (Member, bool, error)
	RemoveMember(ctx context.Context, applicationID, roomID, userID string) (bool, error)
	SharesRoom(ctx context.Context, applicationID, userID, otherID string) (bool, error)
	Acquaintances(ctx context.Context, applicationID, userID string, options MembershipOptions) (Acquaintances, error)
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

A person may do four things to rooms, and the list is the design. **They may
open a room**, and they are in it from the moment it exists. **They may add
somebody** — to a room they are in, and only somebody they already share a room
with. **They may leave.** And **they may see** who is in a room with them, and
who they could add.

What is not here is deliberate.

**No removing anybody else.** Membership carries no role, so there is no owner
whose authority such a power could rest on, and inventing one here would be
deciding moderation by accident — which M32-004 names as the part of this area
that is dangerous to guess at.

**No lookup by address.** One person names another only through a room they
already share. A lookup that confirmed whether an address has an account would
be an enumeration oracle, which the sign-in surface already refuses to be
(M32-002). Sharing a room is the consent: somebody already decided those two
people belong in one place.

**No renaming, closing, or deleting.** Without a role there is no good answer to
who may, and a room is named when it is opened. Those stay with the application,
where scopes exist.
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

// Person is somebody a signed-in person can see, by the name they go by.
type Person struct {
	UserID      string
	DisplayName string
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

// Members returns one page of who is in a room this person is in.
func (personal *Personal) Members(ctx context.Context, roomID string, options MembershipOptions) (People, error) {
	if err := personal.requireMembership(ctx, roomID); err != nil {
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
	return personal.name(ctx, identifiers, membership.NextCursor)
}

/*
Add gives somebody a place in a room this person is in.

**Somebody who cannot be added is not there**, and every reason is the same
answer: an identifier that names nobody, a stranger this person shares no room
with, and somebody who shares a room but is suspended. Distinguishing the first
two would make this an oracle for which identifiers exist, and distinguishing
the third would tell one person about another's suspension.

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

	member, added, err := personal.service.AddMember(ctx, personal.principal.ApplicationID, roomID, userID)
	if errors.Is(err, ErrUserUnavailable) {
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
