package invitations

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"convia/internal/calls"
	"convia/internal/participants"
)

// inviteGuest issues an invitation that names nobody.
func (setup fixture) inviteGuest(t *testing.T, applicationID, callID, role string) Invitation {
	t.Helper()

	invitation, _, err := setup.service.Issue(context.Background(), applicationID, callID,
		Request{Guest: true, Role: role})
	if err != nil {
		t.Fatalf("Issue() a guest invitation error = %v", err)
	}
	return invitation
}

/*
TestAGuestTakesPartWithoutConviaKnowingWhoTheyAre is what M10-010 asked for.

A guest has no Convia user, so the invitation is the whole of their identity.
Everything downstream — the roster, capacity, removal, the credential they
connect with — works on that alone, and Convia never learns a name.
*/
func TestAGuestTakesPartWithoutConviaKnowingWhoTheyAre(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	call := setup.newCall(t, setup.first, nil)
	invitation := setup.inviteGuest(t, setup.first, call.ID, "member")

	if !invitation.Guest() {
		t.Fatal("an invitation issued without a user is not a guest invitation")
	}
	if invitation.UserID != "" {
		t.Errorf("a guest invitation names %q", invitation.UserID)
	}

	redeemed, participant, credential, err := setup.service.Redeem(ctx, invitation)
	if err != nil {
		t.Fatalf("Redeem() error = %v", err)
	}

	if !participant.Guest() {
		t.Error("redeeming a guest invitation produced a participation with a user")
	}
	if participant.UserID != "" {
		t.Errorf("the guest participation names user %q", participant.UserID)
	}
	if participant.InvitationID != invitation.ID {
		t.Errorf("the guest is identified by %q, not by the invitation they redeemed",
			participant.InvitationID)
	}
	if !credential.Issued() {
		t.Error("the guest received no credential to connect with")
	}
	if redeemed.ParticipantID != participant.ID {
		t.Errorf("the invitation names participation %q, not %q", redeemed.ParticipantID, participant.ID)
	}
}

/*
TestAGuestWhoReconnectsReturnsToTheirOwnSeat is the same idempotency a known
person has, resting on a different identity.

One invitation is one presence. Without that, a guest whose connection dropped
would appear twice in the roster and count twice against capacity.
*/
func TestAGuestWhoReconnectsReturnsToTheirOwnSeat(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	call := setup.newCall(t, setup.first, nil)
	invitation := setup.inviteGuest(t, setup.first, call.ID, "member")

	_, first, _, err := setup.service.Redeem(ctx, invitation)
	if err != nil {
		t.Fatalf("Redeem() error = %v", err)
	}

	_, again, _, err := setup.service.Redeem(ctx, invitation)
	if err != nil {
		t.Fatalf("Redeem() again error = %v", err)
	}

	if again.ID != first.ID {
		t.Errorf("a reconnecting guest was seated again as %q, not %q", again.ID, first.ID)
	}

	roster, err := setup.participants.List(ctx, setup.first, call.ID, participants.ListOptions{})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(roster.Participants) != 1 {
		t.Errorf("the roster holds %d entries for one guest", len(roster.Participants))
	}
}

/*
TestTwoGuestsAreTwoPeople proves the identity is the invitation and not the
absence of a user.

If guests collided with one another, a call could hold exactly one of them, and
the whole feature would be a way to seat a single anonymous person.
*/
func TestTwoGuestsAreTwoPeople(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	call := setup.newCall(t, setup.first, nil)
	first := setup.inviteGuest(t, setup.first, call.ID, "member")
	second := setup.inviteGuest(t, setup.first, call.ID, "member")

	_, one, _, err := setup.service.Redeem(ctx, first)
	if err != nil {
		t.Fatalf("Redeem() the first error = %v", err)
	}
	_, other, _, err := setup.service.Redeem(ctx, second)
	if err != nil {
		t.Fatalf("Redeem() the second error = %v", err)
	}

	if one.ID == other.ID {
		t.Fatal("two guests were seated as the same participation")
	}

	roster, err := setup.participants.List(ctx, setup.first, call.ID, participants.ListOptions{})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(roster.Participants) != 2 {
		t.Errorf("the roster holds %d entries for two guests", len(roster.Participants))
	}
}

/*
TestAGuestCountsAgainstCapacity is the rule that would have been easiest to
lose.

Capacity is a property of the room, not of how somebody got in. A guest that
did not count would let an application seat any number of people in a
three-seat room by inviting them instead of admitting them.
*/
func TestAGuestCountsAgainstCapacity(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	limit := 2
	call := setup.newCall(t, setup.first, &limit)

	for attempt := range limit {
		invitation := setup.inviteGuest(t, setup.first, call.ID, "member")
		if _, _, _, err := setup.service.Redeem(ctx, invitation); err != nil {
			t.Fatalf("Redeem() guest %d error = %v", attempt+1, err)
		}
	}

	overflow := setup.inviteGuest(t, setup.first, call.ID, "member")
	if _, _, _, err := setup.service.Redeem(ctx, overflow); !errors.Is(err, participants.ErrCallFull) {
		t.Errorf("Redeem() past capacity error = %v, want %v", err, participants.ErrCallFull)
	}
}

