package peers

import (
	"context"
	"errors"
	"testing"

	"convia/internal/accounts"
	"convia/internal/applications"
)

/*
TestABannedPersonIsNeitherInvitedNorAdmitted carries a room owner's ban across
installations.

Bia joins by a link, is removed, and is sent a second link while nothing stands
in her way. The owner then bans her: no new invitation can be made, and the link
already in her hands no longer admits her. Lifting the ban makes that link work
again, because a refused acceptance releases its claim.
*/
func TestABannedPersonIsNeitherInvitedNorAdmitted(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	bia, biaSigner := visitor(t)
	handle := accounts.Handle("bia", bia.ID())

	first, err := setup.service.Invite(ctx, setup.inviter, setup.room.ID, handle)
	if err != nil {
		t.Fatalf("Invite() error = %v", err)
	}
	accepted, err := setup.service.Accept(ctx, biaSigner, "bia", first.ID)
	if err != nil {
		t.Fatalf("Accept() error = %v", err)
	}

	if _, err := setup.rooms.RemoveMember(ctx, applications.FirstPartyID, setup.room.ID, accepted.UserID); err != nil {
		t.Fatalf("RemoveMember() error = %v", err)
	}
	second, err := setup.service.Invite(ctx, setup.inviter, setup.room.ID, handle)
	if err != nil {
		t.Fatalf("inviting somebody who was only removed error = %v", err)
	}

	if _, err := setup.rooms.Ban(ctx, applications.FirstPartyID, setup.room.ID, accepted.UserID); err != nil {
		t.Fatalf("Ban() error = %v", err)
	}

	if _, err := setup.service.Invite(ctx, setup.inviter, setup.room.ID, handle); !errors.Is(err, ErrBanned) {
		t.Errorf("inviting somebody banned error = %v, want %v", err, ErrBanned)
	}
	if _, err := setup.service.Accept(ctx, biaSigner, "bia", second.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("accepting a link after being banned error = %v, want %v", err, ErrNotFound)
	}
	if in, _ := setup.rooms.IsMember(ctx, applications.FirstPartyID, setup.room.ID, accepted.UserID); in {
		t.Fatal("a banned person was admitted by a link")
	}

	if _, err := setup.rooms.Unban(ctx, applications.FirstPartyID, setup.room.ID, accepted.UserID); err != nil {
		t.Fatalf("Unban() error = %v", err)
	}
	if _, err := setup.service.Accept(ctx, biaSigner, "bia", second.ID); err != nil {
		t.Errorf("accepting the same link once the ban was lifted error = %v", err)
	}
}
