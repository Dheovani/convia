package rooms

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"convia/internal/api"
	"convia/internal/events"
	"convia/internal/users"
)

/*
AddMember gives one of an application's people a place in one of its rooms.

The room is resolved first and the person second, so a request naming both
wrongly is told about the room — which is the half the caller can check without
knowing anything about Convia's identity model.

**A closed room still accepts members.** Closing stops new calls and new
messages; it does not evict the people who were there, and adding somebody to a
finished room so that they can read its history is a reasonable thing to want.
A deleted room is refused, because it is gone from the API.

**Bans are not consulted.** A ban is a room owner's decision about who belongs in
the room, and an application acting on its own rooms keeps the authority it has
always had. People add somebody through AddUnlessBanned.
*/
func (service *Service) AddMember(ctx context.Context, applicationID, roomID, userID string) (Member, bool, error) {
	return service.addMember(ctx, applicationID, roomID, userID, false)
}

/*
AddUnlessBanned gives somebody a place unless the room's owner has banned them,
which is ErrBanned.

It is how a person adds somebody, and how somebody accepting an invitation is
admitted: every way into a room that does not go through the application.
*/
func (service *Service) AddUnlessBanned(ctx context.Context, applicationID, roomID, userID string) (Member, bool, error) {
	return service.addMember(ctx, applicationID, roomID, userID, true)
}

func (service *Service) addMember(
	ctx context.Context,
	applicationID,
	roomID,
	userID string,
	honorBans bool,
) (Member, bool, error) {
	room, err := service.requireRoomForMembership(ctx, applicationID, roomID)
	if err != nil {
		return Member{}, false, err
	}

	if err := service.requirePerson(ctx, applicationID, userID); err != nil {
		return Member{}, false, err
	}

	member, added, err := service.store.AddMember(ctx, Member{
		ApplicationID: applicationID,
		RoomID:        room.ID,
		UserID:        userID,
		CreatedAt:     service.now(),
	}, honorBans)
	if err != nil {
		return Member{}, false, err
	}

	// Only a change is recorded. An application reconciling its own list against
	// Convia's would otherwise fill the audit trail with events where nothing
	// happened, which is how a trail stops being read.
	if added {
		service.announceMembership(ctx, events.MemberAdded, member)
	}

	return member, added, nil
}

/*
RemoveMember takes somebody's place away, reporting whether they had one.

Removing somebody who was not there is not an error: the caller asked for a
state the room is already in, and a retried request must not look like a
mistake.

Nothing happens to what they said. The messages are the room's record of a
conversation that did happen, and withdrawing them would rewrite it for
everybody still there.
*/
func (service *Service) RemoveMember(ctx context.Context, applicationID, roomID,
	userID string) (bool, error) {
	room, err := service.requireRoomForMembership(ctx, applicationID, roomID)
	if err != nil {
		return false, err
	}

	removed, err := service.store.RemoveMember(ctx, applicationID, room.ID, userID)
	if err != nil {
		return false, err
	}

	if removed {
		service.announceMembership(ctx, events.MemberRemoved, Member{
			ApplicationID: applicationID, RoomID: room.ID, UserID: userID})
		service.memberGone(ctx, applicationID, room.ID, userID)
	}
	return removed, nil
}

/*
memberGone tells the call a room is holding that somebody no longer has a place
in the room. What that means for the call is the call's to decide; see
conversations.
*/
func (service *Service) memberGone(ctx context.Context, applicationID, roomID, userID string) {
	if service.calls != nil {
		service.calls.MemberGone(ctx, applicationID, roomID, userID)
	}
}

/*
IsMember reports whether somebody may act in a room as themselves.

It is the question the session surface asks on every request, so it is one
statement against the primary key and it resolves nothing else. In particular it
does **not** check the room's lifecycle: a closed room is still readable by the
people who were in it, and whether a closed room accepts new messages is the
messages domain's rule rather than this one's.
*/
func (service *Service) IsMember(ctx context.Context, applicationID, roomID, userID string) (bool, error) {
	if !ValidID(roomID) {
		return false, nil
	}

	_, err := service.store.Member(ctx, applicationID, roomID, userID)
	if errors.Is(err, ErrNotAMember) {
		return false, nil
	}

	if err != nil {
		return false, err
	}

	return true, nil
}