/*
TestARemovedGuestCannotComeBack makes removal mean the same thing for a guest
as for anybody else.

They hold a link that is still valid, so the only thing standing between them
and the conversation is that their participation is terminal — identified by
the invitation, which is exactly what they would present again.
*/
func TestARemovedGuestCannotComeBack(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	call := setup.newCall(t, setup.first, nil)
	invitation := setup.inviteGuest(t, setup.first, call.ID, "member")

	_, participant, _, err := setup.service.Redeem(ctx, invitation)
	if err != nil {
		t.Fatalf("Redeem() error = %v", err)
	}

	if _, err := setup.participants.Remove(ctx, setup.first, participant.ID,
		participants.RemoverApplication, "", "Disruptive."); err != nil {
		t.Fatalf("Remove() error = %v", err)
	}

	if _, _, _, err := setup.service.Redeem(ctx, invitation); !errors.Is(err, participants.ErrRemoved) {
		t.Errorf("Redeem() after a removal error = %v, want %v", err, participants.ErrRemoved)
	}
}

/*
TestAGuestInvitationIsRefusedOnceItStops covers the states that end a link,
against a participation that does not exist yet.

A guest has no other way in, so an invitation that kept working after being
withdrawn would be the whole of the leak.
*/
func TestAGuestInvitationIsRefusedOnceItStops(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	call := setup.newCall(t, setup.first, nil)

	revoked := setup.inviteGuest(t, setup.first, call.ID, "member")
	withdrawn, err := setup.service.Revoke(ctx, setup.first, revoked.ID)
	if err != nil {
		t.Fatalf("Revoke() error = %v", err)
	}
	if _, _, _, err := setup.service.Redeem(ctx, withdrawn); !errors.Is(err, ErrUnusable) {
		t.Errorf("Redeem() a withdrawn guest invitation error = %v, want %v", err, ErrUnusable)
	}

	expired := setup.inviteGuest(t, setup.first, call.ID, "member")
	expired.ExpiresAt = time.Now().UTC().Add(-time.Minute)
	if _, _, _, err := setup.service.Redeem(ctx, expired); !errors.Is(err, ErrUnusable) {
		t.Errorf("Redeem() an expired guest invitation error = %v, want %v", err, ErrUnusable)
	}

	ended := setup.inviteGuest(t, setup.first, call.ID, "member")
	if _, err := setup.calls.End(ctx, setup.first, call.ID, calls.ActorApplication, "Finished."); err != nil {
		t.Fatalf("End() error = %v", err)
	}
	if _, _, _, err := setup.service.Redeem(ctx, ended); !errors.Is(err, ErrCallEnded) {
		t.Errorf("Redeem() into an ended call error = %v, want %v", err, ErrCallEnded)
	}
}

/*
TestAskingForNeitherAPersonNorAGuestIsRefused is the mistake worth refusing.

Treating a request that forgot to name somebody as a guest invitation would
turn a typo into a link anybody could use. Asking for both is refused for the
same reason: the request means two different things and Convia should not pick
one.
*/
func TestAskingForNeitherAPersonNorAGuestIsRefused(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	call := setup.newCall(t, setup.first, nil)
	userID := setup.newUser(t, setup.first, "ana")

	var validation ValidationError

	_, _, err := setup.service.Issue(ctx, setup.first, call.ID, Request{Role: "member"})
	if !errors.As(err, &validation) {
		t.Errorf("Issue() naming nobody error = %v, want a validation error", err)
	}
	if validation.Field != "user_id" {
		t.Errorf("the refusal points at %q, not at the field that was missing", validation.Field)
	}

	_, _, err = setup.service.Issue(ctx, setup.first, call.ID,
		Request{UserID: userID, Guest: true, Role: "member"})
	if !errors.As(err, &validation) {
		t.Errorf("Issue() asking for both error = %v, want a validation error", err)
	}
}

/*
TestConviaWritesNothingDownAboutAGuest is the privacy claim, checked against
the log rather than asserted in prose.

Convia has no name, no address, and no identifier for a guest beyond the
invitation. The audit trail says a guest joined and which invitation let them
in, which is what an incident needs, and nothing more.
*/
func TestConviaWritesNothingDownAboutAGuest(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	call := setup.newCall(t, setup.first, nil)

	setup.logs.Reset()
	invitation := setup.inviteGuest(t, setup.first, call.ID, "member")

	_, participant, _, err := setup.service.Redeem(ctx, invitation)
	if err != nil {
		t.Fatalf("Redeem() error = %v", err)
	}

	logged := setup.logs.String()
	if !strings.Contains(logged, invitation.ID) {
		t.Errorf("the audit trail does not name the invitation: %s", logged)
	}
	if !strings.Contains(logged, participant.ID) {
		t.Errorf("the audit trail does not name the participation: %s", logged)
	}

	/*
		The one thing that must not appear is a user identifier. A guest has
		none, and a log that carried an empty one would be the first sign that
		somewhere a zero value is being treated as a person.
	*/
	if strings.Contains(logged, `"user_id":"usr_`) {
		t.Errorf("the audit trail names a user for a guest: %s", logged)
	}
}

/*
TestAGuestKeepsTheRoleTheirInvitationCarried proves the role travels for guests
too.

Somebody invited to moderate arrives able to moderate, without the application
promoting them after the fact — which it could not do easily, since it does not
know the participation identifier until the guest has already arrived.
*/
func TestAGuestKeepsTheRoleTheirInvitationCarried(t *testing.T) {
	setup := newFixture(t)
	ctx := context.Background()

	call := setup.newCall(t, setup.first, nil)
	invitation := setup.inviteGuest(t, setup.first, call.ID, "moderator")

	_, participant, _, err := setup.service.Redeem(ctx, invitation)
	if err != nil {
		t.Fatalf("Redeem() error = %v", err)
	}
	if participant.Role != participants.RoleModerator {
		t.Errorf("the guest arrived as a %q", participant.Role)
	}
}
