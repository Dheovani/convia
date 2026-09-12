package rooms

import (
	"context"
	"errors"
	"fmt"

	"convia/internal/api"
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
*/
func (service *Service) AddMember(ctx context.Context, applicationID, roomID, userID string) (Member, bool, error) {
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
	})
	if err != nil {
		return Member{}, false, err
	}

	// Only a change is recorded. An application reconciling its own list against
	// Convia's would otherwise fill the audit trail with events where nothing
	// happened, which is how a trail stops being read.
	if added {
		service.recordMembership(ctx, "room.member_added", member)
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
		service.recordMembership(ctx, "room.member_removed", Member{
			ApplicationID: applicationID, RoomID: room.ID, UserID: userID})
	}
	return removed, nil
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

// recordMembership writes the audit entry for a change to who belongs where.
func (service *Service) recordMembership(ctx context.Context, event string, member Member) {
	service.logger.InfoContext(ctx, event,
		"application_id", member.ApplicationID,
		"room_id", member.RoomID,
		"user_id", member.UserID,
		"request_id", api.RequestIDFromContext(ctx),
	)
}