/*
Ban keeps somebody out of a room and takes their place if they had one,
reporting whether they did.

Banning is repeatable and so is lifting it, and only a change is recorded. A ban
removes the person exactly as leaving does — what they said stays — and is
announced as the same room.member_removed, because membership records no actor.
*/
func (service *Service) Ban(ctx context.Context, applicationID, roomID, userID string) (bool, error) {
	room, err := service.requireRoomForMembership(ctx, applicationID, roomID)
	if err != nil {
		return false, err
	}

	if _, err := service.people.Get(ctx, applicationID, userID); errors.Is(err, users.ErrNotFound) {
		return false, ErrUserNotFound
	} else if err != nil {
		return false, fmt.Errorf("read the person: %w", err)
	}

	banned, removed, err := service.store.Ban(ctx, Ban{
		ApplicationID: applicationID,
		RoomID:        room.ID,
		UserID:        userID,
		CreatedAt:     service.now(),
	})
	if err != nil {
		return false, err
	}

	if banned {
		service.logger.InfoContext(ctx, "room.member_banned",
			"application_id", applicationID,
			"room_id", room.ID,
			"user_id", userID,
			"request_id", api.RequestIDFromContext(ctx),
		)
	}
	if removed {
		service.announceMembership(ctx, events.MemberRemoved, Member{
			ApplicationID: applicationID, RoomID: room.ID, UserID: userID})
		service.memberGone(ctx, applicationID, room.ID, userID)
	}
	return removed, nil
}

// Unban lifts a ban, reporting whether there was one. It gives no place back:
// somebody still has to add the person.
func (service *Service) Unban(ctx context.Context, applicationID, roomID, userID string) (bool, error) {
	room, err := service.requireRoomForMembership(ctx, applicationID, roomID)
	if err != nil {
		return false, err
	}

	lifted, err := service.store.Unban(ctx, applicationID, room.ID, userID)
	if err != nil {
		return false, err
	}

	if lifted {
		service.logger.InfoContext(ctx, "room.member_unbanned",
			"application_id", applicationID,
			"room_id", room.ID,
			"user_id", userID,
			"request_id", api.RequestIDFromContext(ctx),
		)
	}
	return lifted, nil
}

// IsBanned reports whether somebody is kept out of a room. An identifier that
// could not name either is simply not banned.
func (service *Service) IsBanned(ctx context.Context, applicationID, roomID, userID string) (bool, error) {
	if !ValidID(roomID) || !users.ValidID(userID) {
		return false, nil
	}
	return service.store.Banned(ctx, applicationID, roomID, userID)
}

// Bans returns one page of the people kept out of a room.
func (service *Service) Bans(ctx context.Context, applicationID, roomID string, options MembershipOptions) (Bans, error) {
	room, err := service.requireRoomForMembership(ctx, applicationID, roomID)
	if err != nil {
		return Bans{}, err
	}

	limit, err := pageSize(options.Limit)
	if err != nil {
		return Bans{}, err
	}

	page, more, err := service.store.Bans(ctx, applicationID, room.ID, options.Cursor, limit)
	if err != nil {
		return Bans{}, err
	}

	result := Bans{Bans: page}
	if more && len(page) > 0 {
		result.NextCursor = page[len(page)-1].UserID
	}
	return result, nil
}

// MembershipOptions selects one page of a membership listing.
type MembershipOptions struct {
	Limit  int
	Cursor string
}

// Members returns one page of who belongs to a room.
func (service *Service) Members(ctx context.Context, applicationID, roomID string,
	options MembershipOptions) (Membership, error) {
	room, err := service.requireRoomForMembership(ctx, applicationID, roomID)
	if err != nil {
		return Membership{}, err
	}

	limit, err := pageSize(options.Limit)
	if err != nil {
		return Membership{}, err
	}

	page, more, err := service.store.Members(ctx, applicationID, room.ID, options.Cursor, limit)
	if err != nil {
		return Membership{}, err
	}
	return membership(page, more, func(member Member) string { return member.UserID }), nil
}

/*
RoomsOf returns one page of the rooms somebody belongs to.

This is the sidebar. The person is checked before the listing so that an
identifier naming nobody is reported rather than answered with an empty page,
which a caller would read as "they are in no rooms".
*/
func (service *Service) RoomsOf(ctx context.Context, applicationID, userID string,
	options MembershipOptions) (Membership, error) {
	if err := service.requireApplication(ctx, applicationID); err != nil {
		return Membership{}, err
	}

	if err := service.requirePerson(ctx, applicationID, userID); err != nil {
		return Membership{}, err
	}

	limit, err := pageSize(options.Limit)
	if err != nil {
		return Membership{}, err
	}

	page, more, err := service.store.RoomsOf(ctx, applicationID, userID, options.Cursor, limit)
	if err != nil {
		return Membership{}, err
	}
	return membership(page, more, func(member Member) string { return member.RoomID }), nil
}

/*
ForgetMemberships removes every place one person held, for erasure.

It is separate from RemoveMember because it is a different act: removal is the
application deciding somebody no longer belongs somewhere, and this is Convia
stopping holding that they ever did.
*/
func (service *Service) ForgetMemberships(ctx context.Context, applicationID, userID string) (int64, error) {
	if err := service.requireApplication(ctx, applicationID); err != nil {
		return 0, err
	}

	forgotten, err := service.store.ForgetMemberships(ctx, applicationID, userID)
	if err != nil {
		return 0, err
	}

	service.logger.InfoContext(ctx, "room.memberships_forgotten",
		"application_id", applicationID,
		"user_id", userID,
		"rooms", forgotten,
		"request_id", api.RequestIDFromContext(ctx),
	)
	return forgotten, nil
}

/*
membership renders a page and the cursor to continue it from.

The cursor is the last identifier on the page rather than an encoded pair,
because the ordering is the identifier: these listings are ordered by the column
the index already holds, so there is nothing else for a cursor to carry.
*/
func membership(page []Member, more bool, key func(Member) string) Membership {
	result := Membership{Members: page}
	if more && len(page) > 0 {
		result.NextCursor = key(page[len(page)-1])
	}
	return result
}

/*
requireRoomForMembership resolves the room a membership is about.

A deleted room is reported as missing rather than as deleted, because it is gone
from the API and a caller must not learn that an identifier once named
something.
*/
func (service *Service) requireRoomForMembership(ctx context.Context, applicationID, roomID string) (Room, error) {
	room, err := service.Get(ctx, applicationID, roomID)
	if err != nil {
		return Room{}, err
	}

	if room.Status == StatusDeleted {
		return Room{}, ErrNotFound
	}

	return room, nil
}

/*
requirePerson checks that a place is being given to somebody real.

A suspended person is refused. Suspension withdraws access, and handing
somebody a room to write in while they are suspended would be the one place it
did not.
*/
func (service *Service) requirePerson(ctx context.Context, applicationID, userID string) error {
	person, err := service.people.Get(ctx, applicationID, userID)
	if errors.Is(err, users.ErrNotFound) {
		return ErrUserNotFound
	}

	if err != nil {
		return fmt.Errorf("read the person: %w", err)
	}

	if person.Status != users.StatusActive {
		return ErrUserUnavailable
	}

	return nil
}

/*
SetModerator makes a member of a room a moderator, or stops them being one, and
announces it when it changed. The rules are the store's; see Store.SetModerator.
*/
func (service *Service) SetModerator(ctx context.Context, applicationID, roomID, userID string,
	moderator bool) (bool, error) {
	if _, err := service.requireRoomForMembership(ctx, applicationID, roomID); err != nil {
		return false, err
	}
	changed, err := service.store.SetModerator(ctx, applicationID, roomID, userID, moderator)
	if err != nil || !changed {
		return changed, err
	}

	role := RoleMember
	if moderator {
		role = RoleModerator
	}
	service.announceRole(ctx, applicationID, roomID, userID, role)
	return true, nil
}

/*
TransferOwner hands a room to another member who could hold it, and announces
what each of the two now is.
*/
func (service *Service) TransferOwner(ctx context.Context, applicationID, roomID, from, to string) error {
	if _, err := service.requireRoomForMembership(ctx, applicationID, roomID); err != nil {
		return err
	}
	if err := service.store.TransferOwner(ctx, applicationID, roomID, from, to); err != nil {
		return err
	}

	fromForLog := strings.ReplaceAll(strings.ReplaceAll(from, "\n", ""), "\r", "")
	toForLog := strings.ReplaceAll(strings.ReplaceAll(to, "\n", ""), "\r", "")
	service.logger.InfoContext(ctx, "room.owner_transferred",
		"application_id", applicationID,
		"room_id", roomID,
		"from_user_id", fromForLog,
		"to_user_id", toForLog,
		"request_id", api.RequestIDFromContext(ctx),
	)
	service.announceRole(ctx, applicationID, roomID, to, RoleOwner)
	service.announceRole(ctx, applicationID, roomID, from, RoleMember)
	return nil
}

// Moderating reports whether somebody moderates a room without owning it.
func (service *Service) Moderating(ctx context.Context, applicationID, roomID, userID string) (bool, error) {
	member, err := service.store.Member(ctx, applicationID, roomID, userID)
	if errors.Is(err, ErrNotAMember) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return member.Moderator, nil
}

// announceRole tells whoever is listening what somebody now is in a room.
func (service *Service) announceRole(ctx context.Context, applicationID, roomID, userID string, role Role) {
	service.stream.Publish(ctx, events.New(events.MemberRoleChanged, applicationID, roomID,
		api.RequestIDFromContext(ctx), events.Data{"user_id": userID, "role": string(role)}))
}

/*
announceMembership records a change to who belongs where, and tells whoever is
listening.

Only a change is announced, for the reason only a change is audited. The event
names the room and the person and nothing else: membership records no actor, so
there is no truthful way to say whether somebody left or was removed, and the
room's name is the application's label rather than a value Convia assigned.

**Erasure announces nothing.** ForgetMemberships is Convia ceasing to hold that a
person was ever anywhere, and announcing each room they had been in would
broadcast exactly the record erasure exists to remove.
*/
func (service *Service) announceMembership(ctx context.Context, kind events.Type, member Member) {
	service.logger.InfoContext(ctx, string(kind),
		"application_id", member.ApplicationID,
		"room_id", member.RoomID,
		"user_id", member.UserID,
		"request_id", api.RequestIDFromContext(ctx),
	)

	service.stream.Publish(ctx, events.New(kind, member.ApplicationID, member.RoomID,
		api.RequestIDFromContext(ctx), events.Data{"user_id": member.UserID}))
}

/*
RoomIDsOf returns every room somebody belongs to, as identifiers alone.

It is what a person's event stream covers, read when the stream opens and again
while it stays open. It is not paged, because a stream needs the whole set and
paging it would be several reads that could disagree with each other.

It checks neither the application nor the person. Its one caller has just
verified a session, which already asked both questions, and a person Convia
stops serving is found by the stream's next check of that session rather than
here.
*/
func (service *Service) RoomIDsOf(ctx context.Context, applicationID, userID string) ([]string, error) {
	return service.store.RoomIDsOf(ctx, applicationID, userID)
}

/*
LocalNeighbours returns those of the candidates a person may see the presence of:
themselves, and anybody with an account here they share a room with. It checks
nothing else, for the reason RoomIDsOf gives.
*/
func (service *Service) LocalNeighbours(
	ctx context.Context,
	applicationID,
	userID string,
	candidates []string,
) ([]string, error) {
	return service.store.LocalNeighbours(ctx, applicationID, userID, candidates)
}

/*
Many returns several of an application's rooms, keyed by identifier.

It is the read the sidebar performs once instead of once per row. A room that
does not exist, belongs to another tenant, or is deleted is absent rather than
reported: the caller already knows which identifiers it asked about, and a
listing built from memberships has no use for an error about one of them.
*/
func (service *Service) Many(ctx context.Context, applicationID string, ids []string) (map[string]Room, error) {
	if err := service.requireApplication(ctx, applicationID); err != nil {
		return nil, err
	}

	found, err := service.store.Many(ctx, applicationID, ids)
	if err != nil {
		return nil, err
	}

	for id, room := range found {
		if room.Status == StatusDeleted {
			delete(found, id)
		}
	}
	return found, nil
}

/*
SharesRoom reports whether two of an application's people are in a room
together.

An identifier that could not name anybody answers false rather than an error,
because the question is only ever asked on behalf of a person, and a person
must not be able to tell a malformed identifier from a stranger.
*/
func (service *Service) SharesRoom(ctx context.Context, applicationID, userID, otherID string) (bool, error) {
	if !users.ValidID(otherID) {
		return false, nil
	}
	return service.store.SharesRoom(ctx, applicationID, userID, otherID)
}

// Acquaintances is one page of the people somebody shares a room with.
type Acquaintances struct {
	UserIDs    []string
	NextCursor string
}

// Acquaintances returns one page of the people somebody could add to a room.
// See Store.Acquaintances for who is left out and why.
func (service *Service) Acquaintances(ctx context.Context, applicationID, userID string,
	options MembershipOptions) (Acquaintances, error) {
	if err := service.requireApplication(ctx, applicationID); err != nil {
		return Acquaintances{}, err
	}

	limit, err := pageSize(options.Limit)
	if err != nil {
		return Acquaintances{}, err
	}

	identifiers, more, err := service.store.Acquaintances(ctx, applicationID, userID, options.Cursor, limit)
	if err != nil {
		return Acquaintances{}, err
	}

	page := Acquaintances{UserIDs: identifiers}
	if more && len(identifiers) > 0 {
		page.NextCursor = identifiers[len(identifiers)-1]
	}
	return page, nil
}
